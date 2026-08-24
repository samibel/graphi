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
		{"tier invalid", `{"entries":[{"name":"x","path":"p","tier":5,"searches":[{"query":"q","expect_nonempty":true}]}]}`, "invalid tier"},
		{"tier4 manual stress ok", `{"entries":[{"name":"x","path":"p","tier":4,"searches":[{"query":"q","expect_nonempty":true}]}]}`, ""},
		{"tier2 url no sha", `{"entries":[{"name":"x","url":"u","ref":"r","tier":2,"searches":[{"query":"q","expect_nonempty":true}]}]}`, "requires an exact SHA pin"},
		{"tier3 url no sha", `{"entries":[{"name":"x","url":"u","ref":"r","tier":3,"searches":[{"query":"q","expect_nonempty":true}]}]}`, "requires an exact SHA pin"},
		{"tier1 url no sha ok", `{"entries":[{"name":"x","url":"u","ref":"r","tier":1,"language":"go","license":"MIT","searches":[{"query":"q","expect_nonempty":true}]}]}`, ""},
		{"tier2 url with sha ok", `{"entries":[{"name":"x","url":"u","ref":"r","tier":2,"language":"go","license":"MIT","sha":"a0a6ae020bb3","searches":[{"query":"q","expect_nonempty":true}]}]}`, ""},
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

