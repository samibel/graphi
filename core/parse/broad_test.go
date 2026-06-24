//go:build graphi_broad

package parse_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samibel/graphi/core/parse"
)

// These tests run ONLY under `-tags graphi_broad` (CGO_ENABLED=1). They are the
// broad-lane half of SW-056: they exercise the opt-in go-sitter-forest (CGO) bundle
// over the SAME SymbolExtractor contract. They never run in the default lane, so
// they cannot affect the CGo-free default-build gate.

// zigSmokeSource is a small, self-contained Zig program. Zig is a CGO-only grammar
// absent from the pure-Go default set (DN-1's recommended smoke grammar).
const zigSmokeSource = `const std = @import("std");

fn add(a: i32, b: i32) i32 {
    return a + b;
}

pub fn main() void {
    const x = add(1, 2);
    _ = x;
}
`

// TestRegisterBroad_WiresForestOverContract proves the opt-in seam registers the
// CGO forest backend over the existing Parser contract, and that the parser
// declares RuntimeCGOForest (NOT a pure-Go runtime). This is the Slice 2 core: the
// forest backend reaches the registry ONLY via RegisterBroad.
func TestRegisterBroad_WiresForestOverContract(t *testing.T) {
	// The DEFAULT registry must NOT carry the broad (zig) grammar.
	def := parse.RegisterDefaults(parse.NewRegistry())
	if _, err := def.ParserForLang("zig"); !errors.Is(err, parse.ErrNoParser) {
		t.Fatalf("zig must NOT be registered by RegisterDefaults; got err=%v", err)
	}

	// The BROAD registry, built via the opt-in seam, carries it.
	broad := parse.RegisterBroad(parse.NewRegistry())
	p, err := broad.ParserForLang("zig")
	if err != nil {
		t.Fatalf("RegisterBroad must register zig over the contract: %v", err)
	}
	if got := p.Runtime(); got != parse.RuntimeCGOForest {
		t.Fatalf("zig parser Runtime() = %q, want %q (CGO forest)", got, parse.RuntimeCGOForest)
	}
	if got := p.Language(); got != "zig" {
		t.Fatalf("zig parser Language() = %q, want \"zig\"", got)
	}
}

// TestBroadSmokeParse_Zig_FrozenVocabulary smoke-parses one CGO-only grammar (zig)
// over the contract and asserts a FROZEN expected vocabulary (specific node kinds +
// the declared symbols), NOT a bare len>0 (DN-1/DN-4). It also re-asserts
// Runtime()==RuntimeCGOForest on the parse path.
func TestBroadSmokeParse_Zig_FrozenVocabulary(t *testing.T) {
	reg := parse.RegisterBroad(parse.NewRegistry())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	res, err := reg.Parse(ctx, "pkg/sample.zig", []byte(zigSmokeSource))
	if err != nil {
		t.Fatalf("broad smoke parse zig: %v", err)
	}
	if res == nil {
		t.Fatal("broad smoke parse returned nil result")
	}
	if res.Meta.Language != "zig" {
		t.Fatalf("Meta.Language = %q, want \"zig\"", res.Meta.Language)
	}

	// FROZEN vocabulary: this exact, deterministic node set (NOT a bare len>0). Zig
	// models top-level `const` as a VarDecl, so the smoke source yields the file
	// node, the two functions add/main, and the three top-level bindings std/x/_,
	// all mapped onto the canonical Kind* vocabulary with the fixture prefix "pkg".
	// Every produced kind must be drawn from {file, function, variable}.
	gotByQN := map[string]string{}
	for _, n := range res.Nodes {
		switch n.Kind() {
		case parse.KindFile, parse.KindFunction, parse.KindVariable:
			gotByQN[n.QualifiedName()] = n.Kind()
		default:
			t.Errorf("unexpected node kind %q (qn=%q) — vocabulary is frozen to {file,function,variable}", n.Kind(), n.QualifiedName())
		}
	}
	want := map[string]string{
		"pkg/sample.zig": parse.KindFile,
		"pkg.add":        parse.KindFunction,
		"pkg.main":       parse.KindFunction,
		"pkg.std":        parse.KindVariable,
		"pkg.x":          parse.KindVariable,
		"pkg._":          parse.KindVariable,
	}
	for qn, wantKind := range want {
		if got, ok := gotByQN[qn]; !ok {
			t.Errorf("frozen vocabulary missing %q (want kind %q); got %v", qn, wantKind, gotByQN)
		} else if got != wantKind {
			t.Errorf("frozen vocabulary: %q has kind %q, want %q", qn, got, wantKind)
		}
	}
	if len(gotByQN) != len(want) {
		t.Errorf("frozen vocabulary size = %d, want %d (%v)", len(gotByQN), len(want), gotByQN)
	}
}

