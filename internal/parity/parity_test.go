package parity

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/samibel/graphi/internal/corpus"
	"github.com/samibel/graphi/internal/parityreport"
)

// EVERY TEST IN THIS FILE IS HERMETIC. None clones, none reaches the network,
// and none is a substitute for the real-repo matrix. That split is the point,
// and both halves of it matter:
//
//	a hermetic test that clones is not hermetic, and
//	a matrix row that runs on a fixture is not evidence.
//
// These tests prove the HARNESS LOGIC and run in `go run ./cmd/testgate` on
// every PR. The real-repo matrix proves the PRODUCT and runs on
// workflow_dispatch and the nightly schedule only.

// ---------------------------------------------------------------------------
// The instrument boundary — the assertion the whole design rests on.
// ---------------------------------------------------------------------------

// forbiddenDeps are the packages that would make this harness an INSTRUMENT
// rather than an observer. Importing any of them would mean the parity matrix
// runs ingest in the same process it measures — exactly what driving the built
// binary as a subprocess exists to avoid.
//
// core/graphstore is deliberately NOT here. The amended AC-1 permits it for
// opening a store read-only and emitting the snapshot envelope: that is
// in-process COMPARISON, not in-process INGEST, and without it the real FR-7
// assertion is unavailable (no graphi verb emits the envelope, and store FILES
// can never be byte-compared because kv_meta.index.full_ingest_generation is a
// fresh random id on every full pass).
var forbiddenDeps = []string{
	"github.com/samibel/graphi/engine/ingest",
	"github.com/samibel/graphi/engine/watch",
	"github.com/samibel/graphi/engine/link",
	"github.com/samibel/graphi/cmd/eval",
}

// TestNoIngestInProcess is the mechanical guard on that boundary.
//
// IT USES `go list -deps -test`, AND THE -test FLAG IS THE WHOLE POINT. Plain
// `go list -deps` OMITS TEST-ONLY IMPORTS, so a guard written without it can be
// defeated by moving the forbidden import into a _test.go file — the assertion
// would still pass while the harness linked ingest in the very binary that runs
// the matrix's own tests. The check therefore runs over BOTH dependency sets and
// fails if either contains a forbidden package.
func TestNoIngestInProcess(t *testing.T) {
	pkgs := []string{"github.com/samibel/graphi/internal/parity", "github.com/samibel/graphi/cmd/parity"}
	for _, withTest := range []bool{false, true} {
		args := []string{"list", "-deps"}
		if withTest {
			args = append(args, "-test")
		}
		args = append(args, pkgs...)
		out, err := exec.Command("go", args...).Output()
		if err != nil {
			t.Fatalf("go %s: %v", strings.Join(args, " "), err)
		}
		deps := strings.Split(string(out), "\n")
		for _, d := range deps {
			d = strings.TrimSpace(d)
			// `go list -test` renders test variants as "pkg [pkg.test]".
			if i := strings.IndexByte(d, ' '); i > 0 {
				d = d[:i]
			}
			for _, bad := range forbiddenDeps {
				if d == bad || strings.HasPrefix(d, bad+"/") {
					t.Errorf("FORBIDDEN DEPENDENCY (go list -deps%s): %s pulls in %q. "+
						"The parity harness must never run ingest in the process it measures — "+
						"every graph comes from the graphi BINARY as a subprocess.",
						map[bool]string{true: " -test", false: ""}[withTest], pkgs, bad)
				}
			}
		}
	}
}

