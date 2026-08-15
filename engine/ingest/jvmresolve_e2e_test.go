package ingest_test

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/semantic"
)

// jvmFixture: a Java caller into a Kotlin callee — the cross-language case
// the JVM registrants' both-language Input exists for.
func jvmFixture() map[string]string {
	return map[string]string{
		"com/tax/Rate.kt": `package com.tax

class Rate(val n: Int) {
    fun value() {}
}
`,
		"com/shop/Shop.java": `package com.shop;
import com.tax.Rate;
public class Shop {
    public void run(Rate param) {
        param.value();
    }
}
`,
	}
}

// confirmedJVMEdge finds the confirmed shop.run → tax.value calls edge.
func confirmedJVMEdge(t *testing.T, store graphstore.Graphstore) (model.Edge, bool) {
	t.Helper()
	e, ok := edgeBetween(t, store, "shop.run", "tax.value", "calls")
	if !ok {
		return model.Edge{}, false
	}
	return e, e.Tier() == model.TierConfirmed
}

// TestJVMResolve_LiveOptIn is the FIRST LIVE-PATH proof of the JVM binder:
// with the experimental opt-in set, a real IngestAll runs the java registrant
// behind the ADR 0007 seam and the confirmed cross-language edge lands in the
// store at confidence 1.0 with the D1 reason.
func TestJVMResolve_LiveOptIn(t *testing.T) {
	ctx := context.Background()
	t.Setenv(semantic.EnvJVM, "1")
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	root := writeRepo(t, jvmFixture())
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	e, confirmed := confirmedJVMEdge(t, store)
	if !confirmed {
		t.Fatalf("expected a confirmed java→kotlin calls edge, got %+v", e)
	}
	if e.Confidence() != 1.0 {
		t.Fatalf("confidence = %v, want 1.0", e.Confidence())
	}
}

// TestJVMResolve_DefaultOffIsHeuristicOnly pins the shipped default: without
// the opt-in, the same repository produces NO confirmed JVM edge — the
// heuristic tier stays the final word, byte-identical to pre-binder behavior.
func TestJVMResolve_DefaultOffIsHeuristicOnly(t *testing.T) {
	ctx := context.Background()
	t.Setenv(semantic.EnvJVM, "")
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	root := writeRepo(t, jvmFixture())
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	if _, confirmed := confirmedJVMEdge(t, store); confirmed {
		t.Fatal("default-off must produce no confirmed JVM edge")
	}
}

// TestJVMResolve_PerLanguageKillSwitch pins that the ADR 0007 per-language
// switch reaches the JVM registrants: opted in but with java disabled, the
// java-from edge must not confirm.
func TestJVMResolve_PerLanguageKillSwitch(t *testing.T) {
	ctx := context.Background()
	t.Setenv(semantic.EnvJVM, "1")
	t.Setenv("GRAPHI_NO_TYPERESOLVE_JAVA", "1")
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	root := writeRepo(t, jvmFixture())
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	if _, confirmed := confirmedJVMEdge(t, store); confirmed {
		t.Fatal("a kill-switched java registrant must not confirm edges")
	}
}

// TestJVMResolve_FullVsIncrementalParity pins the parity-by-construction
// claim on the live path: an incremental change to the Java caller converges
// to the same graph bytes as a from-scratch full pass.
func TestJVMResolve_FullVsIncrementalParity(t *testing.T) {
	ctx := context.Background()
	t.Setenv(semantic.EnvJVM, "1")

	incStore := graphstore.NewMemStore()
	t.Cleanup(func() { _ = incStore.Close() })
	inc := newIngester(t, incStore, parse.NewDefaultRegistry())
	root := writeRepo(t, jvmFixture())
	if err := inc.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	rewrite(t, root, "com/shop/Shop.java", `package com.shop;
import com.tax.Rate;
public class Shop {
    public void run(Rate param) {
        param.value();
        Rate extra = new Rate(1);
        extra.value();
    }
}
`)
	if err := inc.IngestChanged(ctx, root, []string{"com/shop/Shop.java"}); err != nil {
		t.Fatalf("IngestChanged: %v", err)
	}

	fullStore := graphstore.NewMemStore()
	t.Cleanup(func() { _ = fullStore.Close() })
	full := newIngester(t, fullStore, parse.NewDefaultRegistry())
	if err := full.IngestAll(ctx, root); err != nil {
		t.Fatalf("full IngestAll: %v", err)
	}

	if inc, full := dumpGraph(t, incStore), dumpGraph(t, fullStore); inc != full {
		t.Fatalf("full-vs-incremental divergence with the JVM binder live:\nINC:\n%s\nFULL:\n%s", inc, full)
	}
}
