package ingest_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/query"
)

// TestLink_TypeScript_CrossFileQueries proves the user-visible FU-5 outcome for the
// TypeScript family: a multi-directory repo, ingested through the production TS
// parser + the FU-5 tsResolver, answers Callees/Callers ACROSS files and resolves
// the cross-file import binding to a heuristic-tier edge with file:line evidence.
func TestLink_TypeScript_CrossFileQueries(t *testing.T) {
	ctx := context.Background()
	repo := writeRepo(t, map[string]string{
		"app/main.ts": `import { price } from "../shop/price";
import * as taxlib from "../tax/tax";
export function checkout(): number { return price() + taxlib.rate(); }
`,
		"shop/price.ts": `export function price(): number { return 10; }
`,
		"tax/tax.ts": `export function rate(): number { return 7; }
`,
	})
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	if err := ing.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatal(err)
	}
	id := func(qn string) model.NodeId {
		for _, n := range nodes {
			if n.QualifiedName() == qn {
				return n.ID()
			}
		}
		t.Fatalf("symbol %q not found among %d nodes", qn, len(nodes))
		return ""
	}

	svc := query.New(store)

	// Named-import cross-file call: checkout -> price.
	callees, err := svc.Callees(ctx, id("app.checkout"))
	if err != nil {
		t.Fatal(err)
	}
	if !containsNode(callees, id("shop.price")) {
		t.Errorf("Callees(checkout) missing cross-file price: %+v", callees.Nodes)
	}
	// Namespace-import cross-file call: checkout -> tax.rate.
	if !containsNode(callees, id("tax.rate")) {
		t.Errorf("Callees(checkout) missing namespace tax.rate: %+v", callees.Nodes)
	}
	callers, err := svc.Callers(ctx, id("shop.price"))
	if err != nil {
		t.Fatal(err)
	}
	if !containsNode(callers, id("app.checkout")) {
		t.Errorf("Callers(price) missing checkout: %+v", callers.Nodes)
	}

	// The cross-file edge is heuristic tier with evidence, and no edge targets an
	// absent node.
	edges, err := store.Edges(ctx, graphstore.Query{EdgeKind: "calls"})
	if err != nil {
		t.Fatal(err)
	}
	var foundXfile bool
	for _, e := range edges {
		if e.From() == id("app.checkout") && e.To() == id("shop.price") {
			foundXfile = true
			if e.Tier() != model.TierHeuristic {
				t.Errorf("cross-file TS edge tier = %q, want heuristic", e.Tier())
			}
			if len(e.Evidence()) == 0 {
				t.Errorf("cross-file TS edge has no evidence")
			}
		}
		if _, err := store.GetNode(ctx, e.To()); err != nil {
			t.Errorf("calls edge to absent target %s", e.To())
		}
	}
	if !foundXfile {
		t.Error("no cross-file checkout->price calls edge emitted")
	}
}

// TestLink_TypeScript_GoldenIncrementalVsFull drives the sacred byte-identical
// invariant with the PRODUCTION TS parser across a rename-of-target (cross-file
// caller must re-link), an added same-dir sibling, and a deleted file. It would
// FALSE-GREEN on an empty-edge stub, so it is the real cross-file proof.
func TestLink_TypeScript_GoldenIncrementalVsFull(t *testing.T) {
	ctx := context.Background()

	initial := map[string]string{
		"app/main.ts": `import { price } from "../shop/price";
export function checkout(): number { return price(); }
`,
		"shop/price.ts": `export function price(): number { return 10; }
`,
		"util/util.ts": `export function helper(): number { return 1; }
`,
	}

	storeInc := graphstore.NewMemStore()
	t.Cleanup(func() { _ = storeInc.Close() })
	iInc := newIngester(t, storeInc, parse.NewDefaultRegistry())
	repo := writeRepo(t, initial)
	if err := iInc.IngestAll(ctx, repo); err != nil {
		t.Fatalf("inc IngestAll: %v", err)
	}

	// Mutate: rename price()->cost() (cross-file caller checkout must re-link),
	// add a same-dir sibling, delete util.
	mustWrite(t, repo, "shop/price.ts", `export function cost(): number { return 10; }
`)
	mustWrite(t, repo, "app/main.ts", `import { cost } from "../shop/price";
export function checkout(): number { return cost(); }
`)
	mustWrite(t, repo, "shop/extra.ts", `import { cost } from "./price";
export function extra(): number { return cost(); }
`)
	if err := os.Remove(filepath.Join(repo, "util/util.ts")); err != nil {
		t.Fatalf("rm util: %v", err)
	}
	if err := iInc.IngestChanged(ctx, repo, []string{"app/main.ts", "shop/price.ts", "shop/extra.ts", "util/util.ts"}); err != nil {
		t.Fatalf("incremental: %v", err)
	}
	incSnap := filepath.Join(t.TempDir(), "inc")
	if err := storeInc.Snapshot(ctx, incSnap); err != nil {
		t.Fatalf("inc snapshot: %v", err)
	}
	incBytes, _ := os.ReadFile(incSnap)

	mutated := map[string]string{
		"app/main.ts": `import { cost } from "../shop/price";
export function checkout(): number { return cost(); }
`,
		"shop/price.ts": `export function cost(): number { return 10; }
`,
		"shop/extra.ts": `import { cost } from "./price";
export function extra(): number { return cost(); }
`,
	}
	fullBytes := fullSnapshotBytes(t, mutated)

	if !bytes.Equal(incBytes, fullBytes) {
		t.Fatalf("TS incremental != full (byte-level, incl. provenance):\ninc =%s\nfull=%s", incBytes, fullBytes)
	}
}
