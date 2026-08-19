package parity

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/samibel/graphi/internal/corpus"
	"github.com/samibel/graphi/internal/parityreport"
)

// ---------------------------------------------------------------------------
// WP-J7 (SW-176): the JVM half of the real-repository harness.
// ---------------------------------------------------------------------------

// TestJVMClassTable_BindsToDeclaredMatrix is the JVM drift guard, the same
// bidirectional shape TestClassTable_BindsToDeclaredMatrix applies to the Go
// table.
//
//	MISSING  every declared, non-deferred JVM change class has a real-repo planner.
//	PHANTOM  every planner's id is a declared class.
//	KIND     no deferred or non-change-class row has a planner.
func TestJVMClassTable_BindsToDeclaredMatrix(t *testing.T) {
	rows, err := LoadClasses(filepath.Join("..", "..", ClassesPathJVM))
	if err != nil {
		t.Fatalf("LoadClasses(%s): %v", ClassesPathJVM, err)
	}
	byID := JVMSpecByID()
	declared := map[string]ClassRow{}
	for _, r := range rows {
		declared[r.ID] = r
	}

	t.Run("MISSING", func(t *testing.T) {
		var missing []string
		for _, r := range rows {
			if r.Kind != kindChangeClass || r.HarnessRow == harnessDeferred {
				continue
			}
			if _, ok := byID[r.ID]; !ok {
				missing = append(missing, r.ID)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Fatalf("MISSING: %s declares %d non-deferred change class(es) with no real-repo JVM planner: %s",
				ClassesPathJVM, len(missing), strings.Join(missing, ", "))
		}
	})

	t.Run("PHANTOM", func(t *testing.T) {
		for _, s := range jvmSpecs() {
			d, ok := declared[s.ID]
			if !ok {
				t.Errorf("PHANTOM: JVM planner %q has no matching id in %s — `id` is a frozen wire identifier",
					s.ID, ClassesPathJVM)
				continue
			}
			if d.Kind != kindChangeClass {
				t.Errorf("KIND: JVM planner %q is a change class here but %s declares kind %q",
					s.ID, ClassesPathJVM, d.Kind)
			}
			if d.HarnessRow == harnessDeferred {
				t.Errorf("KIND: JVM planner %q exists but %s defers the class to %s",
					s.ID, ClassesPathJVM, d.DeferredTo)
			}
		}
	})

	t.Run("EVERY_ROW_CARRIES_A_NOTE", func(t *testing.T) {
		// A row whose scope is not stated in the artifact is a row a reader will
		// over-read. This is the same rule the Go table's ClassSpec.Note serves,
		// made mandatory here because EVERY JVM row has a narrower scope than
		// its hermetic twin (no witness is possible on a real repository).
		for _, s := range jvmSpecs() {
			if len(strings.TrimSpace(s.Note)) < 40 {
				t.Errorf("JVM class %q has no substantive Note; every real-repo JVM row must state what it does and does not prove", s.ID)
			}
		}
	})
}

// TestEnvJVMBinder_MatchesEngineSemantic is the drift guard on the one product
// string this package copies instead of importing.
//
// It reads engine/semantic/semantic.go from disk rather than linking it, which
// is exactly the point: the assertion is as strong as an import and costs the
// harness none of the coupling TestSnapshotBoundary_OnlyGraphstoreIsImported
// forbids. If the product ever renames the variable, this fails and names the
// file to look in.
func TestEnvJVMBinder_MatchesEngineSemantic(t *testing.T) {
	p := filepath.Join("..", "..", "engine", "semantic", "semantic.go")
	src, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	want := `EnvJVM = "` + EnvJVMBinder + `"`
	if !strings.Contains(string(src), want) {
		t.Fatalf("DRIFT: %s no longer declares %s. internal/parity copies that string as EnvJVMBinder "+
			"(it may not link engine/semantic); update the copy and this guard together.", p, want)
	}
}

// TestJVMAxes_AreCrossedAndFrozen pins the axis crossing.
//
// The suffixes are FROZEN WIRE IDENTIFIERS: -verdict-diff and -counts-diff key
// on the row id, so renaming a suffix silently makes two dispatches
// incomparable — every row would read "only in run a" / "only in run b" and the
// gate would report a difference that is purely cosmetic, or (worse) a future
// reader would compare a renamed report against an old one and see nothing.
func TestJVMAxes_AreCrossedAndFrozen(t *testing.T) {
	got := map[string]bool{}
	for _, a := range jvmAxes() {
		got[a.Suffix()] = true
	}
	want := []string{
		"[binder=off,profile=default]",
		"[binder=off,profile=fast]",
		"[binder=on,profile=default]",
		"[binder=on,profile=fast]",
	}
	if len(got) != len(want) {
		t.Fatalf("axis crossing produced %d distinct cells, want %d — a collision would make two cells share one row id", len(got), len(want))
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing frozen axis cell %q", w)
		}
	}
	// The binder axis must actually differ, or the crossing is decoration.
	on, off := 0, 0
	for _, a := range jvmAxes() {
		if a.Binder {
			on++
		} else {
			off++
		}
	}
	if on == 0 || off == 0 {
		t.Fatalf("the binder axis must contain both states (on=%d off=%d); a JVM matrix that never runs the binder proves nothing about it", on, off)
	}
}

