package contracts

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Protocol identifies the communication protocol a contract pattern targets.
type Protocol string

const (
	// ProtocolHTTP matches HTTP REST endpoint contracts.
	ProtocolHTTP Protocol = "http"
	// ProtocolGRPC matches gRPC service definition contracts.
	ProtocolGRPC Protocol = "grpc"
)

// Role distinguishes the two sides of a contract.
type Role string

const (
	// RoleProducer is the defining/serving side (handler, service impl).
	RoleProducer Role = "producer"
	// RoleConsumer is the calling/depending side (client, caller).
	RoleConsumer Role = "consumer"
)

// ContractPattern is a versioned rule for detecting a producer or consumer
// contract in the code graph. Patterns are registered in the PatternRegistry
// and matched against graph nodes during detection.
type ContractPattern struct {
	// ID is the unique, stable identifier for this pattern (e.g.
	// "http.handler.net-http"). Used in provenance and diagnostics.
	ID string
	// Version is the semantic version of this pattern definition (e.g.
	// "1.0.0"). Enables forward-compatible evolution without breaking
	// existing matches.
	Version string
	// Protocol is the communication protocol this pattern targets.
	Protocol Protocol
	// Role is the contract side this pattern detects.
	Role Role
	// NodeKinds is the set of node kinds this pattern matches against (e.g.
	// "function", "method", "type"). An empty set matches any kind.
	NodeKinds []string
	// NamePatterns is the set of substring patterns matched against the
	// node's qualified name (e.g. "net/http.Handle", "google.golang.org/grpc").
	// A node matches if its qualified name contains ANY of these substrings.
	NamePatterns []string
	// EdgeKinds is the set of edge kinds that connect a matched node to its
	// contract surface (e.g. "calls", "implements"). Used during graph
	// traversal to find the related fields/types. An empty set means no
	// edge traversal is performed.
	EdgeKinds []string
}

