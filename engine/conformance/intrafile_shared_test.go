package conformance_test

// SW-199 (W5.m) — the shared runner and shared guards for the six languages of
// the intra/parse residual: css, hcl, json, markdown, toml, yaml (five at
// `intra-file-only`, json at `parse-only`).
//
// WHY A SHARED RUNNER AND NOT SIX COPIES. The cross-file-heuristic families
// (bash, lua, php, rust, c#, c/c++) each copied runBashChangeClassRow because
// each needed a family-specific setup step. These six need none: no binder, no
// ambient lookup directory, no resolver configuration — the change is applied,
// the tree is parsed, and the graph is compared. Six identical copies would be
// six places for the assertion set to drift apart, which is the same argument
// parity_matrix_directions_test.go:18-25 makes about hardcoded file lists.
//
// WHAT THE RUNNER ASSERTS, in the order it asserts it. This is the AC-4 shape
// and it is deliberately STRONGER than the change-class runners the
// cross-file-heuristic families use:
//
//	(A) PARSE DETERMINISM — the property AC-4 names. Two INDEPENDENT full
//	    passes over the identical bytes on the identical (store, profile) axis
//	    must serialize to identical snapshot bytes. This is "same input bytes ->
//	    same AST" asserted at BYTE level on the AST's serialization, per the
//	    story's test note, not at node-identity level.
//	(B) FULL-VS-INCREMENTAL PARITY — the assertion the shared harness gives for
//	    free, kept because dropping it would make these six tables weaker than
//	    every other family's for no reason. It is INTRA-FILE parity only: no row
//	    in any of the six tables applies a cross-file change, because none of the
//	    six languages defines a cross-file construct graphi extracts (see each
//	    file's `go_class_applicability:` block, where every cross-file Go class is
//	    dispositioned `not_applicable` with a language-spec reason).
//	(C) NON-VACUITY — the row's witness, run against the incremental graph,
//	    exactly as every other family's runner does it.
//
// The 5x byte-stability of AC-4 is delivered by `-count=5` over the whole
// driver, which is how every other family's byte-stability claim is delivered;
// (A) is what makes a single run already meaningful.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// runIntraFileParityRow is the shared row runner for the six intra/parse
// residual languages. `base` is the language's base tree; `row` is one row of
// the language's table.
func runIntraFileParityRow(t *testing.T, b parityBackend, pr parityProfile, base map[string]string, row changeClassRow) {
	t.Helper()
	axis := b.name + "/" + pr.name
	if row.apply == nil || row.witness == nil {
		t.Fatalf("class %q has no apply/witness", row.id)
	}
	ctx := context.Background()
	root := t.TempDir()
	f := newFixture(t, root)

	seed := map[string]string{}
	for rel, content := range base {
		seed[rel] = content
	}
	for rel, content := range row.seed {
		seed[rel] = content
	}

	incStore := newBackendStore(t, b)
	buildIncrementalParallel(t, root, incStore, pr.p, []func(){
		func() { writeTree(t, root, seed) },
		func() { row.apply(f) },
	})

	fullStore := newBackendStore(t, b)
	if err := newIngester(t, fullStore, pr.p).IngestAll(ctx, root); err != nil {
		t.Fatalf("[%s/%s] full IngestAll: %v", axis, row.id, err)
	}

	// (A) The parse-determinism pass: a SECOND independent full ingest of the
	// SAME bytes on the SAME axis. A store, an ingester and a temp dir of its
	// own, so nothing but the input bytes is shared with the first pass.
	repeatStore := newBackendStore(t, b)
	if err := newIngester(t, repeatStore, pr.p).IngestAll(ctx, root); err != nil {
		t.Fatalf("[%s/%s] repeat IngestAll: %v", axis, row.id, err)
	}

	incSnap := snapshot(t, incStore)
	fullSnap := snapshot(t, fullStore)
	repeatSnap := snapshot(t, repeatStore)

	// (C) Non-vacuity first and unconditionally, against the incremental graph.
	g, err := newGraphView(ctx, incStore)
	if err != nil {
		t.Fatalf("[%s/%s] read incremental graph: %v", axis, row.id, err)
	}
	if err := row.witness(g); err != nil {
		t.Errorf("[%s/%s] VACUOUS ROW: witness did not hold, so `apply` did not produce the claimed shape: %v", axis, row.id, err)
	}

	// (A) reported before (B): a determinism failure explains a parity failure,
	// never the other way round.
	if string(fullSnap) != string(repeatSnap) {
		t.Errorf("[%s/%s] PARSE-DETERMINISM FAIL: two independent full passes over the identical bytes did not serialize identically.\nclass: %s\n%s",
			axis, row.id, row.description,
			snapshotDiff(t, "full-pass-1", fullSnap, "full-pass-2", repeatSnap))
	}

	// (B) The intra-file parity assertion: snapshot bytes, nothing weaker.
	if string(incSnap) != string(fullSnap) {
		t.Errorf("[%s/%s] PARITY FAIL: incremental != full snapshot bytes.\nclass: %s\nchange set: %v\n%s",
			axis, row.id, row.description, f.changeSet(),
			snapshotDiff(t, "incremental", incSnap, "full", fullSnap))
	}
}

