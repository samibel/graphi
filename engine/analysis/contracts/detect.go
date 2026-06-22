package contracts

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// Contract is a detected producer or consumer contract surface in the code
// graph. It carries the matched node, the pattern that detected it, and
// the set of fields (child nodes) that define its typed surface shape.
type Contract struct {
	// NodeID is the graph node that anchors this contract.
	NodeID model.NodeId `json:"node_id"`
	// QualifiedName is the fully-qualified name of the contract node.
	QualifiedName string `json:"qualified_name"`
	// SourcePath is the file where the contract was detected.
	SourcePath string `json:"source_path"`
	// Line is the 1-based line of the contract node.
	Line int `json:"line"`
	// Role is the contract side: producer or consumer.
	Role Role `json:"role"`
	// Protocol is the communication protocol.
	Protocol Protocol `json:"protocol"`
	// PatternID is the pattern that matched this contract.
	PatternID string `json:"pattern_id"`
	// Fields is the set of typed fields/parameters that define the contract's
	// surface shape. Sorted by Name for determinism.
	Fields []Field `json:"fields,omitempty"`
}

// Field is a named, typed element in a contract's surface shape. It is used
// for drift comparison between producer and consumer.
type Field struct {
	// Name is the field identifier.
	Name string `json:"name"`
	// Type is the field's type representation (e.g. "string", "int64",
	// "[]byte"). Compared structurally during drift detection.
	Type string `json:"type"`
	// Required indicates whether the field is mandatory. Changing a required
	// field is a breaking drift; adding an optional field is non-breaking.
	Required bool `json:"required"`
}

// ServiceKey extracts a logical service key from a contract's qualified name
// for cross-service matching. It strips the specific function/method to yield
// the package or service-level prefix that two sides of a contract share.
// For example, "myapp/users.RegisterServer" and "myapp/users.NewClient" both
// yield "myapp/users" as their service key.
func (c Contract) ServiceKey() string {
	name := c.QualifiedName
	// For gRPC patterns, strip the function suffix to get the service package.
	if idx := strings.LastIndex(name, "."); idx > 0 {
		return name[:idx]
	}
	return name
}

// detectContracts scans all graph nodes against the pattern registry and
// returns the detected contracts with their fields. Fields are extracted by
// following edges from the matched node to child/related nodes.
func detectContracts(
	ctx context.Context,
	r query.Reader,
	registry *PatternRegistry,
) ([]Contract, error) {
	nodes, err := r.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return nil, fmt.Errorf("contracts: load nodes: %w", err)
	}
	edges, err := r.Edges(ctx, graphstore.Query{})
	if err != nil {
		return nil, fmt.Errorf("contracts: load edges: %w", err)
	}

	// Build forward adjacency for field extraction.
	adj := make(map[model.NodeId][]model.Edge)
	for _, e := range edges {
		adj[e.From()] = append(adj[e.From()], e)
	}
	// Sort adjacency lists for deterministic traversal.
	for k := range adj {
		entries := adj[k]
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].ID() < entries[j].ID()
		})
		adj[k] = entries
	}

	// Index nodes by ID for field extraction.
	nodeByID := make(map[model.NodeId]model.Node, len(nodes))
	for _, n := range nodes {
		nodeByID[n.ID()] = n
	}

	patterns := registry.All()
	var contracts []Contract

	for _, n := range nodes {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		for _, p := range patterns {
			if !p.Matches(n.Kind(), n.QualifiedName()) {
				continue
			}
			c := Contract{
				NodeID:        n.ID(),
				QualifiedName: n.QualifiedName(),
				SourcePath:    n.SourcePath(),
				Line:          n.Line(),
				Role:          p.Role,
				Protocol:      p.Protocol,
				PatternID:     p.ID,
				Fields:        extractFields(n.ID(), adj, nodeByID, p.EdgeKinds),
			}
			contracts = append(contracts, c)
			break // first matching pattern wins per node
		}
	}

	// Sort contracts deterministically: by Role, then Protocol, then NodeID.
	sort.Slice(contracts, func(i, j int) bool {
		a, b := contracts[i], contracts[j]
		if a.Role != b.Role {
			return a.Role < b.Role
		}
		if a.Protocol != b.Protocol {
			return a.Protocol < b.Protocol
		}
		return a.NodeID < b.NodeID
	})

	return contracts, nil
}