// TestSnapshotBoundary_OnlyGraphstoreIsImported pins the ONE product package
// this harness may link, so a future "simplification" that reaches for
// engine/query or surfaces/client to answer a graph question is caught here
// rather than in review.
func TestSnapshotBoundary_OnlyGraphstoreIsImported(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/samibel/graphi/internal/parity").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	allowed := map[string]bool{
		"github.com/samibel/graphi/core/graphstore":       true,
		"github.com/samibel/graphi/core/model":            true, // transitive, unavoidable
		"github.com/samibel/graphi/internal/corpus":       true,
		"github.com/samibel/graphi/internal/parityreport": true,
		// SW-158, AC-2: the restart-and-recovery row must exercise the REAL
		// cross-process ingest lock, and internal/ingestlock is the package that
		// exists so an out-of-process diagnostic can probe it without importing
		// the runtime composition root — internal/doctor/indexcheck.go:44 and
		// cmd/graphi/status.go:167 use it for exactly that. It single-sources
		// the lock filename and busy classification, so probing it here cannot
		// drift from the runtime that takes it, and re-deriving either in this
		// package would create the second dialect the package was written to
		// prevent. It runs NO ingest and opens no graph store, so it does not
		// touch the instrument boundary above.
		"github.com/samibel/graphi/internal/ingestlock": true,
	}
	for _, d := range strings.Split(string(out), "\n") {
		d = strings.TrimSpace(d)
		if !strings.HasPrefix(d, "github.com/samibel/graphi/") || allowed[d] {
			continue
		}
		if d == "github.com/samibel/graphi/internal/parity" {
			continue
		}
		t.Errorf("unexpected first-party dependency %q. internal/parity may link core/graphstore "+
			"(open read-only + emit the snapshot envelope) and nothing else from the product tree.", d)
	}
}

// ---------------------------------------------------------------------------
// The class table binds to the declared matrix, in both directions.
// ---------------------------------------------------------------------------

// TestClassTable_BindsToDeclaredMatrix is this harness's drift guard, the same
// bidirectional shape SW-157 established for the hermetic harness and
// `cmd/coverage -check` established for the capability matrix.
//
//	MISSING  every declared, non-deferred change class has a real-repo planner.
//	PHANTOM  every planner's id is a declared class.
//	KIND     no crash condition is counted among the change classes.
func TestClassTable_BindsToDeclaredMatrix(t *testing.T) {
	rows, err := LoadClasses(filepath.Join("..", "..", ClassesPath))
	if err != nil {
		t.Fatalf("LoadClasses: %v", err)
	}
	byID := SpecByID()
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
			t.Fatalf("MISSING: %s declares %d non-deferred change class(es) with no real-repo planner: %s",
				ClassesPath, len(missing), strings.Join(missing, ", "))
		}
	})

	t.Run("PHANTOM", func(t *testing.T) {
		for _, s := range specs() {
			d, ok := declared[s.ID]
			if !ok {
				t.Errorf("PHANTOM: planner %q has no matching id in %s — `id` is a frozen wire identifier", s.ID, ClassesPath)
				continue
			}
			if d.Kind != kindChangeClass {
				t.Errorf("KIND: planner %q is a change class here but %s declares kind %q", s.ID, ClassesPath, d.Kind)
			}
			if d.HarnessRow == harnessDeferred {
				t.Errorf("KIND: planner %q exists but %s defers the class to %s", s.ID, ClassesPath, d.DeferredTo)
			}
		}
	})

	t.Run("COUNT", func(t *testing.T) {
		// 16 = PRD FR-7's 15 change classes + `change_colliding_package_dir`,
		// added 2026-08-16 as the hermetic reproduction of PARITY-002. That one
		// is NOT an FR-7 requirement — its prd_source says "none" — it exists to
		// publish a defect as executable data, and it carries a real-repo
		// planner so it runs on pinned clones too. Derived from `kind`, never
		// from len(rows): counting crash conditions among the change classes is
		// the exact conflation that produced backlog.md:55's "16 change
		// classes", so this number and that mistake are not the same 16.
		if got := CountChangeClasses(rows); got != 16 {
			t.Errorf("CountChangeClasses = %d, want 16", got)
		}
	})
}

// ---------------------------------------------------------------------------
// The planners produce REAL edits, on a fixture that exhibits every class.
// ---------------------------------------------------------------------------

