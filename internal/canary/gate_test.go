package canary

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFixture writes a Go file under a temp module root and returns the root.
// Used to prove the AST outbound-dial scan behaves correctly on controlled
// source (Review F1/F2 regression tests).
func writeFixture(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	srcDir := filepath.Join(root, "surfaces", "evil")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Make the fixture package path look non-allowlisted so the scan inspects it.
	if err := os.WriteFile(filepath.Join(srcDir, "evil.go"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return root
}

const httpClientDoFixture = `package evil

import "net/http"

func Leak() {
	var c http.Client
	req, _ := http.NewRequest("GET", "http://telemetry.example.com", nil)
	c.Do(req)
}
`

const defaultClientFixture = `package evil

import "net/http"

func Leak() {
	http.DefaultClient.Get("http://telemetry.example.com")
}
`

// Review F1 regression: the gate MUST flag (*http.Client).Do — the primary HTTP
// egress mechanism — which the v1 gate missed.
func TestGate_FlagsHTTPClientDo(t *testing.T) {
	root := writeFixture(t, httpClientDoFixture)
	cfg := GateConfig{
		ModuleDir:    root,
		GraphCommand: func(string) ([]string, error) { return []string{"net/http"}, nil },
	}
	res, err := RunGate(cfg)
	if err != nil {
		t.Fatalf("RunGate: %v", err)
	}
	if res.Verdict != "fail" {
		t.Fatalf("verdict = %q, want fail (http.Client.Do is egress and must be flagged)", res.Verdict)
	}
	var found bool
	for _, f := range res.Findings {
		if f.Kind == "outbound-dial" && f.Symbol == "http.Client.Do" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an http.Client.Do outbound-dial finding; got %+v", res.Findings)
	}
}

// Review F1 regression: the gate MUST flag http.DefaultClient.Get.
func TestGate_FlagsDefaultClient(t *testing.T) {
	root := writeFixture(t, defaultClientFixture)
	cfg := GateConfig{
		ModuleDir:    root,
		GraphCommand: func(string) ([]string, error) { return []string{"net/http"}, nil },
	}
	res, err := RunGate(cfg)
	if err != nil {
		t.Fatalf("RunGate: %v", err)
	}
	if res.Verdict != "fail" {
		t.Fatalf("verdict = %q, want fail (http.DefaultClient.Get is egress)", res.Verdict)
	}
}

// Review F2 regression: http.NewRequest alone is NOT egress and must NOT be
// flagged on its own (v1 over-flagged it). A file that only constructs a
// request without sending it must pass.
const newRequestOnlyFixture = `package evil

import "net/http"

func Build() {
	http.NewRequest("GET", "http://example.com", nil) // no client.Do — no egress
}
`

func TestGate_DoesNotFlagBareNewRequest(t *testing.T) {
	root := writeFixture(t, newRequestOnlyFixture)
	cfg := GateConfig{
		ModuleDir:    root,
		GraphCommand: func(string) ([]string, error) { return []string{"net/http"}, nil },
	}
	res, err := RunGate(cfg)
	if err != nil {
		t.Fatalf("RunGate: %v", err)
	}
	for _, f := range res.Findings {
		if f.Kind == "outbound-dial" {
			t.Fatalf("bare http.NewRequest must not be flagged as egress; got %+v", f)
		}
	}
}
