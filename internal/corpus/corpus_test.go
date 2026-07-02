package corpus

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// buildGraphi builds the real CLI binary once per test binary invocation.
// Hermetic: it shells to the local Go toolchain exactly like internal/coverage
// and internal/layerguard do, and touches no network.
func buildGraphi(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "graphi")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "github.com/samibel/graphi/cmd/graphi")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build graphi: %v\n%s", err, out)
	}
	return bin
}

// writeFixtureRepo materializes a tiny multi-file repo including the historical
// crash classes: a non-source asset and a malformed JSON file.
func writeFixtureRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"main.go":     "package main\n\nfunc hello() {}\n\nfunc main() { hello() }\n",
		"util.go":     "package main\n\nfunc helper() { hello() }\n",
		"notes.md":    "# fixture\n",
		"data.json":   "{\"ok\": true}\n",
		"broken.json": "{{ handlebars template — not strict JSON }}\n",
		".DS_Store":   "\x00\x01binary junk",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

func localManifest(path string, searches []Search) Manifest {
	return Manifest{Entries: []Entry{{
		Name:     "fixture",
		Path:     path,
		Searches: searches,
	}}}
}

// TestRunner_LocalFixtureFullFlow drives the REAL binary through the full
// index → search → query → analyze → diagnose flow against a local fixture
// containing the historical crash classes, and requires a clean pass.
func TestRunner_LocalFixtureFullFlow(t *testing.T) {
	bin := buildGraphi(t)
	repo := writeFixtureRepo(t)
	r := &Runner{Binary: bin, WorkDir: t.TempDir(), PerEntryTimeout: 2 * time.Minute}

	rep, err := r.Run(context.Background(), localManifest(repo, []Search{
		{Query: "hello", ExpectNonEmpty: true},
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rep.Pass {
		t.Fatalf("fixture run failed:\n%+v", rep.Entries)
	}
	e := rep.Entries[0]
	wantSteps := []string{"materialize", "index", "search:hello", "query:callers", "analyze:impact", "diagnose"}
	var got []string
	for _, s := range e.Steps {
		got = append(got, s.Name)
	}
	for _, w := range wantSteps {
		found := false
		for _, g := range got {
			found = found || g == w
		}
		if !found {
			t.Errorf("step %q missing from run (got %v)", w, got)
		}
	}
}

// TestRunner_EmptyExpectationFails proves the harness BITES: a search promised
// non-empty that yields nothing must fail the entry (anti-vacuity — a corpus
// run that indexes zero symbols must never read as green).
func TestRunner_EmptyExpectationFails(t *testing.T) {
	bin := buildGraphi(t)
	repo := writeFixtureRepo(t)
	r := &Runner{Binary: bin, WorkDir: t.TempDir(), PerEntryTimeout: 2 * time.Minute}

	rep, err := r.Run(context.Background(), localManifest(repo, []Search{
		{Query: "zzz_no_such_symbol_zzz", ExpectNonEmpty: true},
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Pass {
		t.Fatal("run passed although the promised search result is empty (harness is vacuous)")
	}
	var failed *StepResult
	for i := range rep.Entries[0].Steps {
		if !rep.Entries[0].Steps[i].OK {
			failed = &rep.Entries[0].Steps[i]
		}
	}
	if failed == nil || !strings.HasPrefix(failed.Name, "search:") {
		t.Fatalf("expected the search step to be the failing one, got %+v", rep.Entries[0].Steps)
	}
}

// TestRunner_BrokenBinaryFails proves a crashing binary turns the run red
// (the harness's core promise: first-contact crashes become CI failures).
func TestRunner_BrokenBinaryFails(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "graphi")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	// A stand-in "binary" that prints a panic marker and exits non-zero on any
	// invocation — compiled Go so the bite-proof runs on every platform.
	src := filepath.Join(dir, "crash.go")
	code := "package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\nfunc main() {\n\tfmt.Fprintln(os.Stderr, \"panic: runtime error: fixture crash\")\n\tos.Exit(2)\n}\n"
	if err := os.WriteFile(src, []byte(code), 0o600); err != nil {
		t.Fatalf("write stub source: %v", err)
	}
	build := exec.Command("go", "build", "-o", bin, src)
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build stub: %v\n%s", err, out)
	}
	repo := writeFixtureRepo(t)
	r := &Runner{Binary: bin, WorkDir: t.TempDir(), PerEntryTimeout: time.Minute}

	rep, err := r.Run(context.Background(), localManifest(repo, []Search{
		{Query: "hello", ExpectNonEmpty: true},
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Pass {
		t.Fatal("run passed although the binary crashes on index")
	}
}

// TestLoadManifest_Validation pins the fail-closed manifest rules.
func TestLoadManifest_Validation(t *testing.T) {
	write := func(content string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "m.json")
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		return p
	}
	cases := []struct {
		name, body string
		wantErr    string
	}{
		{"no entries", `{"entries":[]}`, "no entries"},
		{"url and path", `{"entries":[{"name":"x","url":"u","ref":"r","path":"p","searches":[{"query":"q","expect_nonempty":true}]}]}`, "exactly one"},
		{"neither url nor path", `{"entries":[{"name":"x","searches":[{"query":"q","expect_nonempty":true}]}]}`, "exactly one"},
		{"url without ref", `{"entries":[{"name":"x","url":"u","searches":[{"query":"q","expect_nonempty":true}]}]}`, "no ref"},
		{"no nonempty search", `{"entries":[{"name":"x","path":"p","searches":[{"query":"q"}]}]}`, "expect_nonempty"},
	}
	for _, c := range cases {
		if _, err := LoadManifest(write(c.body)); err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want contains %q", c.name, err, c.wantErr)
		}
	}
}

// TestCheckedInManifestParses keeps the committed manifest loadable and its
// invariants intact (every repo has a release-tag ref and a non-empty promise).
func TestCheckedInManifestParses(t *testing.T) {
	root, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Skipf("go env GOMOD unavailable: %v", err)
	}
	dir := filepath.Dir(strings.TrimSpace(string(root)))
	m, err := LoadManifest(filepath.Join(dir, "corpus", "manifest.json"))
	if err != nil {
		t.Fatalf("checked-in manifest invalid: %v", err)
	}
	if len(m.Entries) < 5 {
		t.Errorf("corpus shrank to %d entries — the manifest should keep covering the known bug classes", len(m.Entries))
	}
	for _, e := range m.Entries {
		if e.URL != "" && e.Ref == "" {
			t.Errorf("entry %q lost its ref pin", e.Name)
		}
	}
}
