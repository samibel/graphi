package contracts

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// testReader implements query.Reader over in-memory slices for testing.
type testReader struct {
	nodes []model.Node
	edges []model.Edge
}

func (r *testReader) GetNode(_ context.Context, id model.NodeId) (model.Node, error) {
	for _, n := range r.nodes {
		if n.ID() == id {
			return n, nil
		}
	}
	return model.Node{}, graphstore.ErrNotFound
}

func (r *testReader) GetEdge(_ context.Context, id model.EdgeId) (model.Edge, error) {
	for _, e := range r.edges {
		if e.ID() == id {
			return e, nil
		}
	}
	return model.Edge{}, graphstore.ErrNotFound
}

func (r *testReader) Nodes(_ context.Context, _ graphstore.Query) ([]model.Node, error) {
	return r.nodes, nil
}

func (r *testReader) Edges(_ context.Context, _ graphstore.Query) ([]model.Edge, error) {
	return r.edges, nil
}

var _ query.Reader = (*testReader)(nil)

// helper to create a node (panics on error for test brevity).
func mustNode(t *testing.T, kind, name, path string, line, col int) model.Node {
	t.Helper()
	n, err := model.NewNode(kind, name, path, line, col)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// helper to create an edge (panics on error for test brevity).
func mustEdge(t *testing.T, from, to model.NodeId, kind string) model.Edge {
	t.Helper()
	e, err := model.NewEdge(from, to, kind,
		model.TierDerived, 0.9, "contract test", []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// TestSimpleContractMatch verifies that a single HTTP producer is detected
// when a node matches a producer pattern.
func TestSimpleContractMatch(t *testing.T) {
	handler := mustNode(t, "function", "net/http.HandleFunc", "server.go", 10, 1)
	reader := &testReader{nodes: []model.Node{handler}}

	a := New(nil) // default patterns
	result, err := a.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Contracts) != 1 {
		t.Fatalf("expected 1 contract, got %d", len(result.Contracts))
	}

	c := result.Contracts[0]
	if c.Role != RoleProducer {
		t.Errorf("role: want producer, got %s", c.Role)
	}
	if c.Protocol != ProtocolHTTP {
		t.Errorf("protocol: want http, got %s", c.Protocol)
	}
	if c.NodeID != handler.ID() {
		t.Errorf("node: want %s, got %s", handler.ID(), c.NodeID)
	}
}

// TestBreakingDrift verifies that a type change between producer and consumer
// fields is classified as a breaking drift.
func TestBreakingDrift(t *testing.T) {
	// Producer: HTTP handler with a "string" field.
	producer := mustNode(t, "function", "myapp/users.RegisterServer", "server.go", 10, 1)
	prodField := mustNode(t, "string", "myapp/users.UserID", "server.go", 11, 1)
	prodEdge := mustEdge(t, producer.ID(), prodField.ID(), "defines")

	// Consumer: HTTP client expecting an "int" field with the same name.
	consumer := mustNode(t, "call", "myapp/users.NewClient", "client.go", 20, 1)
	consField := mustNode(t, "int", "myapp/users.UserID", "client.go", 21, 1)
	consEdge := mustEdge(t, consumer.ID(), consField.ID(), "calls")

	reader := &testReader{
		nodes: []model.Node{producer, prodField, consumer, consField},
		edges: []model.Edge{prodEdge, consEdge},
	}

	// Custom registry matching our test nodes.
	reg := NewPatternRegistry()
	must(t, reg.Register(ContractPattern{
		ID: "test.producer", Version: "1.0.0", Protocol: ProtocolGRPC,
		Role: RoleProducer, NodeKinds: []string{"function"},
		NamePatterns: []string{"RegisterServer"}, EdgeKinds: []string{"defines"},
	}))
	must(t, reg.Register(ContractPattern{
		ID: "test.consumer", Version: "1.0.0", Protocol: ProtocolGRPC,
		Role: RoleConsumer, NodeKinds: []string{"call"},
		NamePatterns: []string{"NewClient"}, EdgeKinds: []string{"calls"},
	}))

	a := New(reg)
	result, err := a.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Contracts) != 2 {
		t.Fatalf("expected 2 contracts, got %d", len(result.Contracts))
	}
	if len(result.Links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(result.Links))
	}
	if len(result.Drifts) == 0 {
		t.Fatal("expected at least one drift (type changed), got none")
	}

	foundTypeChange := false
	for _, d := range result.Drifts {
		if d.Kind == DriftTypeChanged && d.Severity == Breaking {
			foundTypeChange = true
		}
	}
	if !foundTypeChange {
		t.Error("expected a breaking type-changed drift")
	}
}