// TestGraphiAxis_ClearsBothEnvVars is the guard on the axis label's honesty.
//
// It is a RED-WITHOUT-FIX shape: the child is a script that prints the two
// variables it received, and the test exports BOTH of them in the parent with
// values that would corrupt every row. A harness that merely `append`ed its own
// values without clearing would still pass a naive check (the last assignment
// wins in exec), so the assertion is on the OBSERVED child environment, which is
// the only thing that can distinguish "cleared then set" from "hoped for".
func TestGraphiAxis_ClearsBothEnvVars(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a child process")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fakegraphi")
	body := "#!/bin/sh\necho \"PROFILE=[${GRAPHI_INDEX_PROFILE}]\"\necho \"BINDER=[${GRAPHI_JVM_TYPERESOLVE}]\"\necho \"ARGS=[$*]\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv("GRAPHI_INDEX_PROFILE", "deep")
	t.Setenv(EnvJVMBinder, "1")

	r := &Runner{Binary: script}
	for _, tc := range []struct {
		axis        JVMAxis
		wantProfile string
		wantBinder  string
		wantArgs    string
	}{
		{JVMAxis{Binder: false, Profile: ""}, "", "", "rebuild"},
		{JVMAxis{Binder: false, Profile: "fast"}, "", "", "rebuild -profile fast"},
		{JVMAxis{Binder: true, Profile: ""}, "", "1", "rebuild"},
		{JVMAxis{Binder: true, Profile: "fast"}, "", "1", "rebuild -profile fast"},
	} {
		out, err := r.graphiAxis(context.Background(), dir, tc.axis, "rebuild")
		if err != nil {
			t.Fatalf("%s: %v\n%s", tc.axis.Suffix(), err, out)
		}
		s := string(out)
		if !strings.Contains(s, "PROFILE=["+tc.wantProfile+"]") {
			t.Errorf("%s: child saw the INHERITED GRAPHI_INDEX_PROFILE; want %q.\n%s",
				tc.axis.Suffix(), tc.wantProfile, s)
		}
		if !strings.Contains(s, "BINDER=["+tc.wantBinder+"]") {
			t.Errorf("%s: child's %s is not what the axis declares; want %q. An inherited value here "+
				"would make a binder=off row silently measure the binder while the report still said off.\n%s",
				tc.axis.Suffix(), EnvJVMBinder, tc.wantBinder, s)
		}
		if !strings.Contains(s, "ARGS=["+tc.wantArgs+"]") {
			t.Errorf("%s: child args = %q, want %q", tc.axis.Suffix(), s, tc.wantArgs)
		}
	}
}

// ---------------------------------------------------------------------------
// The scanner.
// ---------------------------------------------------------------------------

// TestMask_NeutralisesCommentsAndLiterals is the property the whole scanner
// rests on. Real JVM source is full of braces inside strings and comments; a
// depth counter that sees them desynchronises on the first one and every
// downstream offset is then wrong — silently, because the model would still
// return SOMETHING.
func TestMask_NeutralisesCommentsAndLiterals(t *testing.T) {
	src := []byte("class A {\n  // }\n  String s = \"}{\";\n  /* } */\n  char c = '}';\n  void m() {}\n}\n")
	msk := mask(src)
	if len(msk) != len(src) {
		t.Fatalf("mask changed length %d -> %d; offsets must be preserved", len(src), len(msk))
	}
	depth := 0
	for _, b := range msk {
		switch b {
		case '{':
			depth++
		case '}':
			depth--
		}
	}
	if depth != 0 {
		t.Fatalf("brace depth after masking = %d, want 0 — a brace in a comment or literal survived", depth)
	}
	// The control: the SAME source without masking does NOT balance, which is
	// what makes this test non-vacuous.
	raw := 0
	for _, b := range src {
		switch b {
		case '{':
			raw++
		case '}':
			raw--
		}
	}
	if raw == 0 {
		t.Fatal("CONTROL FAILED: the fixture balances without masking, so this test would pass with mask() removed")
	}
}

