package importfanout_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/goldenfile"
	"github.com/samibel/graphi/internal/importfanout"
)

// ceilingRelPath is the AX-16a (SW-253) ratchet declaration. It is a separate
// file from the AX-00 baseline on purpose: the baseline is history, the ceiling
// is the contract.
const ceilingRelPath = "docs/rc/ax16-import-fanout-ceiling.json"

// TestAX16_SurfacesClientFanoutRatchet is the gate (SW-253 AC-2/3/4/6). It runs
// inside `go test ./...` and therefore inside testgate — no new workflow.
//
// Re-pin deliberately, after reviewing why the set moved:
//
//	GRAPHI_UPDATE_GOLDEN=1 go test ./internal/importfanout -run TestAX16
//
// Regeneration keeps the category and reason of every edge it already knows,
// writes an empty reason for new ones and an empty `raised_by` placeholder when
// the count rose — both of which fail Validate until a human fills them in.
func TestAX16_SurfacesClientFanoutRatchet(t *testing.T) {
	root := moduleRoot(t)
	direct, err := importfanout.Measure(filepath.Join(root, filepath.FromSlash(measuredPackage)), measuredPackage)
	if err != nil {
		t.Fatalf("measuring %s: %v", measuredPackage, err)
	}
	ceilingPath := filepath.Join(root, filepath.FromSlash(ceilingRelPath))

	if goldenfile.UpdateRequested() {
		previous, loadErr := importfanout.LoadCeiling(ceilingPath)
		if loadErr != nil {
			t.Logf("no readable previous declaration (%v); regenerating from scratch — every reason will be empty", loadErr)
			previous = importfanout.Ceiling{Targets: importfanout.Targets{Intermediate: 30, Final: 20}}
		}
		raw, err := previous.Regenerate(direct).Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(ceilingPath, raw, 0o644); err != nil {
			t.Fatalf("write %s: %v", ceilingPath, err)
		}
		t.Logf("ceiling %s RE-PINNED (%s=1): direct %d; now write a reason for every new edge and name the story in raised_by if the number rose", ceilingRelPath, goldenfile.UpdateEnvVar, direct.Fanout)
		return
	}

	ceiling, err := importfanout.LoadCeiling(ceilingPath)
	if err != nil {
		t.Fatalf("AX-16 fan-out ceiling is required: %v. Create it with `%s=1 go test ./internal/importfanout -run TestAX16` and write a reason per edge.", err, goldenfile.UpdateEnvVar)
	}
	if err := ceiling.Validate(); err != nil {
		t.Fatal(err)
	}
	if ceiling.Package != measuredPackage {
		t.Fatalf("%s declares package %q, but this test measures %q", ceilingRelPath, ceiling.Package, measuredPackage)
	}

	transitive, err := importfanout.MeasureTransitive(root, measuredPackage)
	if err != nil {
		t.Fatalf("transitive closure: %v", err)
	}

	rep := importfanout.Check(ceiling, direct, transitive)
	// AC-4: both numbers on one line, in this exact shape. The transitive number
	// is reported, not gated — it is the anti-gaming instrument for the direct one.
	t.Log(rep.Line())
	if !rep.Pass() {
		t.Fatal(rep.Format(ceilingRelPath))
	}
	t.Logf("targets: intermediate <= %d, final < %d (intent, not gated)", ceiling.Targets.Intermediate, ceiling.Targets.Final)
}

// fixtureTree writes a small module tree under a temp dir:
//
//	hub        imports engine/a, engine/b
//	engine/a   imports core/c
//	engine/b   imports engine/a, core/c
//	core/c     imports stdlib only
//
// Direct fan-out of hub is 2; transitive closure is 3.
func fixtureTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("hub/hub.go", "package hub\n\nimport (\n\t_ \"github.com/samibel/graphi/engine/a\"\n\t_ \"github.com/samibel/graphi/engine/b\"\n)\n")
	write("hub/hub_test.go", "package hub\n\nimport _ \"github.com/samibel/graphi/engine/testonly\"\n")
	write("engine/a/a.go", "package a\n\nimport _ \"github.com/samibel/graphi/core/c\"\n")
	write("engine/b/b.go", "package b\n\nimport (\n\t_ \"github.com/samibel/graphi/engine/a\"\n\t_ \"github.com/samibel/graphi/core/c\"\n)\n")
	write("core/c/c.go", "package c\n\nimport \"fmt\"\n\nvar _ = fmt.Sprint\n")
	return root
}