// TestNonBreakingDrift verifies that an optional field present in the producer
// but missing from the consumer is classified as non-breaking.
func TestNonBreakingDrift(t *testing.T) {
	producerFields := []Field{
		{Name: "id", Type: "string", Required: true},
		{Name: "nickname", Type: "string", Required: false},
	}
	consumerFields := []Field{
		{Name: "id", Type: "string", Required: true},
		// "nickname" is missing → non-breaking since it's optional.
	}

	link := ContractLink{
		ProducerNodeID: "prod01",
		ConsumerNodeID: "cons01",
		ServiceKey:     "myapp/users",
		ProducerFields: producerFields,
		ConsumerFields: consumerFields,
	}

	drifts := detectDrifts([]ContractLink{link})
	if len(drifts) != 1 {
		t.Fatalf("expected 1 drift, got %d", len(drifts))
	}
	if drifts[0].Severity != NonBreaking {
		t.Errorf("severity: want non-breaking, got %s", drifts[0].Severity)
	}
	if drifts[0].Kind != DriftFieldAdded {
		t.Errorf("kind: want field_added, got %s", drifts[0].Kind)
	}
}

// TestNoContracts verifies that an empty graph yields an empty result with no
// error.
func TestNoContracts(t *testing.T) {
	reader := &testReader{}
	a := New(nil)

	result, err := a.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Contracts) != 0 {
		t.Errorf("expected 0 contracts, got %d", len(result.Contracts))
	}
	if len(result.Drifts) != 0 {
		t.Errorf("expected 0 drifts, got %d", len(result.Drifts))
	}
}

// TestHTTPEndpointMatch verifies that HTTP producer and consumer patterns
// detect and link matching endpoint pairs.
func TestHTTPEndpointMatch(t *testing.T) {
	// Producer: net/http handler.
	handler := mustNode(t, "function", "myapp/api.net/http.HandleFunc", "api.go", 10, 1)
	// Consumer: net/http client.
	client := mustNode(t, "call", "myapp/api.net/http.Get", "client.go", 20, 1)

	reader := &testReader{
		nodes: []model.Node{handler, client},
	}

	// Custom registry for this test.
	reg := NewPatternRegistry()
	must(t, reg.Register(ContractPattern{
		ID: "http.prod", Version: "1.0.0", Protocol: ProtocolHTTP,
		Role: RoleProducer, NodeKinds: []string{"function"},
		NamePatterns: []string{"net/http.HandleFunc"},
	}))
	must(t, reg.Register(ContractPattern{
		ID: "http.cons", Version: "1.0.0", Protocol: ProtocolHTTP,
		Role: RoleConsumer, NodeKinds: []string{"call"},
		NamePatterns: []string{"net/http.Get"},
	}))

	a := New(reg)
	result, err := a.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Contracts) != 2 {
		t.Fatalf("expected 2 contracts (1 producer, 1 consumer), got %d", len(result.Contracts))
	}

	hasProducer, hasConsumer := false, false
	for _, c := range result.Contracts {
		if c.Role == RoleProducer {
			hasProducer = true
		}
		if c.Role == RoleConsumer {
			hasConsumer = true
		}
	}
	if !hasProducer || !hasConsumer {
		t.Errorf("expected both producer and consumer; producer=%v consumer=%v", hasProducer, hasConsumer)
	}

	if len(result.Links) != 1 {
		t.Fatalf("expected 1 link (same service key), got %d", len(result.Links))
	}
}