// requireNoNodeFrom is the PARSE-ONLY abstention predicate: it fails if ANY
// node in the graph was extracted from the named source file. json is the one
// parse-only language of the six — core/parse/parser_json.go:25-29 states that
// JSONParser wires no SymbolExtractor — so the honest json witness is "this
// file contributed nothing", and this is the predicate that says so. It is
// paired in every json row with a control assertion over a sibling file, so the
// row cannot pass over an empty graph.
func requireNoNodeFrom(g *graphView, sourcePath string) error {
	for _, n := range g.nodes {
		if n.SourcePath() == sourcePath {
			return fmt.Errorf("parse-only abstention broken: node %q (%s) was extracted from %s; the language mints no symbols, so nothing may come from that file",
				n.QualifiedName(), n.Kind(), sourcePath)
		}
	}
	return nil
}

// requireFileNode fails unless a `file` node exists for the given path. File
// nodes are deliberately kept out of graphView.byQN (changeclass_test.go:108-115)
// so requirePresent cannot see them, and the intra-file-only languages need the
// file node itself as an anchor.
func requireFileNode(g *graphView, path string) error {
	for _, n := range g.nodes {
		if n.Kind() == "file" && n.QualifiedName() == path {
			return nil
		}
	}
	return fmt.Errorf("file node %q absent; graph has %s", path, g.qnList())
}

