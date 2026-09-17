package canary

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	if res.EvidenceDigest == "" {
		t.Fatal("static gate returned a verdict without an evidence digest")
	}
}

// The evaluation-only CodeRank adapter is an explicitly constructed,
// loopback-by-construction transport, just like the opt-in Ollama adapter. The
// repository-wide raw-source scan deliberately sees packages outside the
// default binary graph, so this narrow exception must name only that package;
// a sibling package must remain subject to the outbound-dial gate.
func TestOutboundDialAllowlist_CodeRankIsNarrow(t *testing.T) {
	const coderank = "github.com/samibel/graphi/engine/embed/coderank"
	if !isAllowlistedPkg(coderank) {
		t.Fatalf("%s is an audited loopback-only adapter and must be allowlisted", coderank)
	}
	if isAllowlistedPkg(coderank + "/remote") {
		t.Fatal("CodeRank exact-package allowlist entry leaked to a descendant package")
	}
	if isAllowlistedPkg("github.com/samibel/graphi/engine/embed/coderank_remote") {
		t.Fatal("CodeRank allowlist entry leaked to a sibling package")
	}
}

func TestOutboundDialScan_CodeRankDescendantIsStillScanned(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "engine", "embed", "coderank", "remote")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "remote.go"), []byte(httpClientDoFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	findings, err := scanOutboundDials(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range findings {
		if finding.Kind == "outbound-dial" && finding.Import == "github.com/samibel/graphi/engine/embed/coderank/remote" {
			return
		}
	}
	t.Fatalf("CodeRank descendant escaped outbound-dial scan: %+v", findings)
}

func TestDefaultGraphDoesNotContainCodeRank(t *testing.T) {
	root, err := ResolveModuleDir("")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "list", "-deps", "-test=false", "./cmd/graphi")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list cmd/graphi: %v", err)
	}
	for _, dependency := range strings.Fields(string(out)) {
		if dependency == outboundDialExactCodeRank || strings.HasPrefix(dependency, outboundDialExactCodeRank+"/") {
			t.Fatalf("default cmd/graphi graph contains evaluation-only CodeRank package %q", dependency)
		}
	}
}

func TestStaticEvidenceDigestIsCheckoutPathIndependent(t *testing.T) {
	rootA := writeFixture(t, newRequestOnlyFixture)
	rootB := writeFixture(t, newRequestOnlyFixture)
	deps := []string{"net/http", "fmt"}
	a, err := staticEvidenceDigest(rootA, deps, nil, "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	b, err := staticEvidenceDigest(rootB, []string{"fmt", "net/http"}, nil, "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("evidence digest depends on checkout path or dependency order: %s != %s", a, b)
	}
	tagged, err := staticEvidenceDigest(rootA, deps, []string{"webui_embed"}, "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if tagged == a {
		t.Fatal("build tags did not change the evidence digest")
	}
	otherPlatform, err := staticEvidenceDigest(rootA, deps, nil, "darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if otherPlatform == a {
		t.Fatal("target platform did not change the evidence digest")
	}
}

func TestResolveModuleDirRejectsAnUnrelatedModule(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/not-graphi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveModuleDir(root); err == nil {
		t.Fatal("unrelated Go module was accepted as graphi source evidence")
	}
}