// TestLoadManifest_CorpusMetadataValidation pins the v3 provenance rules: a
// cloned repository must document its terms and language, a recorded file
// census must be attributable, and "stress target" must be earned by a
// measured count rather than claimed by a label.
func TestLoadManifest_CorpusMetadataValidation(t *testing.T) {
	write := func(content string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "m.json")
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		return p
	}
	const search = `"searches":[{"query":"q","expect_nonempty":true}]`
	cases := []struct {
		name, body, wantErr string
	}{
		{"url without license", `{"entries":[{"name":"x","url":"u","ref":"r","language":"go",` + search + `}]}`, "no license"},
		{"url without language", `{"entries":[{"name":"x","url":"u","ref":"r","license":"MIT",` + search + `}]}`, "no language"},
		{"path entry needs neither", `{"entries":[{"name":"x","path":"p",` + search + `}]}`, ""},
		{"census without method", `{"entries":[{"name":"x","path":"p","measured":{"go_files":1,"tracked_files":2,"measured_at":"2026-07-27"},` + search + `}]}`, "measured_at and method"},
		{"census without date", `{"entries":[{"name":"x","path":"p","measured":{"go_files":1,"tracked_files":2,"method":"git ls-files"},` + search + `}]}`, "measured_at and method"},
		{"census with no files", `{"entries":[{"name":"x","path":"p","measured":{"go_files":0,"tracked_files":0,"measured_at":"d","method":"m"},` + search + `}]}`, "at least one tracked file"},
		{"census more go than tracked", `{"entries":[{"name":"x","path":"p","measured":{"go_files":9,"tracked_files":2,"measured_at":"d","method":"m"},` + search + `}]}`, "impossible"},
		{"stress without census", `{"entries":[{"name":"x","path":"p","stress":true,` + search + `}]}`, "without a measured census"},
		{"stress below threshold", `{"entries":[{"name":"x","path":"p","stress":true,"measured":{"go_files":9999,"tracked_files":20000,"measured_at":"d","method":"m"},` + search + `}]}`, "declares stress with 9999"},
		{"stress at threshold ok", `{"entries":[{"name":"x","path":"p","stress":true,"measured":{"go_files":10000,"tracked_files":20000,"measured_at":"d","method":"m"},` + search + `}]}`, ""},
		{"stratification unknown repo", `{"stratification":[{"property":"generics","repo":"nope","evidence":"e"}],"entries":[{"name":"x","path":"p",` + search + `}]}`, "unknown repo"},
		{"stratification no repo no gap", `{"stratification":[{"property":"generics","evidence":"e"}],"entries":[{"name":"x","path":"p",` + search + `}]}`, "not marked as a gap"},
		{"stratification gap with repo", `{"stratification":[{"property":"generics","repo":"x","gap":true,"evidence":"e"}],"entries":[{"name":"x","path":"p",` + search + `}]}`, "marked as a gap but names repo"},
		{"stratification gap without evidence", `{"stratification":[{"property":"generics","gap":true}],"entries":[{"name":"x","path":"p",` + search + `}]}`, "no evidence"},
		{"stratification duplicate property", `{"stratification":[{"property":"generics","repo":"x","evidence":"e"},{"property":"generics","gap":true,"evidence":"e"}],"entries":[{"name":"x","path":"p",` + search + `}]}`, "mapped twice"},
		{"stratification explicit gap ok", `{"stratification":[{"property":"generics","gap":true,"evidence":"no selected repository uses type parameters"}],"entries":[{"name":"x","path":"p",` + search + `}]}`, ""},
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

// fr2Properties is the FR-2 stratification list. Every one of them must be
// mapped by name in the checked-in manifest — to a repository or to an
// explicit gap.
var fr2Properties = []string{
	"small library",
	"mid-size CLI application",
	"web or API service",
	"multi-package repository",
	"large repository or monorepo",
	"generics",
	"multiple go.mod",
	"build tags",
	"generated code",
	"tests and benchmarks",
}

// TestCheckedInManifest_GoCorpus pins the P0 FR-2 shape of the committed
// corpus: five or more real Go repositories pinned to a release tag AND a full
// commit sha, one measured stress target, every stratification property
// mapped, documented terms, and no new entry on the PR gate.
func TestCheckedInManifest_GoCorpus(t *testing.T) {
	m := loadCheckedInManifest(t)

	goRepos := 0
	for _, e := range m.Entries {
		if e.URL == "" || e.Language != "go" {
			continue
		}
		goRepos++
		if e.Ref == "" {
			t.Errorf("go entry %q has no release-tag ref", e.Name)
		}
		if len(e.SHA) != 40 {
			t.Errorf("go entry %q pins sha %q — FR-2 requires the FULL 40-char commit sha", e.Name, e.SHA)
		}
		if e.License == "" || e.PermittedUse == "" {
			t.Errorf("go entry %q must document license and permitted use", e.Name)
		}
		if e.Measured == nil || e.Measured.TrackedFiles == 0 {
			t.Errorf("go entry %q has no measured file census", e.Name)
		}
	}
	if goRepos < 5 {
		t.Errorf("corpus has %d pinned Go repositories, FR-2 requires >= 5", goRepos)
	}

	stress := 0
	for _, e := range m.Entries {
		if !e.Stress {
			continue
		}
		stress++
		if e.Measured.GoFiles < StressMinGoFiles {
			t.Errorf("stress entry %q measures %d go files, want >= %d", e.Name, e.Measured.GoFiles, StressMinGoFiles)
		}
		if e.Tier < 3 {
			t.Errorf("stress entry %q is tier %d — a stress target must never sit on the PR gate", e.Name, e.Tier)
		}
	}
	if stress == 0 {
		t.Error("no entry declares stress: FR-2 needs a >=10k-source-file target")
	}

	mapped := map[string]PropertyMapping{}
	for _, p := range m.Stratification {
		mapped[p.Property] = p
	}
	for _, want := range fr2Properties {
		p, ok := mapped[want]
		if !ok {
			t.Errorf("stratification property %q is not mapped (map it to a repo or record it as an explicit gap)", want)
			continue
		}
		if p.Gap {
			t.Logf("stratification property %q is recorded as an explicit gap: %s", want, p.Evidence)
		}
	}
	for got := range mapped {
		found := false
		for _, want := range fr2Properties {
			found = found || got == want
		}
		if !found {
			t.Errorf("stratification maps unknown property %q — FR-2 names the list", got)
		}
	}
}

// TestCheckedInManifest_PRGateUnchanged proves the Go-depth expansion did not
// grow the PR gate (corpus.yml runs -max-tier 2 on pull requests) and that
// cobra's confirmed-tier acceptance assertion survived it.
func TestCheckedInManifest_PRGateUnchanged(t *testing.T) {
	m := loadCheckedInManifest(t)

	wantPRGate := map[string]bool{
		"cobra": true, "flask": true, "sinatra": true, "ky": true, "express": true,
		"tier1-fixture-go": true, "tier1-fixture-hero-go": true,
		// WP-J6 (language-GA program G6): the hero-jvm suite is a hermetic
		// local fixture (Java+Kotlin, no network, no JDK — the pure-Go
		// grammars parse it and the binder runs in-process), so it belongs on
		// the PR gate exactly like hero-go.
		"tier1-fixture-hero-jvm": true,
		// SW-181 (language-GA program G3): the hero-python suite is a hermetic
		// local fixture (Python, no network, no Python interpreter — the
		// pure-Go gotreesitter grammar parses it and the heuristic resolver
		// runs in-process), so it belongs on the PR gate exactly like
		// hero-jvm and hero-go. The python pin (flask) is already on the gate.
		"tier1-fixture-hero-python": true,
		// SW-182 (language-GA program G6): the hero-typescript suite is a
		// hermetic local fixture (TypeScript, no network, no TS toolchain —
		// the pure-Go gotreesitter grammar parses it and the family resolver
		// runs in-process, including the exact-path target set proven immune
		// to LINK-001 directory fan-out), so it belongs on the PR gate
		// exactly like hero-python, hero-jvm and hero-go.
		"tier1-fixture-hero-typescript": true,
		// SW-197 (language-GA program G6): the 9 SW-197 hero fixtures are
		// hermetic local fixtures (no network, no toolchain — the pure-Go
		// gotreesitter grammars parse them and the per-language heuristic
		// resolvers run in-process), so they belong on the PR gate exactly
		// like hero-python, hero-jvm and hero-go. Bash declares honest-empty
		// per §5.5 (no cross-file construct); c/cpp share one resolver
		// impl (engine/link/resolve_c.go) and one fixture tree.
		"tier1-fixture-hero-bash":   true,
		"tier1-fixture-hero-c-cpp":  true,
		"tier1-fixture-hero-csharp": true,
		"tier1-fixture-hero-lua":    true,
		"tier1-fixture-hero-php":    true,
		"tier1-fixture-hero-ruby":   true,
		"tier1-fixture-hero-rust":   true,
		"tier1-fixture-hero-sql":    true,
	}
	for _, e := range m.Entries {
		if e.Tier > 2 {
			continue
		}
		if !wantPRGate[e.Name] {
			t.Errorf("entry %q runs on the PR gate at tier %d — new corpus entries must be tier 3 or 4", e.Name, e.Tier)
		}
	}

	var cobra *Entry
	for i := range m.Entries {
		if m.Entries[i].Name == "cobra" {
			cobra = &m.Entries[i]
		}
	}
	if cobra == nil {
		t.Fatal("cobra entry disappeared from the corpus")
	}
	want := ConfirmedEdge{SymbolQuery: "ExecuteC", Operation: "callers", Min: 1}
	if len(cobra.ConfirmedEdges) != 1 || cobra.ConfirmedEdges[0] != want {
		t.Errorf("cobra confirmed_edges = %+v, want exactly %+v (the typeresolve acceptance gate)", cobra.ConfirmedEdges, want)
	}
}

func loadCheckedInManifest(t *testing.T) Manifest {
	t.Helper()
	root, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Skipf("go env GOMOD unavailable: %v", err)
	}
	dir := filepath.Dir(strings.TrimSpace(string(root)))
	m, err := LoadManifest(filepath.Join(dir, "corpus", "manifest.json"))
	if err != nil {
		t.Fatalf("checked-in manifest invalid: %v", err)
	}
	return m
}

