package frameworkmap

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// fixtureDeps builds a mixed Spring/Nest graph plus an unannotated Go symbol.
func fixtureDeps(t *testing.T) resolve.Deps {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()

	mk := func(kind, qn, path string, line int, annotations ...string) {
		t.Helper()
		n, err := model.NewNode(kind, qn, path, line, 1)
		if err != nil {
			t.Fatalf("node %s: %v", qn, err)
		}
		if len(annotations) > 0 {
			n = n.WithMeta(model.NewNodeMeta(annotations, nil))
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatalf("put node %s: %v", qn, err)
		}
	}

	mk("type", "shop.UserController", "src/main/java/shop/UserController.java", 10, "RestController")
	mk("method", "shop.UserController.getUser", "src/main/java/shop/UserController.java", 14, "GetMapping")
	mk("method", "shop.OrderListener.onOrder", "src/main/java/shop/OrderListener.java", 8, "KafkaListener")
	mk("type", "shop.PricingService", "src/main/java/shop/PricingService.java", 5, "Service")
	mk("type", "app.CatController", "src/cats/cat.controller.ts", 6, "Controller")
	mk("method", "app.CatController.findAll", "src/cats/cat.controller.ts", 9, "Get")
	mk("function", "util.Helper", "util/helper.go", 3) // no annotations (Go)

	return resolve.Deps{Query: query.New(store), Search: search.New(store)}
}

func reasons(t *testing.T, res *contract.Result) string {
	t.Helper()
	var b strings.Builder
	for _, it := range res.Items {
		b.WriteString(it.Reason)
		b.WriteString("\n")
	}
	return b.String()
}

func TestFrameworkMapDerivesFacts(t *testing.T) {
	res, err := Assemble(context.Background(), Params{Deps: fixtureDeps(t)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("expected found, got %s (%s)", res.Outcome, res.Summary)
	}
	rs := reasons(t, res)
	for _, want := range []string{
		"route: method shop.UserController.getUser (src/main/java/shop/UserController.java:14) — @GetMapping [spring, GET]",
		"route: method app.CatController.findAll (src/cats/cat.controller.ts:9) — @Get [nest, GET]",
		"event: method shop.OrderListener.onOrder (src/main/java/shop/OrderListener.java:8) — @KafkaListener [spring, kafka]",
		"component: type shop.PricingService (src/main/java/shop/PricingService.java:5) — @Service [spring, service]",
		"component: type app.CatController (src/cats/cat.controller.ts:6) — @Controller [nest, controller]",
	} {
		if !strings.Contains(rs, want) {
			t.Fatalf("missing %q in items:\n%s", want, rs)
		}
	}
	if !strings.Contains(res.Summary, "2 route(s)") || !strings.Contains(res.Summary, "[nest spring]") {
		t.Fatalf("summary must count routes and name providers: %q", res.Summary)
	}
	if len(res.Evidence) == 0 {
		t.Fatal("expected evidence citations")
	}
}

func TestFrameworkMapEmptyOutcomes(t *testing.T) {
	// Unavailable.
	res, err := Assemble(context.Background(), Params{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeUnavailable {
		t.Fatalf("nil deps must degrade to unavailable, got %s", res.Outcome)
	}

	// Empty graph.
	store := graphstore.NewMemStore()
	deps := resolve.Deps{Query: query.New(store), Search: search.New(store)}
	res, err = Assemble(context.Background(), Params{Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeEmpty {
		t.Fatalf("empty graph must yield empty, got %s", res.Outcome)
	}

	// Go-only graph: honest no-annotations empty, naming the provider scope.
	n, err := model.NewNode("function", "p.f", "p/f.go", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutNode(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	res, err = Assemble(context.Background(), Params{Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeEmpty || !strings.Contains(res.Summary, "no framework annotations mapped") {
		t.Fatalf("annotation-free graph must explain itself: %s (%s)", res.Outcome, res.Summary)
	}
}

func TestFrameworkMapDeterministic(t *testing.T) {
	deps := fixtureDeps(t)
	a, err := Assemble(context.Background(), Params{Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Assemble(context.Background(), Params{Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	ab, err := contract.Serialize(a)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := contract.Serialize(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ab, bb) {
		t.Fatalf("framework_map output not byte-deterministic:\n%s\n%s", ab, bb)
	}
}