// TestBroadParse_Deterministic asserts the broad path is deterministic: two parses
// of identical input yield identical node/edge counts and qualified names. This is
// the contract every SymbolExtractor honors (mirrors the default-tier determinism
// tests), now exercised over the CGO runtime.
func TestBroadParse_Deterministic(t *testing.T) {
	reg := parse.RegisterBroad(parse.NewRegistry())
	ctx := context.Background()
	a, err := reg.Parse(ctx, "pkg/sample.zig", []byte(zigSmokeSource))
	if err != nil {
		t.Fatalf("parse A: %v", err)
	}
	b, err := reg.Parse(ctx, "pkg/sample.zig", []byte(zigSmokeSource))
	if err != nil {
		t.Fatalf("parse B: %v", err)
	}
	if len(a.Nodes) != len(b.Nodes) || len(a.Edges) != len(b.Edges) {
		t.Fatalf("non-deterministic: A(%d nodes,%d edges) != B(%d nodes,%d edges)",
			len(a.Nodes), len(a.Edges), len(b.Nodes), len(b.Edges))
	}
	for i := range a.Nodes {
		if a.Nodes[i].QualifiedName() != b.Nodes[i].QualifiedName() || a.Nodes[i].Kind() != b.Nodes[i].Kind() {
			t.Fatalf("non-deterministic node order at %d: %q/%q vs %q/%q", i,
				a.Nodes[i].Kind(), a.Nodes[i].QualifiedName(), b.Nodes[i].Kind(), b.Nodes[i].QualifiedName())
		}
	}
}

// TestBroadParse_AdversarialDepth feeds a crafted DEEPLY-NESTED input to turn the
// Go-side depth bound from theory into a CI signal (DN-4/SEC-H). The Go walk's
// fail-closed depth guard (shared maxParseDepth) must reject it with
// ErrMaxDepthExceeded and a SOURCE-FREE SanitizedError — never echo raw source.
//
// NOTE (DN-5 / SW-056-SEC-001): this bounds the GO walk only. A native-C stack
// overflow inside the tree-sitter parser is NOT contained by this guard; that
// residual native-C crash risk is the HUMAN-ACCEPTED, opt-in residual deferred to
// SW-058. This test documents the Go-side limit; it does not contain the C parser.
func TestBroadParse_AdversarialDepth(t *testing.T) {
	prev := parse.SetMaxParseDepth(64) // tighten so a modestly-nested input trips it
	t.Cleanup(func() { parse.SetMaxParseDepth(prev) })

	// Deeply nested parenthesized expression: ((((( ... 1 ... ))))).
	const depth = 4000
	var sb strings.Builder
	sb.WriteString("const x = ")
	sb.WriteString(strings.Repeat("(", depth))
	sb.WriteString("1")
	sb.WriteString(strings.Repeat(")", depth))
	sb.WriteString(";\n")

	reg := parse.RegisterBroad(parse.NewRegistry())
	_, err := reg.Parse(context.Background(), "pkg/deep.zig", []byte(sb.String()))
	if err == nil {
		t.Fatal("expected the deeply-nested input to be rejected by the Go-side depth bound")
	}
	if !errors.Is(err, parse.ErrMaxDepthExceeded) {
		t.Fatalf("expected ErrMaxDepthExceeded, got %v", err)
	}
	// Source-free: the error must not echo the raw nested-paren payload.
	if strings.Contains(err.Error(), "(((") {
		t.Fatalf("SanitizedError leaked raw source into the error string: %q", err.Error())
	}
}

// TestBroadParse_OversizedTimeoutHonored asserts the broad parse honors ctx
// cancellation (the only resource bound that cleanly transfers across the Go/CGO
// boundary alongside MaxFileSize, DN-5): a cancelled ctx yields a non-nil error
// rather than a successful parse.
func TestBroadParse_OversizedTimeoutHonored(t *testing.T) {
	reg := parse.RegisterBroad(parse.NewRegistry())
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	if _, err := reg.Parse(ctx, "pkg/sample.zig", []byte(zigSmokeSource)); err == nil {
		t.Fatal("expected a cancelled ctx to abort the broad parse")
	}
}

// --- Egress: the broad (forest/CGO) path under an injected failing dialer ---

type broadFailingDialer struct{ dialed atomic.Bool }

func (d *broadFailingDialer) DialContext(_ context.Context, network, address string) (net.Conn, error) {
	d.dialed.Store(true)
	return nil, &net.OpError{Op: "dial", Net: network, Err: errBroadDialBlocked{address}}
}

type errBroadDialBlocked struct{ addr string }

func (e errBroadDialBlocked) Error() string { return "broad-lane egress blocked: " + e.addr }

// TestBroadForestPath_ZeroEgress exercises the FOREST (CGO) parse path under an
// injected failing resolver/dialer and asserts no Go-level outbound dial occurred
// (SEC-E, the Go-observable half). It complements — and does NOT replace — the live
// CGO=1 netns deny-egress job, which is the ONLY mechanism that covers C-level
// socket()/connect() (a Go-level dialer canary is structurally blind to it). The
// existing default-tier egress_test runs the CGO=0 default registry and does not
// transfer to the forest path, so this is the broad-specific runtime egress assert.
func TestBroadForestPath_ZeroEgress(t *testing.T) {
	dialer := &broadFailingDialer{}
	origResolver := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: dialer.DialContext}
	t.Cleanup(func() { net.DefaultResolver = origResolver })

	reg := parse.RegisterBroad(parse.NewRegistry())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _ = reg.Parse(ctx, "pkg/sample.zig", []byte(zigSmokeSource))

	if dialer.dialed.Load() {
		t.Fatal("the graphi-broad forest path attempted an outbound dial — zero-egress violated (Go-observable)")
	}
}