// writeFixtureModule materializes a small but STRUCTURALLY COMPLETE Go module:
// two packages with a cross-package call, an interface with one method plus a
// concrete implementor with two methods, a generated file, a build-tag file and
// external imports. Every planner has a target here, so the table's coverage is
// exercised without a clone.
func writeFixtureModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/fixture\n\ngo 1.21\n",
		"shop/shop.go": "package shop\n\nimport (\n\t\"fmt\"\n\t\"os\"\n\n\t\"example.com/fixture/tax\"\n)\n\n" +
			"// Sink consumes a line.\ntype Sink interface {\n\tWrite(s string) error\n}\n\n" +
			"// Buf implements Sink.\ntype Buf struct{ n int }\n\n" +
			"func (b *Buf) Write(s string) error { b.n += len(s); return nil }\n\n" +
			"func (b *Buf) Len() int { return b.n }\n\n" +
			"func Checkout() int {\n\thelper()\n\treturn tax.Rate()\n}\n\n" +
			"func helper() {\n\tfmt.Println(\"h\")\n}\n\n" +
			"func Env() string {\n\treturn os.Getenv(\"HOME\")\n}\n",
		"shop/extra.go": "package shop\n\nfunc price() int {\n\treturn 3\n}\n\n" +
			"func Total() int {\n\tprice()\n\treturn price() + 1\n}\n",
		"tax/tax.go":  "package tax\n\n// Rate is called across the package boundary.\nfunc Rate() int {\n\treturn 7\n}\n",
		"tax/help.go": "package tax\n\nfunc Helper() int {\n\treturn 1\n}\n",
		"gen/gen.go": "// Code generated by protoc-gen-go. DO NOT EDIT.\n\npackage gen\n\n" +
			"func GetA() string { return \"a\" }\n\nfunc GetB() string { return \"b\" }\n",
		"tagged/tagged.go": "//go:build linux\n\npackage tagged\n\nfunc Tagged() int {\n\treturn 1\n}\n",
	}
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

// TestPlanners_ProduceApplicableRealEdits runs EVERY planner against the
// fixture and requires each to find a real target, describe it, and apply
// cleanly. A planner that silently produced no operations would make its row
// vacuously green on a real repository.
func TestPlanners_ProduceApplicableRealEdits(t *testing.T) {
	for _, s := range specs() {
		s := s
		t.Run(s.ID, func(t *testing.T) {
			root := writeFixtureModule(t)
			m, err := discover(root)
			if err != nil {
				t.Fatalf("discover: %v", err)
			}
			mut, err := s.Plan(m)
			if err != nil {
				t.Fatalf("planner found no target on the structurally complete fixture: %v", err)
			}
			if mut == nil || len(mut.Ops) == 0 {
				t.Fatalf("planner produced no file operations — the row would be vacuous")
			}
			if strings.TrimSpace(mut.Desc) == "" {
				t.Fatalf("planner produced no description; the report must be able to state the real edit")
			}
			before := treeDigest(t, root)
			if err := applyMutation(root, mut); err != nil {
				t.Fatalf("apply: %v", err)
			}
			if after := treeDigest(t, root); after == before {
				t.Fatalf("mutation %q changed nothing on disk", mut.Desc)
			}
		})
	}
}

// TestPlanners_AreDeterministic pins the property AC-17 depends on: two
// dispatches must choose the SAME target, or the harness would manufacture the
// run-to-run disagreement the two-dispatch check exists to detect.
func TestPlanners_AreDeterministic(t *testing.T) {
	for _, s := range specs() {
		s := s
		t.Run(s.ID, func(t *testing.T) {
			var first string
			for i := 0; i < 3; i++ {
				root := writeFixtureModule(t)
				m, err := discover(root)
				if err != nil {
					t.Fatalf("discover: %v", err)
				}
				mut, err := s.Plan(m)
				if err != nil {
					t.Fatalf("plan: %v", err)
				}
				if i == 0 {
					first = mut.Desc
					continue
				}
				if mut.Desc != first {
					t.Fatalf("planner is non-deterministic:\n  run 0: %s\n  run %d: %s", first, i, mut.Desc)
				}
			}
		})
	}
}

// TestPlanner_NoTargetIsSignalled proves the errNoTarget contract: a repository
// that does not exhibit a class must make the planner DECLINE, so repository
// selection can walk on. A planner that invented an edit instead would report a
// class as exercised on a repository that cannot exercise it.
func TestPlanner_NoTargetIsSignalled(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/bare\n\ngo 1.21\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "only.go"), []byte("package bare\n\nfunc A() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := discover(root)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	// This module declares no interface, no generated file and no build tag.
	for _, id := range []string{"change_interface", "add_implementation", "remove_implementation",
		"change_build_tag", "replace_generated_file"} {
		spec := SpecByID()[id]
		if _, err := spec.Plan(m); err != errNoTarget {
			t.Errorf("%s: want errNoTarget on a module that does not exhibit it, got %v", id, err)
		}
	}
}