// extractFields follows edges of the specified kinds from the anchor node and
// collects the reachable nodes as typed fields. This is a shallow (depth-1)
// traversal: it picks up direct children only (parameters, return types, struct
// fields that the producer/consumer node defines or references).
func extractFields(
	anchor model.NodeId,
	adj map[model.NodeId][]model.Edge,
	nodeByID map[model.NodeId]model.Node,
	edgeKinds []string,
) []Field {
	if len(edgeKinds) == 0 {
		return nil
	}
	kindSet := make(map[string]struct{}, len(edgeKinds))
	for _, k := range edgeKinds {
		kindSet[k] = struct{}{}
	}

	var fields []Field
	for _, e := range adj[anchor] {
		if _, ok := kindSet[e.Kind()]; !ok {
			continue
		}
		target, ok := nodeByID[e.To()]
		if !ok {
			continue
		}
		f := Field{
			Name:     target.QualifiedName(),
			Type:     target.Kind(),
			Required: true, // default: fields reachable from a contract are required
		}
		fields = append(fields, f)
	}

	// Sort fields for determinism.
	sort.Slice(fields, func(i, j int) bool {
		return fields[i].Name < fields[j].Name
	})

	return fields
}

// matchProducerConsumer links producer and consumer contracts that reference
// the same logical service surface. Two contracts match when they share the
// same Protocol and their ServiceKeys are equal. Returns matched pairs as
// ContractLink values.
func matchProducerConsumer(contracts []Contract) []ContractLink {
	producers := make(map[string][]int) // serviceKey → indices into contracts
	var consumerIdxs []int

	for i, c := range contracts {
		key := c.Protocol.String() + ":" + c.ServiceKey()
		if c.Role == RoleProducer {
			producers[key] = append(producers[key], i)
		} else {
			consumerIdxs = append(consumerIdxs, i)
		}
	}

	var links []ContractLink
	for _, ci := range consumerIdxs {
		consumer := contracts[ci]
		key := consumer.Protocol.String() + ":" + consumer.ServiceKey()
		prodIdxs, ok := producers[key]
		if !ok {
			continue
		}
		for _, pi := range prodIdxs {
			producer := contracts[pi]
			links = append(links, ContractLink{
				ProducerNodeID: producer.NodeID,
				ConsumerNodeID: consumer.NodeID,
				Protocol:       producer.Protocol,
				ServiceKey:     producer.ServiceKey(),
				ProducerFields: producer.Fields,
				ConsumerFields: consumer.Fields,
			})
		}
	}

	// Sort links for determinism.
	sort.Slice(links, func(i, j int) bool {
		a, b := links[i], links[j]
		if a.ServiceKey != b.ServiceKey {
			return a.ServiceKey < b.ServiceKey
		}
		if a.ProducerNodeID != b.ProducerNodeID {
			return a.ProducerNodeID < b.ProducerNodeID
		}
		return a.ConsumerNodeID < b.ConsumerNodeID
	})

	return links
}

// ContractLink is a matched producer↔consumer pair.
type ContractLink struct {
	// ProducerNodeID is the producer side's graph node.
	ProducerNodeID model.NodeId `json:"producer_node_id"`
	// ConsumerNodeID is the consumer side's graph node.
	ConsumerNodeID model.NodeId `json:"consumer_node_id"`
	// Protocol is the shared communication protocol.
	Protocol Protocol `json:"protocol"`
	// ServiceKey is the logical service surface both sides reference.
	ServiceKey string `json:"service_key"`
	// ProducerFields is the producer's typed surface shape.
	ProducerFields []Field `json:"producer_fields,omitempty"`
	// ConsumerFields is the consumer's typed surface shape.
	ConsumerFields []Field `json:"consumer_fields,omitempty"`
}

// String returns the protocol as a string.
func (p Protocol) String() string { return string(p) }