func TestDiscoverJVM_ModelsTypesMethodsImportsAndMixedDirs(t *testing.T) {
	root := writeJVMFixture(t)
	m, err := discoverJVM(root)
	if err != nil {
		t.Fatalf("discoverJVM: %v", err)
	}

	get := func(rel string) *JVMFile {
		f := m.fileAt(rel)
		if f == nil {
			t.Fatalf("model has no file %q", rel)
		}
		return f
	}

	cart := get("src/shop/Cart.java")
	if cart.Pkg != "shop" {
		t.Errorf("Cart.java package = %q, want shop", cart.Pkg)
	}
	if len(cart.Types) != 1 || cart.Types[0].Name != "Cart" || cart.Types[0].Super != "Base" {
		t.Fatalf("Cart.java types = %+v, want one class Cart extends Base", cart.Types)
	}
	onDemand := false
	for _, im := range cart.Imports {
		if im.Path == "tax" && im.OnDemand {
			onDemand = true
		}
	}
	if !onDemand {
		t.Errorf("Cart.java imports = %+v, want the on-demand import tax.*", cart.Imports)
	}

	helper := get("src/util/Helper.java")
	if len(helper.Types) != 1 || len(helper.Types[0].Nested) != 1 || helper.Types[0].Nested[0].Name != "Inner" {
		t.Fatalf("Helper.java nested types = %+v, want one nested Inner", helper.Types)
	}

	app := get("src/k/App.kt")
	if len(app.TopFuncs) == 0 {
		t.Fatalf("App.kt has no top-level fun; Kotlin top-level functions are what jvm_move_symbol moves")
	}
	arity := map[string]int{}
	for _, ty := range app.Types {
		for _, mm := range ty.Methods {
			arity[mm.Name] = mm.Arity
		}
	}
	if arity["tag"] != 1 {
		t.Errorf("App.tag arity = %d, want 1", arity["tag"])
	}
	if arity["pair"] != 2 {
		t.Errorf("App.pair arity = %d, want 2 — a generic parameter must not be miscounted as two", arity["pair"])
	}

	mix := m.dirOf("src/mix")
	if mix == nil || !mix.Mixed() {
		t.Fatalf("src/mix must be a MIXED-LANGUAGE directory (got %+v); the two W0.h rows have no target without one", mix)
	}
}

// ---------------------------------------------------------------------------
// The planners.
// ---------------------------------------------------------------------------

// TestJVMPlanners_EveryDeclaredClassPlansOnTheFixture is the non-vacuity gate on
// the planner table: every declared class must find a real target in real
// source, and the edit it plans must actually change bytes.
//
// A planner that quietly returns errNoTarget on every repository would make its
// row SKIP forever, and a SKIPPED row is not a pass — but nothing would ever say
// so out loud. This test is what says so.
func TestJVMPlanners_EveryDeclaredClassPlansOnTheFixture(t *testing.T) {
	root := writeJVMFixture(t)
	m, err := discoverJVM(root)
	if err != nil {
		t.Fatalf("discoverJVM: %v", err)
	}
	for _, s := range jvmSpecs() {
		s := s
		t.Run(s.ID, func(t *testing.T) {
			mut, err := s.Plan(m)
			if err != nil {
				t.Fatalf("planner returned %v; the fixture is built to exhibit every declared class", err)
			}
			if mut == nil || len(mut.Ops) == 0 {
				t.Fatalf("planner returned no file operations")
			}
			if len(strings.TrimSpace(mut.Desc)) < 30 {
				t.Fatalf("mutation description %q is too thin to re-apply by hand", mut.Desc)
			}
			changed := false
			for _, op := range mut.Ops {
				switch op.Kind {
				case opDelete, opRenameDir:
					changed = true
				case opWrite:
					f := m.fileAt(op.Path)
					if f == nil || string(f.Src) != string(op.Data) {
						changed = true
					}
				}
			}
			if !changed {
				t.Fatalf("planner produced ops that change nothing: %+v", mut.Ops)
			}
			t.Logf("%s", mut.Desc)
		})
	}
}