// TestGeneratedMarker_SurvivesALicenceHeader is a REGRESSION TEST for a real
// harness bug this story hit and had to diagnose from a skipped row.
//
// The first cut scanned only the first 8 lines for the "Code generated … DO NOT
// EDIT." marker. That is where the bare convention puts it, and it is NOT where
// protoc-gen-go puts it: generated gRPC sources open with a ~13-line Apache
// licence header first. Every one of grpc-go's 49 generated files therefore read
// as non-generated, replace_generated_file SKIPPED with "no repository exhibits
// this structure", and the matrix would have published itself as incomplete
// while blaming the corpus for a defect in the harness. A false negative that
// blames the data is the worst kind, so the shape is pinned here.
func TestGeneratedMarker_SurvivesALicenceHeader(t *testing.T) {
	licence := strings.Repeat("// Copyright 2018 gRPC authors. Licensed under the Apache License.\n", 13)
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"bare convention, line 1", "// Code generated by foo. DO NOT EDIT.\n\npackage p\n", true},
		{"behind a licence header", licence + "\n// Code generated by protoc-gen-go. DO NOT EDIT.\n// versions:\n\npackage p\n", true},
		{"ordinary source", "// Package p does things.\n\npackage p\n", false},
		{"marker only AFTER the package clause is not a header marker",
			"package p\n\n// Code generated by hand. DO NOT EDIT.\nfunc f() {}\n", false},
	}
	for _, c := range cases {
		if got := hasGeneratedMarker([]byte(c.src)); got != c.want {
			t.Errorf("%s: hasGeneratedMarker = %v, want %v", c.name, got, c.want)
		}
	}
}

func treeDigest(t *testing.T, root string) string {
	t.Helper()
	var parts []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(root, p)
		parts = append(parts, filepath.ToSlash(rel)+":"+digest(b))
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	sort.Strings(parts)
	return strings.Join(parts, "\n")
}

// ---------------------------------------------------------------------------
// The two PRD §12.3 store-level counts.
// ---------------------------------------------------------------------------

func TestStoreCounts_BiteOnBothConditions(t *testing.T) {
	var g graphPayload
	add := func(id, kind string) {
		g.Nodes = append(g.Nodes, struct {
			ID            string `json:"id"`
			Kind          string `json:"kind"`
			QualifiedName string `json:"qualified_name"`
			SourcePath    string `json:"source_path"`
		}{ID: id, Kind: kind, QualifiedName: id})
	}
	edge := func(id, from, to string) {
		g.Edges = append(g.Edges, struct {
			ID   string `json:"id"`
			From string `json:"from"`
			To   string `json:"to"`
			Kind string `json:"kind"`
		}{ID: id, From: from, To: to, Kind: "calls"})
	}

	// Clean graph: one referenced external node, no dangling edge.
	add("n1", "function")
	add("x1", "external")
	edge("e1", "n1", "x1")
	if sc := storeCounts("r", "c", "full", g); !sc.Pass || sc.OrphanedExternalNodes != 0 || sc.StaleLinkerEdges != 0 {
		t.Fatalf("clean graph must pass, got %+v", sc)
	}

	// An external node nobody references is an orphan the sweep missed.
	add("x2", "external")
	if sc := storeCounts("r", "c", "full", g); sc.OrphanedExternalNodes != 1 || sc.Pass {
		t.Fatalf("orphaned external node not counted: %+v", sc)
	}

	// An edge whose endpoint is not a node is a linker edge that outlived it.
	edge("e2", "n1", "gone")
	sc := storeCounts("r", "c", "full", g)
	if sc.StaleLinkerEdges != 1 || sc.Pass {
		t.Fatalf("stale linker edge not counted: %+v", sc)
	}
	if len(sc.StaleSample) == 0 || len(sc.OrphanSample) == 0 {
		t.Fatalf("a non-zero count must carry a sample so it is actionable: %+v", sc)
	}
	if sc.Side != "full" {
		t.Fatalf("Side must label which graph was counted, got %q", sc.Side)
	}
}