// TestRunner_PinFailsClosed proves the pin machinery the new tier-3/4 entries
// inherit actually BITES: a checkout whose HEAD differs from the recorded sha
// fails at the pin step before any indexing, and a pinned entry whose HEAD
// cannot be read fails too — never a warning, never a silent run against
// whatever the ref points at today.
func TestRunner_PinFailsClosed(t *testing.T) {
	repo := writeFixtureRepo(t)
	head := gitInitCommit(t, repo)

	t.Run("wrong sha", func(t *testing.T) {
		r := &Runner{Binary: "unused", WorkDir: t.TempDir(), PerEntryTimeout: time.Minute}
		m := localManifest(repo, []Search{{Query: "hello", ExpectNonEmpty: true}})
		// A plausible-looking but wrong 40-char pin: exactly the "upstream tag
		// was re-pointed" case.
		m.Entries[0].SHA = "0123456789abcdef0123456789abcdef01234567"
		rep, err := r.Run(context.Background(), m)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if rep.Pass {
			t.Fatal("run passed although HEAD does not match the pinned sha")
		}
		last := rep.Entries[0].Steps[len(rep.Entries[0].Steps)-1]
		if last.Name != "pin" || last.OK {
			t.Fatalf("expected a failing pin step, got %+v", rep.Entries[0].Steps)
		}
		if !strings.Contains(last.Detail, head) {
			t.Errorf("pin failure should report the actual HEAD %q, got %q", head, last.Detail)
		}
	})

	t.Run("unreadable head", func(t *testing.T) {
		r := &Runner{Binary: "unused", WorkDir: t.TempDir(), PerEntryTimeout: time.Minute}
		m := localManifest(t.TempDir(), []Search{{Query: "hello", ExpectNonEmpty: true}})
		m.Entries[0].SHA = "0123456789abcdef0123456789abcdef01234567"
		rep, err := r.Run(context.Background(), m)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if rep.Pass {
			t.Fatal("run passed although the pinned checkout has no readable HEAD")
		}
	})

	t.Run("matching sha proceeds past the pin", func(t *testing.T) {
		r := &Runner{Binary: "unused", WorkDir: t.TempDir(), PerEntryTimeout: time.Minute}
		m := localManifest(repo, []Search{{Query: "hello", ExpectNonEmpty: true}})
		m.Entries[0].SHA = head
		rep, err := r.Run(context.Background(), m)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		// The stub binary path is unusable, so the run still fails — but it must
		// fail LATER than the pin, proving the pin itself accepted the checkout.
		for _, s := range rep.Entries[0].Steps {
			if s.Name == "pin" && !s.OK {
				t.Fatalf("matching sha rejected at the pin step: %s", s.Detail)
			}
		}
	})
}

// gitInitCommit turns dir into a git repository with one commit and returns
// its HEAD sha. Hermetic: no remote, no network, isolated config.
func gitInitCommit(t *testing.T, dir string) string {
	t.Helper()
	env := append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+filepath.Join(t.TempDir(), "gitconfig"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=corpus", "GIT_AUTHOR_EMAIL=corpus@example.invalid",
		"GIT_COMMITTER_NAME=corpus", "GIT_COMMITTER_EMAIL=corpus@example.invalid",
	)
	run := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Skipf("git %v unavailable: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init")
	run("add", "-A")
	run("commit", "-m", "fixture")
	return run("rev-parse", "HEAD")
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
