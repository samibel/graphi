package parse_test

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samibel/graphi/core/parse"
)

// fixtures exercises every default-tier parser on a representative source snippet,
// so the zero-egress assertion covers the NEW default-tier parsers (SW-055 AC#5),
// not just one path.
var egressFixtures = map[string]string{
	"a.go":   "package a\nfunc F() int { return G() }\nfunc G() int { return 1 }\n",
	"a.json": `{"k":[1,2,{"n":true}]}`,
	"a.py":   "import os\ndef f():\n    return os.getcwd()\n",
	"a.js":   "function f(){ return g(); }\nfunction g(){ return 1; }\n",
	"a.ts":   "function f(): number { return 1; }\nclass C { m() { return f(); } }\n",
	"a.tsx":  "const X = () => <div/>;\n",
	"a.java": "class A { int f(){ return 1; } }\n",
	"a.c":    "int f(void){ return 1; }\n",
	"a.rb":   "def f\n  1\nend\n",
	"a.rs":   "fn f() -> i32 { 1 }\n",
	"a.php":  "<?php function f(){ return 1; }\n",
	"a.cs":   "class A { int F(){ return 1; } }\n",
	"a.kt":   "fun f(): Int { return 1 }\n",
	"a.cpp":  "int f(){ return 1; }\n",
	"a.sh":   "f() { echo hi; }\n",
	"a.sql":  "SELECT 1;\n",
	"a.lua":  "function f() return 1 end\n",
	"a.css":  "a { color: red; }\n",
	"a.yaml": "a: 1\nb: [1,2]\n",
	"a.toml": "a = 1\n",
	"a.md":   "# Title\n\ntext\n",
	"a.tf":   "resource \"x\" \"y\" {}\n",
}

// failingDialer records any dial ATTEMPT and always fails — installed as the
// process-level net dialer for the duration of the test. If parsing any default
// language triggers a dial, dialed flips and the test fails. No live sockets are
// opened (the dialer fails before connecting), satisfying the "injected failing
// DialContext, no live sockets" design (SW-055 AC#5).
type failingDialer struct{ dialed atomic.Bool }

func (d *failingDialer) DialContext(_ context.Context, network, address string) (net.Conn, error) {
	d.dialed.Store(true)
	return nil, &net.OpError{Op: "dial", Net: network, Err: errDialBlocked{address}}
}

type errDialBlocked struct{ addr string }

func (e errDialBlocked) Error() string { return "egress blocked by canary dialer: " + e.addr }

// TestDefaultTierParsers_ZeroEgress exercises every default-tier parser and asserts
// no parser attempted any outbound dial. The assertion is on dial ATTEMPT via an
// injected failing dialer (no live sockets) — the runtime half of the zero-egress
// guard for the new default-tier parsers.
func TestDefaultTierParsers_ZeroEgress(t *testing.T) {
	dialer := &failingDialer{}

	// Install the sentinel dialer process-wide for the duration of the test. The
	// zero-value net.Dialer used by net.Dial does not consult this, so we also use
	// a custom resolver to catch any DNS dial. Pure-CPU parsers must touch neither.
	origResolver := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial:     dialer.DialContext,
	}
	t.Cleanup(func() { net.DefaultResolver = origResolver })

	reg := parse.RegisterDefaults(parse.NewRegistry())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for name, src := range egressFixtures {
		// A parse may legitimately return an error for a thin fixture; egress is the
		// only property under test here, so the error is intentionally ignored.
		_, _ = reg.Parse(ctx, name, []byte(src))
	}

	if dialer.dialed.Load() {
		t.Fatal("a default-tier parser attempted an outbound dial — zero-egress violated")
	}
}