func fixtureCeiling() importfanout.Ceiling {
	return importfanout.Ceiling{
		Package: "hub",
		Ceiling: 2,
		AllowedImports: []importfanout.AllowedImport{
			{Path: "engine/a", Category: importfanout.CategoryEngineHandler, Reason: "a"},
			{Path: "engine/b", Category: importfanout.CategoryEngineHandler, Reason: "b"},
		},
		Targets: importfanout.Targets{Intermediate: 2, Final: 1},
	}
}

func measureFixture(t *testing.T, root string) (importfanout.Result, importfanout.Transitive) {
	t.Helper()
	direct, err := importfanout.Measure(filepath.Join(root, "hub"), "hub")
	if err != nil {
		t.Fatal(err)
	}
	transitive, err := importfanout.MeasureTransitive(root, "hub")
	if err != nil {
		t.Fatal(err)
	}
	return direct, transitive
}

// TestAX16_RatchetHoldsOnTheFixture is the positive control for the kill tests.
func TestAX16_RatchetHoldsOnTheFixture(t *testing.T) {
	root := fixtureTree(t)
	c := fixtureCeiling()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	direct, transitive := measureFixture(t, root)
	rep := importfanout.Check(c, direct, transitive)
	if !rep.Pass() {
		t.Fatalf("the ratchet failed a tree that matches its declaration:\n%s", rep.Format("x.json"))
	}
	if got, want := rep.Line(), "direct 2 (ceiling 2) · transitive 3"; got != want {
		t.Errorf("Line() = %q, want %q", got, want)
	}
	if out := rep.Format(""); !strings.Contains(out, "holds") || strings.Contains(out, "FAILED") {
		t.Errorf("passing render is wrong:\n%s", out)
	}
}