// TestStoreCounts_CoverBothSides is the regression test for review finding
// Major 2. The first cut passed only the rebuild graph to the counter and
// decoded the incremental graph without ever using it, so
// "orphaned external nodes = 0" was undisclosed-ly a statement about one of the
// two graphs the row compares — and the incremental side is the one a parity
// defect actually lands on. Every executed row must now produce a labelled count
// for BOTH sides.
func TestStoreCounts_CoverBothSides(t *testing.T) {
	if testing.Short() {
		t.Skip("drives the built binary")
	}
	bin := buildGraphi(t)
	root := writeFixtureModule(t)
	rows := []ClassRow{
		{ID: "add_file", Kind: kindChangeClass, Label: "add file", HarnessRow: "required"},
		{ID: "delete_file", Kind: kindChangeClass, Label: "delete file", HarnessRow: "required", KnownDefect: "PARITY-001"},
	}
	m := corpus.Manifest{Entries: []corpus.Entry{{
		Name: "fixture", Path: root, Tier: 1, Language: "go",
		Searches: []corpus.Search{{Query: "Checkout", ExpectNonEmpty: true}},
	}}}
	r := &Runner{
		Binary: bin, WorkDir: t.TempDir(), MaxTier: 1, AllowLocal: true,
		PerClassTimeout: 3 * time.Minute, RunnerClass: "test",
	}
	prov := parityreport.NewProvenance("test-sha")
	prov.WorktreeClean, prov.ProductDiffEmpty = true, true
	rep, err := r.Run(context.Background(), m, rows, prov)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	sides := map[string]map[string]bool{}
	for _, sc := range rep.StoreCounts {
		if sc.Side == "" {
			t.Errorf("store count for %s carries no Side label: %+v", sc.Class, sc)
		}
		if sides[sc.Class] == nil {
			sides[sc.Class] = map[string]bool{}
		}
		sides[sc.Class][sc.Side] = true
	}
	if len(sides) == 0 {
		t.Fatal("no store counts were taken at all")
	}
	for class, got := range sides {
		if !got["full"] || !got["incremental"] {
			t.Errorf("class %s counted sides %v; both \"full\" and \"incremental\" are required", class, got)
		}
	}
}

// ---------------------------------------------------------------------------
// Fail-closed publication.
// ---------------------------------------------------------------------------

