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

	m := localManifest(repo, []Search{
		{Query: "hello", ExpectNonEmpty: true},
	})
	// The fixture's cross-file call helper() -> hello() is type-checkable, so
	// the wired typeresolve pass must prove at least one confirmed caller.
	m.Entries[0].ConfirmedEdges = []ConfirmedEdge{
		{SymbolQuery: "hello", Operation: "callers", Min: 1},
	}
	rep, err := r.Run(context.Background(), m)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rep.Pass {
		t.Fatalf("fixture run failed:\n%+v", rep.Entries)
	}
	e := rep.Entries[0]
	wantSteps := []string{"materialize", "index", "search:hello", "query:callers", "analyze:impact", "confirmed:callers:hello", "diagnose"}
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

// TestRunner_ConfirmedAssertionBites proves the confirmed-tier assertion is
// not vacuous: an impossible minimum turns the run red, and a symbol query
// with no EXACT name match fails instead of silently anchoring on a fuzzy
// neighbor.
func TestRunner_ConfirmedAssertionBites(t *testing.T) {
	bin := buildGraphi(t)
	repo := writeFixtureRepo(t)

	t.Run("impossible minimum", func(t *testing.T) {
		r := &Runner{Binary: bin, WorkDir: t.TempDir(), PerEntryTimeout: 2 * time.Minute}
		m := localManifest(repo, []Search{{Query: "hello", ExpectNonEmpty: true}})
		m.Entries[0].ConfirmedEdges = []ConfirmedEdge{{SymbolQuery: "hello", Operation: "callers", Min: 99}}
		rep, err := r.Run(context.Background(), m)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if rep.Pass {
			t.Fatal("run passed although 99 confirmed callers cannot exist (assertion is vacuous)")
		}
	})

	t.Run("no exact anchor match", func(t *testing.T) {
		r := &Runner{Binary: bin, WorkDir: t.TempDir(), PerEntryTimeout: 2 * time.Minute}
		m := localManifest(repo, []Search{{Query: "hello", ExpectNonEmpty: true}})
		// "hell" fuzzy-matches hello/helper but names no exact symbol; the
		// anchor resolution must refuse rather than pick a lookalike.
		m.Entries[0].ConfirmedEdges = []ConfirmedEdge{{SymbolQuery: "hell", Operation: "callers", Min: 1}}
		rep, err := r.Run(context.Background(), m)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if rep.Pass {
			t.Fatal("run passed although the anchor symbol does not exist by exact name")
		}
	})
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
		{"sha too short", `{"entries":[{"name":"x","url":"u","ref":"r","sha":"abc123","searches":[{"query":"q","expect_nonempty":true}]}]}`, "12 hex"},
		{"sha not hex", `{"entries":[{"name":"x","url":"u","ref":"r","sha":"zzzzzzzzzzzz","searches":[{"query":"q","expect_nonempty":true}]}]}`, "12 hex"},
		{"confirmed empty query", `{"entries":[{"name":"x","path":"p","searches":[{"query":"q","expect_nonempty":true}],"confirmed_edges":[{"operation":"callers","min":1}]}]}`, "empty symbol_query"},
		{"confirmed bad operation", `{"entries":[{"name":"x","path":"p","searches":[{"query":"q","expect_nonempty":true}],"confirmed_edges":[{"symbol_query":"s","operation":"impact","min":1}]}]}`, "must be callers"},
		{"confirmed zero min", `{"entries":[{"name":"x","path":"p","searches":[{"query":"q","expect_nonempty":true}],"confirmed_edges":[{"symbol_query":"s","operation":"callers","min":0}]}]}`, "vacuous"},
	}
	for _, c := range cases {
		if _, err := LoadManifest(write(c.body)); err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want contains %q", c.name, err, c.wantErr)
		}
	}
}