// TestJVMPlanners_AreDeterministic re-plans every class over a freshly scanned
// model and requires an identical mutation.
//
// This is the property -counts-diff depends on: if a planner chose its target by
// map iteration order, two dispatches would edit different files and the counts
// gate would report a product non-determinism that was really the harness's own.
func TestJVMPlanners_AreDeterministic(t *testing.T) {
	root := writeJVMFixture(t)
	a, err := discoverJVM(root)
	if err != nil {
		t.Fatalf("discoverJVM: %v", err)
	}
	for i := 0; i < 5; i++ {
		b, err := discoverJVM(root)
		if err != nil {
			t.Fatalf("discoverJVM: %v", err)
		}
		for _, s := range jvmSpecs() {
			ma, ea := s.Plan(a)
			mb, eb := s.Plan(b)
			if (ea == nil) != (eb == nil) {
				t.Fatalf("%s: planner disagreed with itself across two scans (%v vs %v)", s.ID, ea, eb)
			}
			if ea != nil {
				continue
			}
			if ma.Desc != mb.Desc || len(ma.Ops) != len(mb.Ops) {
				t.Fatalf("%s: NON-DETERMINISTIC planner.\n  a: %s\n  b: %s", s.ID, ma.Desc, mb.Desc)
			}
			for k := range ma.Ops {
				if ma.Ops[k].Kind != mb.Ops[k].Kind || ma.Ops[k].Path != mb.Ops[k].Path ||
					string(ma.Ops[k].Data) != string(mb.Ops[k].Data) || ma.Ops[k].NewPath != mb.Ops[k].NewPath {
					t.Fatalf("%s: op %d differs between two scans of the same tree", s.ID, k)
				}
			}
		}
	}
}

// TestJVMRepos_LocalFixturesAreRefusedByDefault mirrors the Go guard: a JVM
// matrix row that runs on a checked-in fixture is not evidence.
func TestJVMRepos_LocalFixturesAreRefusedByDefault(t *testing.T) {
	m, err := corpus.LoadManifest(filepath.Join("..", "..", "corpus", "manifest.json"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	pins := jvmRepos(m, 3, false)
	if len(pins) == 0 {
		t.Fatal("the JVM candidate pool is EMPTY: corpus/manifest.json declares no java/kotlin pin within tier 3, so WP-J7 has nothing to measure")
	}
	for _, e := range pins {
		if e.URL == "" {
			t.Errorf("local fixture %q reached the real-repo JVM candidate pool", e.Name)
		}
	}
	// The walk order is load-bearing: it is what makes a class land on the
	// smallest pin that can host it, and it must be stable across dispatches.
	for i := 1; i < len(pins); i++ {
		if jvmFileCount(pins[i-1]) > jvmFileCount(pins[i]) && pins[i-1].Tier == pins[i].Tier {
			t.Errorf("JVM pins are not ordered by source-file count within a tier: %s(%d) before %s(%d)",
				pins[i-1].Name, jvmFileCount(pins[i-1]), pins[i].Name, jvmFileCount(pins[i]))
		}
	}
}

// TestJVMRunner_EndToEndOnALocalFixture drives the BUILT BINARY over a local
// JVM fixture: clone-free, network-free, and through the same Runner the
// dispatch uses.
//
// It proves the JVM half is capable of DISTINGUISHING pass from fail, which a
// green-only test cannot: alongside the real classes it runs a row whose
// planner is deliberately absent from the table, and requires that row to come
// back ERROR rather than quietly PASS.
func TestJVMRunner_EndToEndOnALocalFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("drives the built binary")
	}
	bin := buildGraphi(t)
	root := writeJVMFixture(t)

	rows := []ClassRow{
		{ID: "jvm_add_file", Kind: kindChangeClass, Label: "add a Java file", HarnessRow: "required"},
		{ID: "jvm_delete_file", Kind: kindChangeClass, Label: "delete a Java file", HarnessRow: "required"},
		{ID: "jvm_not_a_declared_class", Kind: kindChangeClass, Label: "undeclared", HarnessRow: "required"},
	}
	m := corpus.Manifest{Entries: []corpus.Entry{{
		Name: "jvm-fixture", Path: root, Tier: 1, Language: langJava,
		Measured: &corpus.FileCensus{SourceFiles: 9},
	}}}

	r := &Runner{
		Binary: bin, WorkDir: t.TempDir(), MaxTier: 1, AllowLocal: true,
		PerClassTimeout: 5 * time.Minute, RunnerClass: "test",
		Log: func(f string, a ...any) { t.Logf(f, a...) },
	}
	prov := parityreport.NewProvenance("test-sha")
	prov.WorktreeClean, prov.ProductDiffEmpty = true, true

	rep, err := r.RunJVM(context.Background(), m, rows, prov)
	if err != nil {
		t.Fatalf("RunJVM: %v", err)
	}
	if rep.Family != "jvm" {
		t.Errorf("report family = %q, want jvm", rep.Family)
	}
	if rep.MatrixSource != ClassesPathJVM {
		t.Errorf("report matrix source = %q, want %q", rep.MatrixSource, ClassesPathJVM)
	}
	got := rep.VerdictSet()
	for _, c := range rep.Classes {
		t.Logf("%-46s %-8s repo=%s inc=%d/%d full=%d/%d\n    axis: %s\n    %s",
			c.ID, c.Verdict, c.Repo, c.IncNodes, c.IncEdges, c.FullNodes, c.FullEdges, c.AxisNote, c.Mutation)
	}
	cells := 0
	for _, a := range jvmAxes() {
		cells++
		for _, id := range []string{"jvm_add_file", "jvm_delete_file"} {
			v := got[id+a.Suffix()]
			if v != parityreport.VerdictPass && v != parityreport.VerdictFail {
				t.Errorf("%s%s = %s, want a decided verdict (PASS or FAIL)", id, a.Suffix(), v)
			}
		}
		if v := got["jvm_not_a_declared_class"+a.Suffix()]; v != parityreport.VerdictError {
			t.Errorf("an undeclared class must come back ERROR, got %s — a harness that cannot "+
				"tell a missing planner from a passing row is not a gate", v)
		}
	}
	if cells != 4 {
		t.Fatalf("axis crossing produced %d cells, want 4", cells)
	}
	if len(rep.Classes) != len(rows)*cells {
		t.Errorf("report has %d rows, want %d (%d classes x %d axis cells)", len(rep.Classes), len(rows)*cells, len(rows), cells)
	}
	if rep.Publishable {
		t.Error("a three-row run over a thirteen-class matrix must never be publishable")
	}
	if len(rep.StoreCounts) == 0 {
		t.Error("the §12.3 store-level counts were not taken on any JVM row")
	}
	// Every decided row must carry its axis in the ARTIFACT, not only in the id.
	for _, c := range rep.Classes {
		if c.AxisNote == "" {
			t.Errorf("row %s carries no axis_note; a reader cannot tell whether the binder ran", c.ID)
		}
	}
}