// TestMultipleProducers verifies that when multiple producers share a service
// key with a single consumer, each producer is linked independently.
func TestMultipleProducers(t *testing.T) {
	prod1 := mustNode(t, "function", "myapp/orders.RegisterServer", "svc1.go", 10, 1)
	prod2 := mustNode(t, "function", "myapp/orders.RegisterServer.v2", "svc2.go", 10, 1)
	consumer := mustNode(t, "call", "myapp/orders.NewClient", "client.go", 20, 1)

	reader := &testReader{
		nodes: []model.Node{prod1, prod2, consumer},
	}

	reg := NewPatternRegistry()
	must(t, reg.Register(ContractPattern{
		ID: "grpc.prod", Version: "1.0.0", Protocol: ProtocolGRPC,
		Role: RoleProducer, NodeKinds: []string{"function"},
		NamePatterns: []string{"RegisterServer"},
	}))
	must(t, reg.Register(ContractPattern{
		ID: "grpc.cons", Version: "1.0.0", Protocol: ProtocolGRPC,
		Role: RoleConsumer, NodeKinds: []string{"call"},
		NamePatterns: []string{"NewClient"},
	}))

	a := New(reg)
	result, err := a.Run(context.Background(), reader)
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Contracts) != 3 {
		t.Fatalf("expected 3 contracts (2 producers, 1 consumer), got %d", len(result.Contracts))
	}

	producers := 0
	for _, c := range result.Contracts {
		if c.Role == RoleProducer {
			producers++
		}
	}
	if producers != 2 {
		t.Errorf("expected 2 producers, got %d", producers)
	}
}

// TestDeterminism verifies that repeated runs over the same graph yield
// identical results.
func TestDeterminism(t *testing.T) {
	handler := mustNode(t, "function", "myapp/api.net/http.HandleFunc", "api.go", 10, 1)
	client := mustNode(t, "call", "myapp/api.net/http.Get", "client.go", 20, 1)
	field1 := mustNode(t, "string", "myapp/api.Name", "api.go", 11, 1)
	field2 := mustNode(t, "int", "myapp/api.Age", "api.go", 12, 1)

	e1 := mustEdge(t, handler.ID(), field1.ID(), "defines")
	e2 := mustEdge(t, handler.ID(), field2.ID(), "defines")
	e3 := mustEdge(t, client.ID(), field1.ID(), "calls")

	reader := &testReader{
		nodes: []model.Node{handler, client, field1, field2},
		edges: []model.Edge{e1, e2, e3},
	}

	reg := NewPatternRegistry()
	must(t, reg.Register(ContractPattern{
		ID: "http.prod", Version: "1.0.0", Protocol: ProtocolHTTP,
		Role: RoleProducer, NodeKinds: []string{"function"},
		NamePatterns: []string{"net/http.HandleFunc"}, EdgeKinds: []string{"defines"},
	}))
	must(t, reg.Register(ContractPattern{
		ID: "http.cons", Version: "1.0.0", Protocol: ProtocolHTTP,
		Role: RoleConsumer, NodeKinds: []string{"call"},
		NamePatterns: []string{"net/http.Get"}, EdgeKinds: []string{"calls"},
	}))

	a := New(reg)

	var firstResult ContractResult
	for i := 0; i < 5; i++ {
		result, err := a.Run(context.Background(), reader)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			firstResult = result
			continue
		}
		if len(result.Contracts) != len(firstResult.Contracts) {
			t.Fatalf("run %d: contract count differs: %d vs %d", i, len(result.Contracts), len(firstResult.Contracts))
		}
		for j := range result.Contracts {
			if result.Contracts[j].NodeID != firstResult.Contracts[j].NodeID {
				t.Errorf("run %d contract %d: NodeID differs", i, j)
			}
			if result.Contracts[j].Role != firstResult.Contracts[j].Role {
				t.Errorf("run %d contract %d: Role differs", i, j)
			}
		}
		if len(result.Drifts) != len(firstResult.Drifts) {
			t.Fatalf("run %d: drift count differs: %d vs %d", i, len(result.Drifts), len(firstResult.Drifts))
		}
		for j := range result.Drifts {
			if result.Drifts[j].Kind != firstResult.Drifts[j].Kind {
				t.Errorf("run %d drift %d: Kind differs", i, j)
			}
			if result.Drifts[j].FieldName != firstResult.Drifts[j].FieldName {
				t.Errorf("run %d drift %d: FieldName differs", i, j)
			}
		}
	}
}