// Matches reports whether the pattern matches a node with the given kind and
// qualified name. A match requires: (1) the node kind is in NodeKinds (or
// NodeKinds is empty), AND (2) the qualified name contains at least one of the
// NamePatterns substrings.
func (p ContractPattern) Matches(nodeKind, qualifiedName string) bool {
	if len(p.NamePatterns) == 0 {
		return false
	}
	if len(p.NodeKinds) > 0 {
		found := false
		for _, k := range p.NodeKinds {
			if k == nodeKind {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for _, sub := range p.NamePatterns {
		if strings.Contains(qualifiedName, sub) {
			return true
		}
	}
	return false
}

// PatternRegistry is the concurrency-safe, versioned registry of contract
// detection patterns. It mirrors the registry pattern used in the parent
// analysis package: duplicate IDs are rejected, and the set is immutable after
// registration.
type PatternRegistry struct {
	mu       sync.RWMutex
	patterns map[string]ContractPattern // keyed by ID
}

// NewPatternRegistry returns an empty, ready-to-use PatternRegistry.
func NewPatternRegistry() *PatternRegistry {
	return &PatternRegistry{patterns: make(map[string]ContractPattern)}
}

// Register adds a pattern to the registry. It returns an error if the pattern
// has an empty ID, empty version, or a duplicate ID.
func (r *PatternRegistry) Register(p ContractPattern) error {
	if p.ID == "" {
		return fmt.Errorf("contracts: register pattern with empty ID")
	}
	if p.Version == "" {
		return fmt.Errorf("contracts: register pattern %q with empty version", p.ID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.patterns[p.ID]; exists {
		return fmt.Errorf("contracts: pattern %q already registered", p.ID)
	}
	r.patterns[p.ID] = p
	return nil
}

// Get returns the pattern registered under id and whether one exists.
func (r *PatternRegistry) Get(id string) (ContractPattern, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.patterns[id]
	return p, ok
}

// All returns all registered patterns in deterministic ID order.
func (r *PatternRegistry) All() []ContractPattern {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ContractPattern, 0, len(r.patterns))
	for _, p := range r.patterns {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ByRole returns all patterns with the given role in deterministic ID order.
func (r *PatternRegistry) ByRole(role Role) []ContractPattern {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []ContractPattern
	for _, p := range r.patterns {
		if p.Role == role {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// IDs returns the sorted list of registered pattern IDs.
func (r *PatternRegistry) IDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.patterns))
	for id := range r.patterns {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// DefaultPatternRegistry returns a PatternRegistry pre-populated with the
// built-in v1 patterns for HTTP and gRPC producer/consumer detection.
func DefaultPatternRegistry() *PatternRegistry {
	r := NewPatternRegistry()
	for _, p := range defaultPatterns {
		// Best-effort at construction; duplicate-ID would indicate a
		// programming fault, so we panic to surface it immediately.
		if err := r.Register(p); err != nil {
			panic(fmt.Sprintf("contracts: default pattern registration: %v", err))
		}
	}
	return r
}

// defaultPatterns is the built-in set of v1 contract detection patterns.
var defaultPatterns = []ContractPattern{
	// HTTP producers — handlers that serve endpoints.
	{
		ID:           "http.producer.net-http",
		Version:      "1.0.0",
		Protocol:     ProtocolHTTP,
		Role:         RoleProducer,
		NodeKinds:    []string{"function", "method"},
		NamePatterns: []string{"net/http.Handle", "http.HandleFunc", "http.ServeMux"},
		EdgeKinds:    []string{"calls", "defines"},
	},
	{
		ID:           "http.producer.gin",
		Version:      "1.0.0",
		Protocol:     ProtocolHTTP,
		Role:         RoleProducer,
		NodeKinds:    []string{"function", "method"},
		NamePatterns: []string{"gin.Engine.GET", "gin.Engine.POST", "gin.Engine.PUT", "gin.Engine.DELETE", "gin.RouterGroup"},
		EdgeKinds:    []string{"calls", "defines"},
	},
	{
		ID:           "http.producer.echo",
		Version:      "1.0.0",
		Protocol:     ProtocolHTTP,
		Role:         RoleProducer,
		NodeKinds:    []string{"function", "method"},
		NamePatterns: []string{"echo.Echo.GET", "echo.Echo.POST", "echo.Echo.PUT", "echo.Echo.DELETE", "echo.Group"},
		EdgeKinds:    []string{"calls", "defines"},
	},
	{
		ID:           "http.producer.chi",
		Version:      "1.0.0",
		Protocol:     ProtocolHTTP,
		Role:         RoleProducer,
		NodeKinds:    []string{"function", "method"},
		NamePatterns: []string{"chi.Mux.Get", "chi.Mux.Post", "chi.Mux.Put", "chi.Mux.Delete", "chi.NewRouter"},
		EdgeKinds:    []string{"calls", "defines"},
	},
	// HTTP consumers — client call sites.
	{
		ID:           "http.consumer.net-http",
		Version:      "1.0.0",
		Protocol:     ProtocolHTTP,
		Role:         RoleConsumer,
		NodeKinds:    []string{"function", "method", "call"},
		NamePatterns: []string{"net/http.Get", "net/http.Post", "http.Client.Do", "http.NewRequest"},
		EdgeKinds:    []string{"calls", "references"},
	},
	// gRPC producers — service implementations.
	{
		ID:           "grpc.producer.service",
		Version:      "1.0.0",
		Protocol:     ProtocolGRPC,
		Role:         RoleProducer,
		NodeKinds:    []string{"function", "method", "type"},
		NamePatterns: []string{"RegisterServer", "grpc.ServiceDesc", "pb.Register"},
		EdgeKinds:    []string{"implements", "defines"},
	},
	// gRPC consumers — client stubs.
	{
		ID:           "grpc.consumer.client",
		Version:      "1.0.0",
		Protocol:     ProtocolGRPC,
		Role:         RoleConsumer,
		NodeKinds:    []string{"function", "method", "call"},
		NamePatterns: []string{"NewClient", "grpc.Dial", "grpc.NewClient", "pb.New"},
		EdgeKinds:    []string{"calls", "references"},
	},
}