// requireNoFileNode fails if a `file` node still exists for the given path —
// the delete_file counterpart of requireFileNode.
func requireNoFileNode(g *graphView, path string) error {
	for _, n := range g.nodes {
		if n.Kind() == "file" && n.QualifiedName() == path {
			return fmt.Errorf("file node %q still present after delete", path)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// The applicability disposition — G3's second half, as DATA
// ---------------------------------------------------------------------------
//
// The G3 threshold every one of the six evidence rows publishes
// (docs/rc/evidence-index.yaml, GA-LANG-<lang>-G3) is not only the byte-parity
// table: it is "PLUS an explicit applicability disposition per Go class — every
// cross-file change class dispositioned as `not_applicable` with a language-spec
// reason, so an inapplicable class is data the drift guard can see, not prose in
// a note." The language-GA program states the same at
// docs/plan/2026-08-graphi-p2-language-ga-program-v1.md:153-155, and fixes the
// vocabulary: `applicable` / `adapted` / `not_applicable` + reason.
//
// So the disposition is a SECOND top-level key in each per-language
// parity-class YAML, `go_class_applicability:`, and the guard below is what
// makes it data rather than prose. `deferred` is the fourth member this story
// adds, and it is added rather than avoided BECAUSE the alternative is a false
// disposition: the four language-agnostic ingest classes (branch_switch,
// replace_generated_file, and the two crash conditions) are neither
// inapplicable to these languages nor witnessed by these tables, and claiming
// either would be untrue. A `deferred` disposition must name a deferred_to, in
// the same shape harness_row: "deferred" must (paritymatrix_test.go's DEFERRED
// direction).
//
// NAMING: the failure messages below are prefixed APPLICABILITY-MISSING /
// APPLICABILITY-PHANTOM rather than MISSING / PHANTOM ON PURPOSE. The
// seven-direction guard (parity_matrix_directions_test.go:196-215) recognizes a
// direction by the FIRST WORD of a t.Errorf format string, so a bare "MISSING"
// here would let a family file satisfy the MISSING direction WITHOUT a drift
// guard. The prefix keeps this test additive to the seven and incapable of
// standing in for any of them.

// goClassApplicabilityRow is one entry of a per-language
// `go_class_applicability:` block.
type goClassApplicabilityRow struct {
	GoClass     string `yaml:"go_class"`
	Disposition string `yaml:"disposition"`
	WitnessedBy string `yaml:"witnessed_by"`
	DeferredTo  string `yaml:"deferred_to"`
	Reason      string `yaml:"reason"`
}

const (
	dispositionApplicable    = "applicable"
	dispositionAdapted       = "adapted"
	dispositionNotApplicable = "not_applicable"
	dispositionDeferred      = "deferred"
)

var legalDispositions = []string{
	dispositionApplicable,
	dispositionAdapted,
	dispositionNotApplicable,
	dispositionDeferred,
}

// loadGoClassApplicability parses the `go_class_applicability:` block of a
// per-language parity-class YAML.
func loadGoClassApplicability(t *testing.T, path string) []goClassApplicabilityRow {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		GoClassApplicability []goClassApplicabilityRow `yaml:"go_class_applicability"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(doc.GoClassApplicability) == 0 {
		t.Fatalf("%s declared no go_class_applicability rows; the G3 threshold requires an explicit disposition per Go class", path)
	}
	return doc.GoClassApplicability
}

// assertGoClassApplicability is the shared applicability guard. It binds a
// per-language YAML's `go_class_applicability:` block to the Go change-class
// matrix (docs/rc/parity-classes.yaml) in both directions, and pins the
// disposition vocabulary and the obligations each disposition carries.
func assertGoClassApplicability(t *testing.T, lang, path string, declared []parityRow) {
	t.Helper()

	goClasses := map[string]bool{}
	for _, r := range loadParityClasses(t) {
		goClasses[r.ID] = true
	}
	if len(goClasses) == 0 {
		t.Fatalf("%s declared no rows to disposition against", parityClassesPath)
	}

	rowIDs := map[string]bool{}
	for _, r := range declared {
		rowIDs[r.ID] = true
	}

	seen := map[string]bool{}
	for _, d := range loadGoClassApplicability(t, path) {
		if seen[d.GoClass] {
			t.Errorf("APPLICABILITY-DUPLICATE: %s disposition %s declares go_class %q twice", lang, path, d.GoClass)
			continue
		}
		seen[d.GoClass] = true

		if !goClasses[d.GoClass] {
			t.Errorf("APPLICABILITY-PHANTOM: %s dispositions go_class %q, which %s does not declare",
				lang, d.GoClass, parityClassesPath)
			continue
		}
		if !oneOfString(d.Disposition, legalDispositions) {
			t.Errorf("APPLICABILITY-VOCABULARY: %s go_class %q has disposition %q, outside %v",
				lang, d.GoClass, d.Disposition, legalDispositions)
			continue
		}
		if strings.TrimSpace(d.Reason) == "" {
			t.Errorf("APPLICABILITY-REASON: %s go_class %q is dispositioned %q with an empty reason; a disposition without a reason is prose-free but evidence-free too",
				lang, d.GoClass, d.Disposition)
		}
		switch d.Disposition {
		case dispositionApplicable, dispositionAdapted:
			if d.WitnessedBy == "" {
				t.Errorf("APPLICABILITY-WITNESS: %s go_class %q is dispositioned %q but names no witnessed_by row",
					lang, d.GoClass, d.Disposition)
			} else if !rowIDs[d.WitnessedBy] {
				t.Errorf("APPLICABILITY-WITNESS: %s go_class %q is witnessed_by %q, which is not a parity_classes row id in %s",
					lang, d.GoClass, d.WitnessedBy, path)
			}
			if d.DeferredTo != "" {
				t.Errorf("APPLICABILITY-WITNESS: %s go_class %q is dispositioned %q yet names deferred_to %q",
					lang, d.GoClass, d.Disposition, d.DeferredTo)
			}
		case dispositionNotApplicable:
			if d.WitnessedBy != "" {
				t.Errorf("APPLICABILITY-WITNESS: %s go_class %q is not_applicable yet names witnessed_by %q — an inapplicable class cannot have a witness",
					lang, d.GoClass, d.WitnessedBy)
			}
			if d.DeferredTo != "" {
				t.Errorf("APPLICABILITY-WITNESS: %s go_class %q is not_applicable yet names deferred_to %q — an inapplicable class is not owed to anybody",
					lang, d.GoClass, d.DeferredTo)
			}
		case dispositionDeferred:
			if d.WitnessedBy != "" {
				t.Errorf("APPLICABILITY-WITNESS: %s go_class %q is deferred yet names witnessed_by %q",
					lang, d.GoClass, d.WitnessedBy)
			}
			if d.DeferredTo == "" {
				t.Errorf("APPLICABILITY-WITNESS: %s go_class %q is deferred with no deferred_to owner", lang, d.GoClass)
			}
		}
	}

	var undispositioned []string
	for id := range goClasses {
		if !seen[id] {
			undispositioned = append(undispositioned, id)
		}
	}
	sort.Strings(undispositioned)
	if len(undispositioned) > 0 {
		t.Errorf("APPLICABILITY-MISSING: %s leaves %v undispositioned in %s; the G3 threshold requires an explicit disposition per Go class",
			lang, undispositioned, path)
	}
}

// oneOfString is the closed-set membership helper the per-family Vocabulary
// guards each define inline; the applicability guard shares one copy.
func oneOfString(v string, legal []string) bool {
	for _, l := range legal {
		if v == l {
			return true
		}
	}
	return false
}