// TestRegistryIntegration verifies the Analyzer Name and that the default
// registry ships with expected patterns.
func TestRegistryIntegration(t *testing.T) {
	a := New(nil)
	if a.Name() != "contracts" {
		t.Errorf("name: want contracts, got %s", a.Name())
	}

	ids := a.Registry().IDs()
	if len(ids) == 0 {
		t.Fatal("default registry should have at least one pattern")
	}

	// Verify a known pattern exists.
	p, ok := a.Registry().Get("http.producer.net-http")
	if !ok {
		t.Fatal("expected http.producer.net-http pattern in default registry")
	}
	if p.Version != "1.0.0" {
		t.Errorf("version: want 1.0.0, got %s", p.Version)
	}
}

// TestPatternMatches verifies ContractPattern.Matches logic.
func TestPatternMatches(t *testing.T) {
	p := ContractPattern{
		ID:           "test",
		Version:      "1.0.0",
		NodeKinds:    []string{"function", "method"},
		NamePatterns: []string{"http.Handle"},
	}

	if !p.Matches("function", "net/http.HandleFunc") {
		t.Error("should match function with http.Handle substring")
	}
	if p.Matches("type", "net/http.HandleFunc") {
		t.Error("should not match type (not in NodeKinds)")
	}
	if p.Matches("function", "fmt.Println") {
		t.Error("should not match unrelated name")
	}

	// Empty NodeKinds matches any kind.
	p2 := ContractPattern{
		ID: "any", Version: "1.0.0",
		NamePatterns: []string{"grpc.Dial"},
	}
	if !p2.Matches("call", "google.golang.org/grpc.Dial") {
		t.Error("empty NodeKinds should match any kind")
	}
}

// TestRegistryDuplicateRejection verifies that duplicate pattern IDs are
// rejected.
func TestRegistryDuplicateRejection(t *testing.T) {
	reg := NewPatternRegistry()
	p := ContractPattern{ID: "dup", Version: "1.0.0", NamePatterns: []string{"x"}}
	if err := reg.Register(p); err != nil {
		t.Fatalf("first register should succeed: %v", err)
	}
	if err := reg.Register(p); err == nil {
		t.Fatal("duplicate register should return error")
	}
}

// TestNoDriftWhenSurfacesMatch verifies that matching producer and consumer
// surfaces produce zero drifts.
func TestNoDriftWhenSurfacesMatch(t *testing.T) {
	fields := []Field{
		{Name: "id", Type: "string", Required: true},
		{Name: "name", Type: "string", Required: true},
	}

	link := ContractLink{
		ProducerNodeID: "prod01",
		ConsumerNodeID: "cons01",
		ServiceKey:     "myapp/users",
		ProducerFields: fields,
		ConsumerFields: fields,
	}

	drifts := detectDrifts([]ContractLink{link})
	if len(drifts) != 0 {
		t.Errorf("expected 0 drifts for matching surfaces, got %d", len(drifts))
	}
}

// must is a test helper that fails the test if err is non-nil.
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