// TestReport_FailsClosed walks every refusal AC-11 names. Each must REFUSE, not
// warn: a report that publishes itself with a caveat is a report whose caveat
// nobody reads.
func TestReport_FailsClosed(t *testing.T) {
	clean := func() parityreport.Report {
		p := parityreport.NewProvenance("deadbeef")
		p.WorktreeClean = true
		p.ProductDiffEmpty = true
		p.RunnerClass = "ubuntu-latest"
		r := parityreport.Report{Provenance: p}
		for i := 0; i < 15; i++ {
			r.Classes = append(r.Classes, parityreport.ClassResult{
				ID: "c" + string(rune('a'+i)), Kind: parityreport.KindChangeClass, Verdict: parityreport.VerdictPass})
		}
		// The two crash conditions are rows of the matrix too (SW-158), and
		// Finalize scores them on their own count. A helper that emitted only
		// change classes would make every subtest below run against a matrix
		// that is incomplete for an unrelated reason.
		for _, id := range []string{"interrupted_full_pass", "restart_and_recovery"} {
			r.Classes = append(r.Classes, parityreport.ClassResult{
				ID: id, Kind: parityreport.KindCrashCondition, Verdict: parityreport.VerdictPass})
		}
		return r
	}

	t.Run("clean run publishes", func(t *testing.T) {
		r := clean()
		r.Finalize(15, 2)
		if !r.Publishable || r.Outcome != parityreport.OutcomePass {
			t.Fatalf("clean run must publish as PASS: %+v", r.NotPublishableBecause)
		}
	})

	t.Run("dirty worktree refuses", func(t *testing.T) {
		r := clean()
		r.Provenance.WorktreeClean = false
		r.Provenance.WorktreeDirtyDetail = " M engine/ingest/ingest.go"
		r.Finalize(15, 2)
		if r.Publishable {
			t.Fatal("a dirty worktree must refuse publication")
		}
	})

	t.Run("product diff refuses", func(t *testing.T) {
		r := clean()
		r.Provenance.ProductDiffEmpty = false
		r.Provenance.ProductDiffDetail = "engine/ingest/ingest.go | 2 +-"
		r.Finalize(15, 2)
		if r.Publishable {
			t.Fatal("a non-empty product diff against the candidate must refuse publication")
		}
	})

	t.Run("missing runner class refuses", func(t *testing.T) {
		r := clean()
		r.Provenance.RunnerClass = ""
		r.Finalize(15, 2)
		if r.Publishable {
			t.Fatal("an unattributed run must refuse publication")
		}
	})

	t.Run("incomplete run refuses and is not a pass", func(t *testing.T) {
		r := clean()
		r.Classes[3].Verdict = parityreport.VerdictSkipped
		r.Finalize(15, 2)
		if r.Publishable {
			t.Fatal("a run with a skipped row must refuse publication")
		}
		if r.Outcome == parityreport.OutcomePass {
			t.Fatal("a skipped row must never read as a pass")
		}
	})

	t.Run("a FAIL is blocking but still publishable evidence", func(t *testing.T) {
		r := clean()
		r.Classes[2].Verdict = parityreport.VerdictFail
		r.Finalize(15, 2)
		if r.Outcome != parityreport.OutcomeFail {
			t.Fatalf("every mismatch is blocking: outcome = %s", r.Outcome)
		}
		if !r.Publishable {
			t.Fatal("a FAIL is legitimate published evidence, not a reason to withhold the report")
		}
	})

	t.Run("a non-zero §12.3 count fails the run", func(t *testing.T) {
		r := clean()
		r.StoreCounts = append(r.StoreCounts, parityreport.StoreCounts{
			Repo: "cobra", Class: "delete_file", OrphanedExternalNodes: 2, Pass: false})
		r.Finalize(15, 2)
		if r.Outcome != parityreport.OutcomeFail {
			t.Fatalf("orphaned external nodes must fail the run: %s", r.Outcome)
		}
	})

	t.Run("a shortened class list cannot self-declare complete", func(t *testing.T) {
		r := clean()
		r.Classes = append(r.Classes[:10:10], r.Classes[15:]...)
		r.Finalize(15, 2)
		if r.Complete || r.Publishable {
			t.Fatal("ten decided rows must not satisfy a fifteen-class matrix")
		}
	})

	t.Run("a truncated matrix cannot certify itself", func(t *testing.T) {
		// The hole this closes: passing the number of rows one happened to run
		// as the declared total would make any subset "complete".
		r := clean()
		r.Classes = append(r.Classes[:2:2], r.Classes[15:]...)
		r.Finalize(2, 2)
		if r.Complete || r.Publishable {
			t.Fatal("two decided rows declared as a two-class matrix must still be incomplete: FR-7 declares 15")
		}
	})

	t.Run("a moved upstream pin refuses", func(t *testing.T) {
		r := clean()
		r.Repos = append(r.Repos, parityreport.RepoRef{
			Name: "cobra", PinnedSHA: "a0a6ae020bb3", HeadSHA: "ffffffffffffffff"})
		r.Finalize(15, 2)
		if r.Publishable {
			t.Fatal("a manifest pin mismatch must refuse publication")
		}
	})
}

// TestProvenance_NeverClaimsItRanAtTheCandidate pins the sentence AC-12 governs.
func TestProvenance_NeverClaimsItRanAtTheCandidate(t *testing.T) {
	p := parityreport.NewProvenance("cafebabe")
	if !strings.Contains(p.Statement, "byte-identical to v0.7.1 at "+parityreport.CandidateSHA) {
		t.Fatalf("statement must say the product SOURCE is byte-identical: %q", p.Statement)
	}
	for _, bad := range []string{"measured at the candidate", "ran at the candidate", "at v0.7.1"} {
		if strings.Contains(strings.ToLower(p.Statement), bad) {
			t.Fatalf("statement implies the run happened AT the candidate: %q", p.Statement)
		}
	}
	if p.RunSHA == p.CandidateSHA {
		t.Fatal("run sha and candidate sha must be recorded separately")
	}
}

