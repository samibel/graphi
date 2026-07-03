package surfaces_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/surfaces/cli"
	"github.com/samibel/graphi/surfaces/client"
)

// TestEP015_DiagnoseCLISurfaceEqualsCanonicalBytes proves the SW-094 parity
// guarantee for the diagnostics op: the CLI surface emits EXACTLY the canonical
// bytes the shared client returns (diagnostic.Marshal), with no per-surface
// formatting. Because every surface consumes the same client + single marshaller,
// byte-identical output across surfaces is by construction.
func TestEP015_DiagnoseCLISurfaceEqualsCanonicalBytes(t *testing.T) {
	store, _ := seed(t)
	c := client.NewDirect(query.New(store), nil)
	ctx := context.Background()

	want, err := c.Diagnose(ctx, nil, client.DiagnoseOptions{})
	if err != nil {
		t.Fatalf("client.Diagnose: %v", err)
	}

	var out, errb bytes.Buffer
	if err := cli.RunDiagnose(ctx, c, nil, &out, &errb); err != nil {
		t.Fatalf("RunDiagnose: %v (stderr=%s)", err, errb.String())
	}
	got := bytes.TrimRight(out.Bytes(), "\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("CLI surface bytes != canonical marshaller bytes:\n cli=%s\n want=%s", got, want)
	}
	// The seed has a heuristic edge (unresolved reference) and unreferenced nodes
	// (dead symbols), so the outcome must be "reported" — a non-trivial result.
	if !bytes.Contains(want, []byte(`"outcome":"reported"`)) {
		t.Fatalf("expected reported outcome from seeded graph, got: %s", want)
	}
}

// TestEP015_DiagnoseDeterministic is the determinism half of the SW-094 harness:
// repeated independent runs over the same graph are byte-identical (stands in for
// full-vs-incremental byte parity, which holds because the output is a pure
// function of graph content via the canonical comparator).
func TestEP015_DiagnoseDeterministic(t *testing.T) {
	store, _ := seed(t)
	c := client.NewDirect(query.New(store), nil)
	ctx := context.Background()
	a, err := c.Diagnose(ctx, nil, client.DiagnoseOptions{})
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	for i := 0; i < 5; i++ {
		b, _ := c.Diagnose(ctx, nil, client.DiagnoseOptions{})
		if !bytes.Equal(a, b) {
			t.Fatalf("diagnose run %d not byte-identical:\n a=%s\n b=%s", i, a, b)
		}
	}
}

// TestEP015_HTTPEditOpsUnavailableUntilWired documents the incremental rollout:
// the daemon/HTTP client adapter returns a typed unavailable for the edit ops
// until a daemon edit RPC is added (the same precedent analysis/edit/review
// followed). The in-process Direct client is the live path.
func TestEP015_HTTPEditOpsUnavailableUntilWired(t *testing.T) {
	h, err := client.NewHTTP("http://127.0.0.1:0")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	ctx := context.Background()
	if _, err := h.Inline(ctx, client.InlineRequest{TargetSymbol: "x"}); err != client.ErrEditUnavailable {
		t.Fatalf("HTTP Inline err = %v, want ErrEditUnavailable", err)
	}
	if _, err := h.SafeDelete(ctx, client.SafeDeleteRequest{TargetSymbol: "x"}); err != client.ErrEditUnavailable {
		t.Fatalf("HTTP SafeDelete err = %v, want ErrEditUnavailable", err)
	}
	if _, err := h.Diagnose(ctx, nil, client.DiagnoseOptions{}); err != client.ErrDiagnosticUnavailable {
		t.Fatalf("HTTP Diagnose err = %v, want ErrDiagnosticUnavailable", err)
	}
}