// writeJVMFixture materializes a small Java+Kotlin tree exhibiting every
// declared JVM change class, including a MIXED-LANGUAGE directory.
//
// It is written as a git repository because the Runner restores a materialized
// clone between rows, and a local Path entry is restored by re-copying the
// pristine source — so the git history is not strictly needed here, but the
// tree must otherwise look exactly like a real one.
func writeJVMFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		// alt.Rate is the SHADOWING target: a second type with the simple name
		// Rate, in a package Cart.java does not import on demand.
		"src/alt/Rate.java": `package alt;

public class Rate {
  public String rate() {
    return "alt";
  }
}
`,
		"src/tax/Rate.java": `package tax;

public class Rate {
  public String rate() {
    return "tax";
  }
}
`,
		"src/shop/Base.java": `package shop;

public class Base {
  public void ping() {
  }
}
`,
		"src/shop/Other.java": `package shop;

public class Other {
  public void ping2() {
  }
}
`,
		"src/shop/Cart.java": `package shop;

import tax.*;

public class Cart extends Base {
  public void checkout() {
    Rate r = new Rate();
    r.rate();
  }

  public String name() {
    return "cart";
  }

  public void price(int cents) {
  }
}
`,
		"src/util/Helper.java": `package util;

import alt.Rate;

public class Helper {
  public String help() {
    Rate r = new Rate();
    return r.rate();
  }

  public static class Inner {
    public void deep() {
    }
  }
}
`,
		// The MIXED-LANGUAGE directory: .java beside .kt.
		"src/mix/Caller.java": `package mix;

import tax.Rate;
import shop.Cart;

public class Caller {
  public void run() {
    Rate r = new Rate();
    r.rate();
    Cart c = new Cart();
    c.checkout();
  }
}
`,
		"src/mix/Helper.kt": `package mix

fun mixHelp(): String {
  val t: String = "mix"
  return t
}
`,
		"src/k/App.kt": `package k

fun run(): String {
  val r: String = "app"
  return r
}

class App {
  fun go() {
  }

  fun tag(s: String) {
  }

  fun pair(m: Map<String, Int>, n: Int) {
  }
}
`,
	}
	rels := make([]string, 0, len(files))
	for rel := range files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(abs, []byte(files[rel]), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}