// TestVerdictSet_ComparesVerdictsNotBytes pins AC-17's comparison unit.
func TestVerdictSet_ComparesVerdictsNotBytes(t *testing.T) {
	mk := func(when, v string) parityreport.Report {
		p := parityreport.NewProvenance("sha")
		p.GeneratedAt = when
		return parityreport.Report{Provenance: p, Classes: []parityreport.ClassResult{
			{ID: "add_file", Kind: "change_class", Verdict: parityreport.VerdictPass, DurationMS: 10},
			{ID: "delete_file", Kind: "change_class", Verdict: v, DurationMS: 99},
		}}
	}
	a := mk("2026-07-30T00:00:00Z", parityreport.VerdictFail)
	b := mk("2026-07-31T11:22:33Z", parityreport.VerdictFail)
	if a.VerdictSetDigest() != b.VerdictSetDigest() {
		t.Fatal("two dispatches differing only in timestamps and durations must agree")
	}
	c := mk("2026-07-31T11:22:33Z", parityreport.VerdictPass)
	if a.VerdictSetDigest() == c.VerdictSetDigest() {
		t.Fatal("a changed verdict must show as a disagreement")
	}
}

// ---------------------------------------------------------------------------
// Tier discipline.
// ---------------------------------------------------------------------------

// TestTier4IsExcludedByConstruction proves AC-7's "by construction, not by
// configuration". kubernetes is SW-145's subject and needs a named machine; no
// flag value may pull it in.
func TestTier4IsExcludedByConstruction(t *testing.T) {
	for _, in := range []int{4, 5, 99} {
		if got := clampTier(in); got != MaxSupportedTier {
			t.Errorf("clampTier(%d) = %d, want %d — tier 4 must be unreachable", in, got, MaxSupportedTier)
		}
	}
	m, err := corpus.LoadManifest(filepath.Join("..", "..", "corpus", "manifest.json"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	for _, e := range goRepos(m, clampTier(99), false) {
		if e.Name == "kubernetes" || e.Tier > MaxSupportedTier {
			t.Fatalf("tier-4 entry %q reached the candidate pool", e.Name)
		}
	}
}

// TestRepoOrder_PrefersTierThenSize pins the selection walk AC-6 describes.
func TestRepoOrder_PrefersTierThenSize(t *testing.T) {
	m, err := corpus.LoadManifest(filepath.Join("..", "..", "corpus", "manifest.json"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	got := goRepos(m, 3, false)
	if len(got) == 0 {
		t.Fatal("no Go repositories in the candidate pool")
	}
	if got[0].Name != "cobra" {
		t.Errorf("first candidate = %q, want cobra (the only tier-1 real repository)", got[0].Name)
	}
	for i := 1; i < len(got); i++ {
		a, b := got[i-1], got[i]
		if a.Tier > b.Tier {
			t.Errorf("tier order violated at %d: %s(t%d) before %s(t%d)", i, a.Name, a.Tier, b.Name, b.Tier)
		}
		if a.Tier == b.Tier && goFileCount(a) > goFileCount(b) {
			t.Errorf("size order violated at %d: %s(%d) before %s(%d)", i,
				a.Name, goFileCount(a), b.Name, goFileCount(b))
		}
	}
}

// TestManifestDeclaresTheTwoPinnedProperties proves the manifest still says what
// the two manifest-pinned classes rely on. If the corpus is re-stratified, the
// harness must fail here rather than silently re-point a class.
func TestManifestDeclaresTheTwoPinnedProperties(t *testing.T) {
	m, err := corpus.LoadManifest(filepath.Join("..", "..", "corpus", "manifest.json"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	strat := stratification(m)
	for _, s := range specs() {
		if s.ManifestProperty == "" {
			continue
		}
		repo, ok := strat[s.ManifestProperty]
		if !ok {
			t.Errorf("class %q needs manifest property %q, which corpus/manifest.json no longer declares",
				s.ID, s.ManifestProperty)
			continue
		}
		t.Logf("%-24s -> %-8s (manifest property %q)", s.ID, repo, s.ManifestProperty)
	}
	if strat["build tags"] != "gin" {
		t.Errorf("build tags -> %q, want gin", strat["build tags"])
	}
	if strat["generated code"] != "grpc-go" {
		t.Errorf("generated code -> %q, want grpc-go", strat["generated code"])
	}
}

// ---------------------------------------------------------------------------
// End to end, hermetically: the real binary, a local fixture, no network.
// ---------------------------------------------------------------------------

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

// TestRunner_EndToEndOnALocalFixture drives the REAL binary through the whole
// row: baseline rebuild, real edit, incremental sync, fresh full rebuild, and
// snapshot-byte comparison — with no network at all.
//
// It also proves the harness is CAPABLE OF DISTINGUISHING pass from fail, which
// a green-only test cannot: add_file must converge, while delete_file is the
// PARITY-001 shape and is expected to diverge. Asserting both directions is what
// stops a harness that always reports PASS from looking correct.
func TestRunner_EndToEndOnALocalFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("drives the built binary")
	}
	bin := buildGraphi(t)
	root := writeFixtureModule(t)

	rows := []ClassRow{
		{ID: "add_file", Kind: kindChangeClass, Label: "add file", HarnessRow: "required"},
		{ID: "delete_file", Kind: kindChangeClass, Label: "delete file", HarnessRow: "required", KnownDefect: "PARITY-001"},
	}
	m := corpus.Manifest{Entries: []corpus.Entry{{
		Name: "fixture", Path: root, Tier: 1, Language: "go",
		Searches: []corpus.Search{{Query: "Checkout", ExpectNonEmpty: true}},
	}}}

	r := &Runner{
		Binary: bin, WorkDir: t.TempDir(), MaxTier: 1, AllowLocal: true,
		PerClassTimeout: 3 * time.Minute, RunnerClass: "test",
		Log: func(f string, a ...any) { t.Logf(f, a...) },
	}
	prov := parityreport.NewProvenance("test-sha")
	prov.WorktreeClean, prov.ProductDiffEmpty = true, true

	rep, err := r.Run(context.Background(), m, rows, prov)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := rep.VerdictSet()
	for _, c := range rep.Classes {
		t.Logf("%-14s %-8s repo=%s inc=%d/%d full=%d/%d\n    %s\n    %s",
			c.ID, c.Verdict, c.Repo, c.IncNodes, c.IncEdges, c.FullNodes, c.FullEdges, c.Mutation, c.Detail)
	}
	if got["add_file"] != parityreport.VerdictPass {
		t.Errorf("add_file = %s, want PASS — adding a file must converge", got["add_file"])
	}
	// PARITY-001 IS FIXED (2026-08-16): the incremental path now runs the
	// deleted-path purge AND COMMITS IT before linkFiles, so the linker cannot
	// resolve a call into a node the pass is about to delete. This assertion used
	// to PIN the defect as data (want FAIL); it now asserts the CONVERGENCE, and
	// it is the independent confirmation that matters most — unlike the hermetic
	// engine/conformance row, this drives the BUILT BINARY end to end through the
	// real Runner, so it proves the shipped path converges, not just the library.
	// A regression here means the ordering was reintroduced in engine/ingest.
	if got["delete_file"] != parityreport.VerdictPass {
		t.Errorf("delete_file = %s, want PASS. PARITY-001 was fixed by committing the deleted-path "+
			"purge before linkFiles (engine/ingest, see the PARITY-001 FIX comment); a FAIL here "+
			"means that ordering was reintroduced and `graphi sync` diverges permanently from "+
			"`graphi rebuild` when a file declaring a cross-package callee is deleted.", got["delete_file"])
	}
	if rep.Outcome != parityreport.OutcomeIncomplete {
		t.Errorf("a two-row run over a fifteen-class matrix must be INCOMPLETE, got %s", rep.Outcome)
	}
	if rep.Publishable {
		t.Error("a partial run must never be publishable")
	}
	if len(rep.StoreCounts) == 0 {
		t.Error("the §12.3 store-level counts were not taken")
	}
}

// TestRunner_LocalFixturesAreRefusedByDefault proves the real-repo matrix cannot
// quietly run on a fixture. AllowLocal is the test-only door, and it is shut by
// default.
func TestRunner_LocalFixturesAreRefusedByDefault(t *testing.T) {
	m, err := corpus.LoadManifest(filepath.Join("..", "..", "corpus", "manifest.json"))
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	for _, e := range goRepos(m, 3, false) {
		if e.URL == "" {
			t.Errorf("local fixture %q reached the real-repo candidate pool — a matrix row that "+
				"runs on a fixture is not evidence", e.Name)
		}
	}
	local := 0
	for _, e := range goRepos(m, 3, true) {
		if e.URL == "" {
			local++
		}
	}
	if local == 0 {
		t.Error("AllowLocal must admit the tier-1 local fixtures, or the hermetic tests have no repository")
	}
}