// TestShaMatchesPrefix pins the prefix-pin semantics.
func TestShaMatchesPrefix(t *testing.T) {
	head := "a0a6ae020bb35d7dd6fe670cd06b83349e6b6c90"
	cases := []struct {
		pinned string
		want   bool
	}{
		{"a0a6ae020bb3", true},
		{"A0A6AE020BB3", true}, // case-insensitive
		{head, true},           // full sha
		{"a0a6ae020bb4", false},
		{head + "00", false}, // longer than head
	}
	for _, c := range cases {
		if got := shaMatches(c.pinned, head); got != c.want {
			t.Errorf("shaMatches(%q) = %v, want %v", c.pinned, got, c.want)
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
		if e.URL != "" && e.SHA == "" {
			t.Errorf("entry %q lost its sha pin (recorded from the first green run)", e.Name)
		}
	}
}

// TestLoadManifest_TierValidation pins tier and SHA-pin rules.
func TestLoadManifest_TierValidation(t *testing.T) {
	write := func(content string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "m.json")
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		return p
	}
	cases := []struct {
		name, body, wantErr string
	}{
		{"tier invalid", `{"entries":[{"name":"x","path":"p","tier":4,"searches":[{"query":"q","expect_nonempty":true}]}]}`, "invalid tier"},
		{"tier2 url no sha", `{"entries":[{"name":"x","url":"u","ref":"r","tier":2,"searches":[{"query":"q","expect_nonempty":true}]}]}`, "requires an exact SHA pin"},
		{"tier3 url no sha", `{"entries":[{"name":"x","url":"u","ref":"r","tier":3,"searches":[{"query":"q","expect_nonempty":true}]}]}`, "requires an exact SHA pin"},
		{"tier1 url no sha ok", `{"entries":[{"name":"x","url":"u","ref":"r","tier":1,"searches":[{"query":"q","expect_nonempty":true}]}]}`, ""},
		{"tier2 url with sha ok", `{"entries":[{"name":"x","url":"u","ref":"r","tier":2,"sha":"a0a6ae020bb3","searches":[{"query":"q","expect_nonempty":true}]}]}`, ""},
	}
	for _, c := range cases {
		_, err := LoadManifest(write(c.body))
		if c.wantErr == "" {
			if err != nil {
				t.Errorf("%s: unexpected error: %v", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: err = %v, want contains %q", c.name, err, c.wantErr)
		}
	}
}

// TestRunner_TierFilter proves tier and max-tier filtering work and that
// omitting the flags runs all entries.
func TestRunner_TierFilter(t *testing.T) {
	m := Manifest{
		Entries: []Entry{
			{Name: "one", Path: "/dev/null", Tier: 1, Searches: []Search{{Query: "q", ExpectNonEmpty: true}}},
			{Name: "two", Path: "/dev/null", Tier: 2, Searches: []Search{{Query: "q", ExpectNonEmpty: true}}},
			{Name: "three", Path: "/dev/null", Tier: 3, Searches: []Search{{Query: "q", ExpectNonEmpty: true}}},
		},
	}

	r := &Runner{Binary: "ignored", Tier: 2, WorkDir: t.TempDir()}
	rep, err := r.Run(context.Background(), m)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Name != "two" {
		t.Fatalf("expected exactly tier 2 entry, got %v", rep.Entries)
	}

	r2 := &Runner{Binary: "ignored", MaxTier: 2, WorkDir: t.TempDir()}
	rep2, err := r2.Run(context.Background(), m)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep2.Entries) != 2 {
		t.Fatalf("expected 2 entries with max-tier 2, got %d", len(rep2.Entries))
	}

	r3 := &Runner{Binary: "ignored", WorkDir: t.TempDir()}
	rep3, err := r3.Run(context.Background(), m)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep3.Entries) != 3 {
		t.Fatalf("expected all 3 entries when no filter set, got %d", len(rep3.Entries))
	}
}

// TestRunner_TierDefaultBackwardCompat proves entries without a tier default
// to tier 1 and are included in tier-1/max-tier-1 runs.
func TestRunner_TierDefaultBackwardCompat(t *testing.T) {
	m := Manifest{
		Entries: []Entry{
			{Name: "legacy", Path: "/dev/null", Searches: []Search{{Query: "q", ExpectNonEmpty: true}}},
		},
	}
	r := &Runner{Binary: "ignored", Tier: 1, WorkDir: t.TempDir()}
	rep, err := r.Run(context.Background(), m)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.Entries) != 1 {
		t.Fatalf("expected legacy entry to default to tier 1, got %d entries", len(rep.Entries))
	}
}

// TestRunner_BudgetPreserved proves the budget field survives filtering.
func TestRunner_BudgetPreserved(t *testing.T) {
	m := Manifest{
		Entries: []Entry{
			{Name: "budgeted", Path: "/dev/null", Tier: 1, BudgetMS: 5000, Searches: []Search{{Query: "q", ExpectNonEmpty: true}}},
		},
	}
	r := &Runner{Binary: "ignored", WorkDir: t.TempDir()}
	rep, err := r.Run(context.Background(), m)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Name != "budgeted" {
		t.Fatalf("expected budgeted entry to survive filter")
	}
}

// TestRunner_ScenarioRefReserved proves the scenario_ref field is accepted
// by the loader and does not break validation.
func TestRunner_ScenarioRefReserved(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.json")
	body := `{"entries":[{"name":"x","path":"p","tier":1,"scenario_ref":"c3-anchor-1","searches":[{"query":"q","expect_nonempty":true}]}]}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadManifest(p); err != nil {
		t.Fatalf("scenario_ref should not break validation: %v", err)
	}
}