// TestAX16_GateFailsOnAnUndeclaredImport is the kill test (SW-253 AC-7): one
// undeclared import in a temp-dir fixture must make the check fail, naming it.
// Same shape as TestSW248_GateFailsOnAnUnreachableShadowOperation.
func TestAX16_GateFailsOnAnUndeclaredImport(t *testing.T) {
	root := fixtureTree(t)
	// Exchange an edge: hub drops engine/b and picks up core/c. The count is
	// unchanged (2), so a count-only gate would pass this. The set must not.
	hub := filepath.Join(root, "hub", "hub.go")
	if err := os.WriteFile(hub, []byte("package hub\n\nimport (\n\t_ \"github.com/samibel/graphi/engine/a\"\n\t_ \"github.com/samibel/graphi/core/c\"\n)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	direct, transitive := measureFixture(t, root)
	rep := importfanout.Check(fixtureCeiling(), direct, transitive)
	if rep.Pass() {
		t.Fatal("the ratchet passed a package with an undeclared import at an unchanged count; " +
			"it would not have caught the exchanged edge it exists to catch")
	}
	if len(rep.Undeclared) != 1 || rep.Undeclared[0] != "core/c" {
		t.Fatalf("the gate names %v as undeclared, want exactly [core/c]", rep.Undeclared)
	}
	if len(rep.Stale) != 1 || rep.Stale[0] != "engine/b" {
		t.Fatalf("the gate names %v as stale, want exactly [engine/b]", rep.Stale)
	}
	out := rep.Format("docs/rc/x.json")
	for _, want := range []string{"import fan-out ratchet FAILED", "undeclared import", "core/c", "declared but no longer imported", "engine/b", "docs/rc/x.json"} {
		if !strings.Contains(out, want) {
			t.Errorf("the failing render is missing %q:\n%s", want, out)
		}
	}
}

// TestAX16_GateFailsAboveTheCeilingNamingTheAddedEdges is AC-2, first clause.
func TestAX16_GateFailsAboveTheCeilingNamingTheAddedEdges(t *testing.T) {
	root := fixtureTree(t)
	hub := filepath.Join(root, "hub", "hub.go")
	if err := os.WriteFile(hub, []byte("package hub\n\nimport (\n\t_ \"github.com/samibel/graphi/engine/a\"\n\t_ \"github.com/samibel/graphi/engine/b\"\n\t_ \"github.com/samibel/graphi/core/c\"\n)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	direct, transitive := measureFixture(t, root)
	rep := importfanout.Check(fixtureCeiling(), direct, transitive)
	if rep.Pass() {
		t.Fatal("direct 3 against ceiling 2 passed")
	}
	out := rep.Format("")
	for _, want := range []string{"exceeds the ceiling 2", "added edges: core/c", "direct 3 (ceiling 2) · transitive 3"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
}

// TestAX16_GateFailsBelowTheCeilingAndSaysToLowerIt is AC-2, third clause:
// gains are locked in, never left as slack.
func TestAX16_GateFailsBelowTheCeilingAndSaysToLowerIt(t *testing.T) {
	root := fixtureTree(t)
	hub := filepath.Join(root, "hub", "hub.go")
	if err := os.WriteFile(hub, []byte("package hub\n\nimport _ \"github.com/samibel/graphi/engine/a\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	direct, transitive := measureFixture(t, root)
	rep := importfanout.Check(fixtureCeiling(), direct, transitive)
	if rep.Pass() {
		t.Fatal("direct 1 against ceiling 2 passed — slack was left in the ratchet")
	}
	out := rep.Format("")
	for _, want := range []string{"BELOW the ceiling 2", "lower `ceiling` to 1", "engine/b"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
}

// TestAX16_ValidateRejectsAnUnreviewedRaise is AC-3 plus the AC-1 schema rules.
func TestAX16_ValidateRejectsAnUnreviewedRaise(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(c *importfanout.Ceiling)
		want   string
	}{
		{"ceiling above declared edges", func(c *importfanout.Ceiling) { c.Ceiling = 3 }, "greater than the 2 declared"},
		{"raised_by without a reason", func(c *importfanout.Ceiling) { c.RaisedBy = &importfanout.Raise{Story: "SW-999"} }, "`raised_by` is set but"},
		{"raised_by without a story", func(c *importfanout.Ceiling) { c.RaisedBy = &importfanout.Raise{Reason: "because"} }, "`raised_by` is set but"},
		{"empty reason", func(c *importfanout.Ceiling) { c.AllowedImports[0].Reason = "  " }, "`reason` is empty"},
		{"unknown category", func(c *importfanout.Ceiling) { c.AllowedImports[0].Category = "misc" }, "`category` \"misc\" is not one of"},
		{"duplicate path", func(c *importfanout.Ceiling) { c.AllowedImports[1].Path = "engine/a" }, "declared twice"},
		{"unsorted", func(c *importfanout.Ceiling) {
			c.AllowedImports[0], c.AllowedImports[1] = c.AllowedImports[1], c.AllowedImports[0]
		}, "keep the list sorted"},
		{"absolute module path", func(c *importfanout.Ceiling) { c.AllowedImports[0].Path = "github.com/samibel/graphi/engine/a" }, "must be module-relative"},
		{"final target above intermediate", func(c *importfanout.Ceiling) { c.Targets.Final = 5 }, "`targets`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := fixtureCeiling()
			tc.mutate(&c)
			err := c.Validate()
			if err == nil {
				t.Fatal("Validate accepted an invalid declaration")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error does not say %q:\n%v", tc.want, err)
			}
		})
	}

	// A properly named raise is fine.
	c := fixtureCeiling()
	c.RaisedBy = &importfanout.Raise{Story: "SW-999", Reason: "one new engine handler seam"}
	if err := c.Validate(); err != nil {
		t.Errorf("a named raise was rejected: %v", err)
	}
}

// TestAX16_RegeneratePreservesKnownReasonsAndFlagsARaise pins the re-pin path.
func TestAX16_RegeneratePreservesKnownReasonsAndFlagsARaise(t *testing.T) {
	c := fixtureCeiling()

	// The count rose: engine/b, engine/z added; engine/a kept.
	up := c.Regenerate(importfanout.Result{Package: "hub", Fanout: 3, Imports: []string{"engine/z", "engine/b", "engine/a"}})
	if up.Ceiling != 3 {
		t.Errorf("ceiling = %d, want 3 (the measured value)", up.Ceiling)
	}
	if up.RaisedBy == nil || up.RaisedBy.Story != "" || up.RaisedBy.Reason != "" {
		t.Errorf("a rise must leave an EMPTY raised_by placeholder, got %+v", up.RaisedBy)
	}
	if err := up.Validate(); err == nil {
		t.Error("a freshly regenerated raise validated without a human writing the reason and the story")
	}
	wantPaths := []string{"engine/a", "engine/b", "engine/z"}
	for i, edge := range up.AllowedImports {
		if edge.Path != wantPaths[i] {
			t.Fatalf("allowed_imports[%d] = %q, want %q (sorted)", i, edge.Path, wantPaths[i])
		}
	}
	if up.AllowedImports[0].Reason != "a" || up.AllowedImports[0].Category != importfanout.CategoryEngineHandler {
		t.Errorf("a known edge lost its reason/category: %+v", up.AllowedImports[0])
	}
	if up.AllowedImports[2].Reason != "" || up.AllowedImports[2].Category != "" {
		t.Errorf("a new edge must be written with an empty reason and category, got %+v", up.AllowedImports[2])
	}
	if up.Targets != c.Targets {
		t.Errorf("targets changed on regeneration: %+v", up.Targets)
	}

	// The count fell: the ceiling follows it down and raised_by stays null.
	down := c.Regenerate(importfanout.Result{Package: "hub", Fanout: 1, Imports: []string{"engine/a"}})
	if down.Ceiling != 1 || down.RaisedBy != nil || len(down.AllowedImports) != 1 {
		t.Errorf("lowering: got ceiling %d raised_by %+v edges %d", down.Ceiling, down.RaisedBy, len(down.AllowedImports))
	}
	if err := down.Validate(); err != nil {
		t.Errorf("a lowered declaration must validate as-is: %v", err)
	}
}

// TestMeasureTransitive_WalksTheClosureAndExcludesSelfAndTests is AC-4's unit
// test on a temp-dir fixture, and the anti-gaming demonstration: pushing an
// import one hop away changes the direct number and not the transitive one.
func TestMeasureTransitive_WalksTheClosureAndExcludesSelfAndTests(t *testing.T) {
	root := fixtureTree(t)
	direct, transitive := measureFixture(t, root)
	if direct.Fanout != 2 {
		t.Fatalf("direct = %d, want 2", direct.Fanout)
	}
	if transitive.Count != 3 {
		t.Fatalf("transitive = %d, want 3 (engine/a, engine/b, core/c); got %v", transitive.Count, transitive.Packages)
	}
	want := []string{"core/c", "engine/a", "engine/b"}
	for i := range want {
		if transitive.Packages[i] != want[i] {
			t.Fatalf("packages = %v, want %v", transitive.Packages, want)
		}
	}
	if transitive.Package != "hub" {
		t.Errorf("package = %q", transitive.Package)
	}

	// Gaming attempt: a collector package `engine/all` re-exports a and b, hub
	// imports only the collector. Direct drops 2 → 1; transitive rises 3 → 4
	// (the collector itself is now reachable) — nothing real was removed.
	if err := os.MkdirAll(filepath.Join(root, "engine", "all"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "engine", "all", "all.go"), []byte("package all\n\nimport (\n\t_ \"github.com/samibel/graphi/engine/a\"\n\t_ \"github.com/samibel/graphi/engine/b\"\n)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "hub", "hub.go"), []byte("package hub\n\nimport _ \"github.com/samibel/graphi/engine/all\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gamedDirect, gamedTransitive := measureFixture(t, root)
	if gamedDirect.Fanout != 1 {
		t.Errorf("gamed direct = %d, want 1", gamedDirect.Fanout)
	}
	if gamedTransitive.Count != 4 {
		t.Errorf("gamed transitive = %d, want 4 — the collector hides nothing from the closure; got %v", gamedTransitive.Count, gamedTransitive.Packages)
	}
}

// TestMeasureTransitive_MissingPackageIsAnError: an import that resolves to no
// directory must not be silently dropped from the closure.
func TestMeasureTransitive_MissingPackageIsAnError(t *testing.T) {
	root := fixtureTree(t)
	if err := os.RemoveAll(filepath.Join(root, "core", "c")); err != nil {
		t.Fatal(err)
	}
	if _, err := importfanout.MeasureTransitive(root, "hub"); err == nil {
		t.Fatal("a reachable package with no directory was silently dropped from the closure")
	}
}

// TestAX16_LoadCeilingRejectsUnknownFields: a misspelt key must not silently
// declare nothing.
func TestAX16_LoadCeilingRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(path, []byte(`{"package":"hub","ceiling":1,"allowed_import":[],"targets":{"intermediate":1,"final":1},"raised_by":null}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := importfanout.LoadCeiling(path); err == nil {
		t.Fatal("LoadCeiling accepted an unknown field")
	}
	if _, err := importfanout.LoadCeiling(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("LoadCeiling accepted a missing file")
	}
}
