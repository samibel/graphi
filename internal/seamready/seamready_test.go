package seamready

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/divergence"
	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/mcp"
)

// ---------------------------------------------------------------------------
// AC-3 — the verdict rule, exhaustively.
// ---------------------------------------------------------------------------

// TestAX14_VerdictTable enumerates every combination of the three states over
// six criteria (3^6 = 729) and checks the reduction against the rule stated in
// AC-3: READY iff all PASS; NOT_READY iff any FAIL; otherwise UNKNOWN. It is a
// table by construction rather than by hand so no combination is left to
// "obviously the same".
func TestAX14_VerdictTable(t *testing.T) {
	states := []State{StatePass, StateFail, StateUnknown}
	var walk func(prefix []State)
	total := 0
	walk = func(prefix []State) {
		if len(prefix) == len(Criteria) {
			total++
			got := Reduce(prefix)
			anyFail, allPass := false, true
			for _, s := range prefix {
				if s == StateFail {
					anyFail = true
				}
				if s != StatePass {
					allPass = false
				}
			}
			want := VerdictUnknown
			switch {
			case anyFail:
				want = VerdictNotReady
			case allPass:
				want = VerdictReady
			}
			if got != want {
				t.Errorf("Reduce(%v) = %s, want %s", prefix, got, want)
			}
			return
		}
		for _, s := range states {
			walk(append(append([]State(nil), prefix...), s))
		}
	}
	walk(nil)
	if total != 729 {
		t.Fatalf("enumerated %d combinations, want 729", total)
	}
}

// TestAX14_KillTest_OneUnknownKeepsTheVerdictOffReady is AC-3's discriminating
// case: five PASS rows and a single UNKNOWN must NOT reduce to READY, and
// neither rendering may show the operation as READY or the row as PASS.
func TestAX14_KillTest_OneUnknownKeepsTheVerdictOffReady(t *testing.T) {
	for i := range Criteria {
		states := make([]State, len(Criteria))
		for j := range states {
			states[j] = StatePass
		}
		states[i] = StateUnknown
		if got := Reduce(states); got == VerdictReady {
			t.Fatalf("five PASS + UNKNOWN at %s reduced to READY", Criteria[i].ID)
		} else if got != VerdictUnknown {
			t.Fatalf("five PASS + UNKNOWN at %s reduced to %s, want UNKNOWN", Criteria[i].ID, got)
		}
	}

	// Render the concrete case through both renderers and read them the way a
	// human or a script would.
	a := Assessment{Operations: []OperationAssessment{{
		Operation: "dead_code",
		Criteria: func() []Row {
			rows := make([]Row, len(Criteria))
			for i, c := range Criteria {
				rows[i] = Row{ID: c.ID, Name: c.Name, State: StatePass, Evidence: "x"}
			}
			rows[1].State = StateUnknown
			rows[1].Evidence = ""
			rows[1].Reason = "K unset"
			return rows
		}(),
	}}}
	a.Operations[0].Verdict = Reduce(a.Operations[0].states())

	text := a.Text()
	if !strings.Contains(text, "dead_code  UNKNOWN\n") {
		t.Errorf("text render does not carry the verdict line `dead_code  UNKNOWN`:\n%s", text)
	}
	if strings.Contains(text, "dead_code  READY") {
		t.Errorf("text render shows the operation READY with an UNKNOWN row:\n%s", text)
	}

	var doc struct {
		Schema     string `json:"schema"`
		Operations []struct {
			Operation string `json:"operation"`
			Verdict   string `json:"verdict"`
			Criteria  []struct {
				ID    string `json:"id"`
				State string `json:"state"`
			} `json:"criteria"`
		} `json:"operations"`
	}
	raw, err := a.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("json render is not valid json: %v\n%s", err, raw)
	}
	if doc.Schema != SchemaV1 {
		t.Errorf("schema = %q, want %q", doc.Schema, SchemaV1)
	}
	if len(doc.Operations) != 1 || doc.Operations[0].Verdict != "UNKNOWN" {
		t.Fatalf("json verdict = %+v, want UNKNOWN", doc.Operations)
	}
	if doc.Operations[0].Criteria[1].State != "UNKNOWN" {
		t.Errorf("json state of the unknown row = %q", doc.Operations[0].Criteria[1].State)
	}
}

// TestAX14_NoCriteriaIsNotReady pins the empty case: a verdict over nothing
// must not be READY. "All of zero criteria pass" is vacuously true and exactly
// the laundering the rule exists to refuse.
func TestAX14_NoCriteriaIsNotReady(t *testing.T) {
	if got := Reduce(nil); got != VerdictUnknown {
		t.Fatalf("Reduce(nil) = %s, want UNKNOWN", got)
	}
	short := []State{StatePass, StatePass}
	if got := Reduce(short); got != VerdictUnknown {
		t.Fatalf("Reduce over %d of %d criteria = %s, want UNKNOWN", len(short), len(Criteria), got)
	}
}

// ---------------------------------------------------------------------------
// Declaration parsing — fail closed.
// ---------------------------------------------------------------------------

const minimalDecl = `
schema: seam-readiness-v1
k: null
operations:
  - operation: dead_code
    criteria:
      c1: {release_tags: [v0.11.0]}
`

func TestAX14_ParseAcceptsTheMinimalDeclaration(t *testing.T) {
	d, err := ParseDeclaration([]byte(minimalDecl))
	if err != nil {
		t.Fatal(err)
	}
	if d.K != nil {
		t.Fatalf("k = %v, want unset", *d.K)
	}
	if len(d.Operations) != 1 || d.Operations[0].Operation != "dead_code" {
		t.Fatalf("operations = %+v", d.Operations)
	}
}

func TestAX14_ParseFailsClosed(t *testing.T) {
	cases := map[string]string{
		"unknown criterion id": `
schema: seam-readiness-v1
k: null
operations:
  - operation: dead_code
    criteria:
      c7: {release_tags: [v0.11.0]}
`,
		"unknown top-level field": `
schema: seam-readiness-v1
k: null
ready: true
operations: []
`,
		"unknown artifact field": `
schema: seam-readiness-v1
k: null
operations:
  - operation: dead_code
    criteria:
      c1: {verdict: PASS}
`,
		"wrong schema": `
schema: seam-readiness-v2
k: null
operations: []
`,
		"explained mismatch without a reason": `
schema: seam-readiness-v1
k: 30
operations:
  - operation: dead_code
    criteria:
      c2: {explained_mismatches: [{kind: result, count: 1}]}
`,
		"explained mismatch with a zero count": `
schema: seam-readiness-v1
k: 30
operations:
  - operation: dead_code
    criteria:
      c2: {explained_mismatches: [{kind: result, count: 0, reason: because}]}
`,
		"duplicate operation": `
schema: seam-readiness-v1
k: null
operations:
  - operation: dead_code
    criteria: {}
  - operation: dead_code
    criteria: {}
`,
		"empty operation name": `
schema: seam-readiness-v1
k: null
operations:
  - operation: ""
    criteria: {}
`,
		"negative k": `
schema: seam-readiness-v1
k: -1
operations: []
`,
		"zero k": `
schema: seam-readiness-v1
k: 0
operations: []
`,
		"test symbol without a file": `
schema: seam-readiness-v1
k: null
operations:
  - operation: dead_code
    criteria:
      c3: {tests: [{symbol: TestX}]}
`,
	}
	for name, src := range cases {
		if _, err := ParseDeclaration([]byte(src)); err == nil {
			t.Errorf("%s: parsed without error; the declaration must fail closed", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Evaluation — fakes for the declared and machine sources.
// ---------------------------------------------------------------------------

type fakeGit struct {
	tags    map[string]string // tag -> canary.go content at that tag
	commits map[string]bool
}

func (g fakeGit) TagExists(tag string) (bool, error) { _, ok := g.tags[tag]; return ok, nil }
func (g fakeGit) FileAtTag(tag, path string) ([]byte, error) {
	c, ok := g.tags[tag]
	if !ok {
		return nil, errors.New("no such tag")
	}
	return []byte(c), nil
}
func (g fakeGit) CommitExists(sha string) (bool, error) { return g.commits[sha], nil }

// shadowCanaryAt is the shape of surfaces/client/canary.go the c1 check reads:
// the shipped default and the migrated list, one id per line.
func shadowCanaryAt(ops ...string) string {
	var b strings.Builder
	b.WriteString("package client\n\nvar migratedOperations = []string{\n")
	for _, op := range ops {
		b.WriteString("\t\"" + op + "\",\n")
	}
	b.WriteString("}\n\nconst canaryModeDefault = CanaryModeShadow\n")
	return b.String()
}

func symbolsIn(present map[string][]string) SymbolLookup {
	return func(file, symbol string) (bool, error) {
		for _, s := range present[file] {
			if s == symbol {
				return true, nil
			}
		}
		return false, nil
	}
}

func intp(v int) *int { return &v }

func baseSources() Sources {
	return Sources{
		Migrated: []string{"dead_code", "repo_overview"},
		Record: divergence.Document{
			Schema: divergence.Schema,
			State:  divergence.StateUnknown,
			Operations: []divergence.OperationView{
				{OperationRecord: divergence.OperationRecord{Operation: "dead_code"}, State: divergence.StateUnknown},
				{OperationRecord: divergence.OperationRecord{Operation: "repo_overview"}, State: divergence.StateUnknown},
			},
		},
		Git: fakeGit{
			tags:    map[string]string{"v0.11.0": shadowCanaryAt("dead_code", "repo_overview")},
			commits: map[string]bool{"4f14966": true},
		},
		Symbols: symbolsIn(map[string][]string{
			"surfaces/client/executor_argument_fidelity_test.go": {"TestExecutor_ArgumentMutationsMoveTheParity"},
			"surfaces/client/executor_parity_test.go":            {"TestExecutor_AdapterByteParity"},
		}),
		EnvVarFor:      func(op string) string { return "GRAPHI_CANARY_" + strings.ToUpper(op) },
		LegacyAccepted: func() error { return nil },
	}
}

// fullDecl declares every artifact for dead_code so that, with K set and a
// clean record, the operation reads READY — the only way to know the PASS
// branches are reachable at all.
const fullDecl = `
schema: seam-readiness-v1
k: 30
operations:
  - operation: dead_code
    criteria:
      c1: {release_tags: [v0.11.0]}
      c2: {}
      c3: {tests: [{file: surfaces/client/executor_argument_fidelity_test.go, symbol: TestExecutor_ArgumentMutationsMoveTheParity}]}
      c4: {workflow: test-gate, run_id: "1", sha: 4f14966}
      c5: {tests: [{file: surfaces/client/executor_parity_test.go, symbol: TestExecutor_AdapterByteParity}]}
      c6: {workflow: executor-rollback, run_id: "2", sha: 4f14966}
`

func evalDecl(t *testing.T, decl string, src Sources) Assessment {
	t.Helper()
	d, err := ParseDeclaration([]byte(decl))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Evaluate(d, src)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func rowOf(t *testing.T, a Assessment, op, id string) Row {
	t.Helper()
	for _, o := range a.Operations {
		if o.Operation != op {
			continue
		}
		for _, r := range o.Criteria {
			if r.ID == id {
				return r
			}
		}
	}
	t.Fatalf("no row %s for %s in %+v", id, op, a.Operations)
	return Row{}
}

func verdictOf(t *testing.T, a Assessment, op string) Verdict {
	t.Helper()
	for _, o := range a.Operations {
		if o.Operation == op {
			return o.Verdict
		}
	}
	t.Fatalf("no operation %s in %+v", op, a.Operations)
	return ""
}

// TestAX14_ReadyIsReachable proves the PASS branches exist: K set, 30 clean
// observations, every artifact declared and present → READY.
func TestAX14_ReadyIsReachable(t *testing.T) {
	src := baseSources()
	src.Record.Operations[0].Observations = 30
	src.Record.Operations[0].State = divergence.StateAgreed
	a := evalDecl(t, fullDecl, src)
	if got := verdictOf(t, a, "dead_code"); got != VerdictReady {
		t.Fatalf("dead_code = %s, want READY:\n%s", got, a.Text())
	}
	for _, r := range a.Operations[0].Criteria {
		if r.State != StatePass {
			t.Errorf("%s %s = %s (%s)", r.ID, r.Name, r.State, r.Reason)
		}
	}
	// AC-4: a set K prints the rule-of-three bound beside it.
	if !strings.Contains(a.Text(), "K = 30") || !strings.Contains(a.Text(), "3/30") {
		t.Errorf("text does not print K and its 3/K bound:\n%s", a.Text())
	}
}

// TestAX14_MissingEntryReadsUnknown: an operation on the seam with no yaml
// entry at all is still listed, and every declared-source row reads UNKNOWN.
func TestAX14_MissingEntryReadsUnknown(t *testing.T) {
	a := evalDecl(t, fullDecl, baseSources())
	if got := verdictOf(t, a, "repo_overview"); got != VerdictUnknown {
		t.Fatalf("repo_overview (undeclared) = %s, want UNKNOWN", got)
	}
	for _, id := range []string{"c1", "c3", "c4", "c5"} {
		if r := rowOf(t, a, "repo_overview", id); r.State != StateUnknown {
			t.Errorf("%s of an undeclared operation = %s, want UNKNOWN", id, r.State)
		}
	}
}

// TestAX14_MissingSymbolReadsUnknownNotPass: a declared test that is not in the
// tree is UNKNOWN. Not FAIL — the tree says nothing about the property — and
// never PASS, which is what a declaration-only check would have produced.
func TestAX14_MissingSymbolReadsUnknownNotPass(t *testing.T) {
	src := baseSources()
	src.Symbols = symbolsIn(map[string][]string{})
	a := evalDecl(t, fullDecl, src)
	for _, id := range []string{"c3", "c5"} {
		r := rowOf(t, a, "dead_code", id)
		if r.State != StateUnknown {
			t.Errorf("%s with a missing symbol = %s, want UNKNOWN", id, r.State)
		}
		if !strings.Contains(r.Reason, "not found") {
			t.Errorf("%s reason does not say the symbol is missing: %q", id, r.Reason)
		}
	}
	if got := verdictOf(t, a, "dead_code"); got == VerdictReady {
		t.Fatal("READY with a missing symbol")
	}
}

// TestAX14_SymbolLookupReadsTheRealTree exercises the go/parser lookup on a
// temp file: a top-level Test function is found, a method of the same name is
// not, and an absent file is "not found", not an error.
func TestAX14_SymbolLookupReadsTheRealTree(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "package pkg\n\nimport \"testing\"\n\nfunc TestReal(t *testing.T) {}\n\ntype x struct{}\n\nfunc (x) TestMethod(t *testing.T) {}\n"
	if err := os.WriteFile(filepath.Join(root, "pkg", "a_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	look := TreeSymbols(root)
	if ok, err := look("pkg/a_test.go", "TestReal"); err != nil || !ok {
		t.Errorf("TestReal: ok=%v err=%v, want found", ok, err)
	}
	if ok, err := look("pkg/a_test.go", "TestMethod"); err != nil || ok {
		t.Errorf("TestMethod (a method): ok=%v err=%v, want not found", ok, err)
	}
	if ok, err := look("pkg/a_test.go", "TestMissing"); err != nil || ok {
		t.Errorf("TestMissing: ok=%v err=%v, want not found", ok, err)
	}
	if ok, err := look("pkg/nope_test.go", "TestReal"); err != nil || ok {
		t.Errorf("absent file: ok=%v err=%v, want not found without error", ok, err)
	}
}

// ---------------------------------------------------------------------------
// c2 — the divergence criterion, and AC-4.
// ---------------------------------------------------------------------------

func TestAX14_KUnsetReadsC2Unknown(t *testing.T) {
	src := baseSources()
	src.Record.Operations[0].Observations = 1000 // plenty — irrelevant while K is unset
	src.Record.Operations[0].State = divergence.StateAgreed
	decl := strings.Replace(fullDecl, "k: 30", "k: null", 1)
	a := evalDecl(t, decl, src)
	if a.K != nil {
		t.Fatalf("K = %v, want unset", *a.K)
	}
	r := rowOf(t, a, "dead_code", "c2")
	if r.State != StateUnknown {
		t.Fatalf("c2 with K unset = %s, want UNKNOWN", r.State)
	}
	if !strings.Contains(r.Reason, "K unset") {
		t.Errorf("c2 reason does not name K: %q", r.Reason)
	}
	if got := verdictOf(t, a, "dead_code"); got != VerdictUnknown {
		t.Fatalf("verdict with K unset = %s, want UNKNOWN", got)
	}
	if !strings.Contains(a.Text(), "K unset — owner decision 1 not taken") {
		t.Errorf("text does not print the AC-4 sentence:\n%s", a.Text())
	}
}

func TestAX14_UnexplainedMismatchFails(t *testing.T) {
	src := baseSources()
	src.Record.Operations[0].Observations = 30
	src.Record.Operations[0].Mismatches = 1
	src.Record.Operations[0].State = divergence.StateDiverged
	a := evalDecl(t, fullDecl, src)
	if r := rowOf(t, a, "dead_code", "c2"); r.State != StateFail {
		t.Fatalf("c2 with an unexplained mismatch = %s, want FAIL (%s)", r.State, r.Reason)
	}
	if got := verdictOf(t, a, "dead_code"); got != VerdictNotReady {
		t.Fatalf("verdict = %s, want NOT_READY", got)
	}

	// A mismatch is a FAIL even while K is unset: the record is decisive
	// about disagreement regardless of how many agreements were wanted.
	decl := strings.Replace(fullDecl, "k: 30", "k: null", 1)
	a = evalDecl(t, decl, src)
	if r := rowOf(t, a, "dead_code", "c2"); r.State != StateFail {
		t.Fatalf("c2 mismatch with K unset = %s, want FAIL", r.State)
	}
}

func TestAX14_ExplainedMismatchIsSubtracted(t *testing.T) {
	src := baseSources()
	src.Record.Operations[0].Observations = 30
	src.Record.Operations[0].Mismatches = 1
	src.Record.Operations[0].State = divergence.StateDiverged
	decl := strings.Replace(fullDecl, "c2: {}",
		"c2: {explained_mismatches: [{kind: result, count: 1, reason: 'legacy emits a trailing newline the executor trims; SW-0 fixed it'}]}", 1)
	a := evalDecl(t, decl, src)
	r := rowOf(t, a, "dead_code", "c2")
	if r.State != StatePass {
		t.Fatalf("c2 with the one mismatch explained = %s, want PASS (%s)", r.State, r.Reason)
	}
	if !strings.Contains(r.Evidence, "1 explained") {
		t.Errorf("c2 evidence does not disclose the explained mismatch: %q", r.Evidence)
	}

	// Explaining MORE than was recorded is a declaration the record
	// contradicts: UNKNOWN, never PASS.
	src.Record.Operations[0].Mismatches = 0
	src.Record.Operations[0].State = divergence.StateAgreed
	a = evalDecl(t, decl, src)
	if r := rowOf(t, a, "dead_code", "c2"); r.State != StateUnknown {
		t.Fatalf("c2 with an over-explained record = %s, want UNKNOWN", r.State)
	}
}

func TestAX14_TooFewObservationsIsUnknownAndSkippedIsDisclosed(t *testing.T) {
	src := baseSources()
	src.Record.Operations[0].Observations = 29
	src.Record.Operations[0].Skipped = 4
	src.Record.Operations[0].State = divergence.StateAgreed
	a := evalDecl(t, fullDecl, src)
	r := rowOf(t, a, "dead_code", "c2")
	if r.State != StateUnknown {
		t.Fatalf("c2 at 29/30 = %s, want UNKNOWN", r.State)
	}
	if !strings.Contains(r.Evidence, "skipped 4") {
		t.Errorf("skipped count is not disclosed on the row: %q", r.Evidence)
	}
	src.Record.Operations[0].Observations = 30
	a = evalDecl(t, fullDecl, src)
	r = rowOf(t, a, "dead_code", "c2")
	if r.State != StatePass {
		t.Fatalf("c2 at 30/30 with skips = %s, want PASS — skipped is disclosed, not judged", r.State)
	}
	if !strings.Contains(r.Evidence, "skipped 4") {
		t.Errorf("skipped count is not disclosed on the PASS row: %q", r.Evidence)
	}
}

func TestAX14_UnreadableRecordIsUnknown(t *testing.T) {
	src := baseSources()
	src.Record.Operations[0].Observations = 30
	src.Record.Operations[0].State = divergence.StateAgreed
	src.RecordErr = errors.New("divergence: read: permission denied")
	a := evalDecl(t, fullDecl, src)
	if r := rowOf(t, a, "dead_code", "c2"); r.State != StateUnknown {
		t.Fatalf("c2 with an unreadable record = %s, want UNKNOWN", r.State)
	}
}

// ---------------------------------------------------------------------------
// c1 — release line; c4/c6 — declared run ids; c6 — the code half.
// ---------------------------------------------------------------------------

func TestAX14_ReleaseLineNeedsAnExistingTagWithTheOpOnTheSeam(t *testing.T) {
	// Tag does not exist → UNKNOWN (the declaration cannot be confirmed).
	src := baseSources()
	src.Git = fakeGit{tags: map[string]string{}, commits: map[string]bool{"4f14966": true}}
	a := evalDecl(t, fullDecl, src)
	if r := rowOf(t, a, "dead_code", "c1"); r.State != StateUnknown {
		t.Fatalf("c1 with a missing tag = %s, want UNKNOWN (%s)", r.State, r.Reason)
	}
	// Tag exists but the op was NOT on the seam there → FAIL: a positive
	// finding that the declaration is wrong.
	src.Git = fakeGit{tags: map[string]string{"v0.11.0": shadowCanaryAt("repo_overview")}, commits: map[string]bool{"4f14966": true}}
	a = evalDecl(t, fullDecl, src)
	if r := rowOf(t, a, "dead_code", "c1"); r.State != StateFail {
		t.Fatalf("c1 with the op absent at the tag = %s, want FAIL (%s)", r.State, r.Reason)
	}
	// Tag exists, op listed, but the default at that tag was legacy → FAIL.
	legacy := strings.Replace(shadowCanaryAt("dead_code"), "CanaryModeShadow", "CanaryModeLegacy", 1)
	src.Git = fakeGit{tags: map[string]string{"v0.11.0": legacy}, commits: map[string]bool{"4f14966": true}}
	a = evalDecl(t, fullDecl, src)
	if r := rowOf(t, a, "dead_code", "c1"); r.State != StateFail {
		t.Fatalf("c1 with a legacy default at the tag = %s, want FAIL (%s)", r.State, r.Reason)
	}
	// Not inside a git checkout → UNKNOWN.
	src.Git = nil
	a = evalDecl(t, fullDecl, src)
	if r := rowOf(t, a, "dead_code", "c1"); r.State != StateUnknown {
		t.Fatalf("c1 outside a checkout = %s, want UNKNOWN", r.State)
	}
}

func TestAX14_DeclaredRunNeedsIdAndKnownSha(t *testing.T) {
	src := baseSources()
	// Unknown sha → UNKNOWN.
	decl := strings.Replace(fullDecl, "run_id: \"1\", sha: 4f14966", "run_id: \"1\", sha: deadbeef", 1)
	a := evalDecl(t, decl, src)
	if r := rowOf(t, a, "dead_code", "c4"); r.State != StateUnknown {
		t.Fatalf("c4 with an unknown sha = %s, want UNKNOWN (%s)", r.State, r.Reason)
	}
	// Missing run id → UNKNOWN.
	decl = strings.Replace(fullDecl, "run_id: \"2\", sha: 4f14966", "sha: 4f14966", 1)
	a = evalDecl(t, decl, src)
	if r := rowOf(t, a, "dead_code", "c6"); r.State != StateUnknown {
		t.Fatalf("c6 with no run id = %s, want UNKNOWN (%s)", r.State, r.Reason)
	}
}

func TestAX14_RollbackCodeChecksFail(t *testing.T) {
	src := baseSources()
	src.LegacyAccepted = func() error { return errors.New("legacy is not a position") }
	a := evalDecl(t, fullDecl, src)
	if r := rowOf(t, a, "dead_code", "c6"); r.State != StateFail {
		t.Fatalf("c6 with legacy rejected = %s, want FAIL (%s)", r.State, r.Reason)
	}
	src = baseSources()
	src.EnvVarFor = func(string) string { return "" }
	a = evalDecl(t, fullDecl, src)
	if r := rowOf(t, a, "dead_code", "c6"); r.State != StateFail {
		t.Fatalf("c6 with no env variable = %s, want FAIL (%s)", r.State, r.Reason)
	}
	src = baseSources()
	src.EnvVarFor = func(op string) string { return "graphi canary " + op }
	a = evalDecl(t, fullDecl, src)
	if r := rowOf(t, a, "dead_code", "c6"); r.State != StateFail {
		t.Fatalf("c6 with an unusable env name = %s, want FAIL (%s)", r.State, r.Reason)
	}
}

// ---------------------------------------------------------------------------
// AC-1 — canonical order; AC-8 — Stable operations appear nowhere.
// ---------------------------------------------------------------------------

func TestAX14_OperationOrderIsCanonicalNotDeclarationOrder(t *testing.T) {
	decl := `
schema: seam-readiness-v1
k: null
operations:
  - operation: repo_overview
    criteria: {}
  - operation: dead_code
    criteria: {}
`
	src := baseSources()
	src.Migrated = []string{"dead_code", "repo_overview"}
	a := evalDecl(t, decl, src)
	if len(a.Operations) != 2 || a.Operations[0].Operation != "dead_code" || a.Operations[1].Operation != "repo_overview" {
		t.Fatalf("order = %v, want the migrated order [dead_code repo_overview]", a.Operations)
	}
	for _, o := range a.Operations {
		if len(o.Criteria) != len(Criteria) {
			t.Fatalf("%s has %d rows, want %d", o.Operation, len(o.Criteria), len(Criteria))
		}
		for i, r := range o.Criteria {
			if r.ID != Criteria[i].ID {
				t.Errorf("%s row %d is %s, want %s", o.Operation, i, r.ID, Criteria[i].ID)
			}
		}
	}
}

func TestAX14_AnOperationOffTheSeamIsRejected(t *testing.T) {
	decl := `
schema: seam-readiness-v1
k: null
operations:
  - operation: search
    criteria: {}
`
	d, err := ParseDeclaration([]byte(decl))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Evaluate(d, baseSources())
	if err == nil {
		t.Fatal("a declaration naming an operation that is not on the seam was accepted")
	}
	// The message is what a declaration author debugs against: it must name
	// the flag the file came in through, not the default path — which may
	// not be the file they edited.
	if !strings.Contains(err.Error(), "-declaration") || strings.Contains(err.Error(), DeclarationPath) {
		t.Fatalf("off-seam rejection should name the -declaration flag, not %s: %v", DeclarationPath, err)
	}
}

// TestAX14_UnhandledCriterionReadsUnknownNotBlank: a criterion id with no
// evaluator reads UNKNOWN with a reason, never a blank state. Checked once on
// the dispatcher directly and once end-to-end with Criteria widened by a
// seventh entry, so the text render is seen to say why.
func TestAX14_UnhandledCriterionReadsUnknownNotBlank(t *testing.T) {
	src := baseSources()
	r := evalCriterion(Criterion{ID: "c9", Name: "unhandled"}, "dead_code", nil, Artifact{}, divergence.OperationView{}, src)
	if r.State != StateUnknown || r.Reason == "" {
		t.Fatalf("unhandled criterion = %q (%q), want UNKNOWN with a reason", r.State, r.Reason)
	}

	saved := Criteria
	Criteria = append(append([]Criterion{}, saved...), Criterion{ID: "c9", Name: "unhandled"})
	defer func() { Criteria = saved }()
	src.Record.Operations[0].Observations = 30
	src.Record.Operations[0].State = divergence.StateAgreed
	a := evalDecl(t, fullDecl, src) // every c1..c6 PASS for dead_code
	row := rowOf(t, a, "dead_code", "c9")
	if row.State != StateUnknown || !strings.Contains(row.Reason, "no evaluator") {
		t.Fatalf("c9 = %q (%q), want UNKNOWN / no evaluator", row.State, row.Reason)
	}
	if got := verdictOf(t, a, "dead_code"); got != VerdictUnknown {
		t.Fatalf("verdict with an unhandled criterion = %s, want UNKNOWN", got)
	}
	if !strings.Contains(a.Text(), "c9  unhandled") || !strings.Contains(a.Text(), "UNKNOWN  — no evaluator for criterion c9") {
		t.Fatalf("text does not render the unhandled row as UNKNOWN with its reason:\n%s", a.Text())
	}
}

// TestAX14_RuleOfThreeBelowThreeBoundsNothing: 3/K is a probability, so for
// K < 3 the sentence must not print a rate above 100 %% as if it were one.
func TestAX14_RuleOfThreeBelowThreeBoundsNothing(t *testing.T) {
	for _, k := range []int{1, 2} {
		s := RuleOfThree(k)
		if !strings.Contains(s, "3/"+strconv.Itoa(k)) || !strings.Contains(s, "bounds nothing") {
			t.Errorf("RuleOfThree(%d) = %q, want the 3/K form and a no-bound note", k, s)
		}
		if strings.Contains(s, "300.0%") || strings.Contains(s, "150.0%") {
			t.Errorf("RuleOfThree(%d) prints a rate above 100%%: %q", k, s)
		}
	}
	if s := RuleOfThree(3); !strings.Contains(s, "3/3 = 100.0%") {
		t.Errorf("RuleOfThree(3) = %q", s)
	}
	if s := RuleOfThree(30); !strings.Contains(s, "3/30 = 10.0%") {
		t.Errorf("RuleOfThree(30) = %q", s)
	}
}

// ---------------------------------------------------------------------------
// The shipped declaration against the live tree — AC-4, AC-7, AC-8.
// ---------------------------------------------------------------------------

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the package directory")
		}
		dir = parent
	}
}

func shippedAssessment(t *testing.T) (Declaration, Assessment) {
	t.Helper()
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, DeclarationPath))
	if err != nil {
		t.Fatal(err)
	}
	d, err := ParseDeclaration(raw)
	if err != nil {
		t.Fatalf("the shipped declaration does not parse: %v", err)
	}
	// An empty state directory: the record every fresh install has. The
	// developer's own ~/.graphi is deliberately NOT read here — a test must
	// not depend on what the machine running it happened to observe.
	src := LiveSources(root, t.TempDir())
	a, err := Evaluate(d, src)
	if err != nil {
		t.Fatal(err)
	}
	return d, a
}

// TestAX14_ShippedDeclarationCoversTheSeamExactly: the yaml names every
// migrated operation and nothing else — a new migration has to bring its entry.
func TestAX14_ShippedDeclarationCoversTheSeamExactly(t *testing.T) {
	d, _ := shippedAssessment(t)
	declared := map[string]bool{}
	for _, o := range d.Operations {
		declared[o.Operation] = true
	}
	for _, op := range client.MigratedOperations() {
		if !declared[op] {
			t.Errorf("migrated operation %q has no entry in %s", op, DeclarationPath)
		}
	}
	if len(declared) != len(client.MigratedOperations()) {
		t.Errorf("%s declares %d operations, the seam has %d", DeclarationPath, len(declared), len(client.MigratedOperations()))
	}
}

// TestAX14_TodayEveryOperationIsUnknown is AC-4 and AC-7 as a test: K is
// unset, so c2 is UNKNOWN for every operation and no verdict is READY.
func TestAX14_TodayEveryOperationIsUnknown(t *testing.T) {
	d, a := shippedAssessment(t)
	if d.K != nil {
		t.Fatalf("the shipped declaration sets k=%d; owner decision 1 is not this story's to take", *d.K)
	}
	if len(a.Operations) != len(client.MigratedOperations()) {
		t.Fatalf("%d operations assessed, want %d", len(a.Operations), len(client.MigratedOperations()))
	}
	for _, o := range a.Operations {
		if o.Verdict != VerdictUnknown {
			t.Errorf("%s = %s, want UNKNOWN", o.Operation, o.Verdict)
		}
		if r := rowOf(t, a, o.Operation, "c2"); r.State != StateUnknown {
			t.Errorf("%s c2 = %s, want UNKNOWN while K is unset", o.Operation, r.State)
		}
	}
	if a.Record.Observations != 0 || a.Record.Mismatches != 0 {
		t.Fatalf("an empty state directory read %d observation(s), %d mismatch(es)", a.Record.Observations, a.Record.Mismatches)
	}
	if !strings.Contains(a.Text(), "K unset — owner decision 1 not taken") {
		t.Errorf("text does not carry the AC-4 sentence:\n%s", a.Text())
	}
}

// TestAX14_ShippedC4IsUndeclaredWhilePreconditionDIsUnknown pins the
// round-2 correction: the performance budget is on record as UNKNOWN and
// blocking (stories/SW-238/preconditions.md §(d)), so the shipped declaration
// carries no run for c4 and every c4 row reads UNKNOWN. Declaring one again
// means editing this test deliberately, the way a flip story edits its pin.
func TestAX14_ShippedC4IsUndeclaredWhilePreconditionDIsUnknown(t *testing.T) {
	d, a := shippedAssessment(t)
	for _, o := range d.Operations {
		if art, declared := o.Criteria["c4"]; declared && (art.Workflow != "" || art.RunID != "" || art.SHA != "") {
			t.Errorf("%s declares a c4 run (%s %s @ %s) while precondition (d) is on record as UNKNOWN", o.Operation, art.Workflow, art.RunID, art.SHA)
		}
	}
	for _, o := range a.Operations {
		if r := rowOf(t, a, o.Operation, "c4"); r.State != StateUnknown || r.Reason != "no CI run declared" {
			t.Errorf("%s c4 = %s (%q), want UNKNOWN / no CI run declared", o.Operation, r.State, r.Reason)
		}
	}
}

// TestAX14_StableOperationsAppearNowhere: no Stable operation is on the seam,
// so none may appear in the declaration or in either rendering (AC-8).
func TestAX14_StableOperationsAppearNowhere(t *testing.T) {
	d, a := shippedAssessment(t)
	text := a.Text()
	raw, err := a.JSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range d.Operations {
		if mcp.IsStableOperation(o.Operation) {
			t.Errorf("Stable operation %q is declared in %s", o.Operation, DeclarationPath)
		}
	}
	for _, o := range a.Operations {
		if mcp.IsStableOperation(o.Operation) {
			t.Errorf("Stable operation %q was assessed", o.Operation)
		}
	}
	for _, name := range mcp.StableOperations {
		for _, body := range []string{text, string(raw)} {
			for _, line := range strings.Split(body, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), name+" ") || strings.Contains(line, "\""+name+"\"") {
					t.Errorf("Stable operation %q appears in the output: %q", name, line)
				}
			}
		}
	}
}

// TestAX14_TextRenderHasOneVerdictLineAndSixRowsPerOperation pins the AC-1
// shape a reader greps for.
func TestAX14_TextRenderHasOneVerdictLineAndSixRowsPerOperation(t *testing.T) {
	_, a := shippedAssessment(t)
	text := a.Text()
	for _, op := range client.MigratedOperations() {
		if n := strings.Count(text, "\n"+op+"  "); n != 1 {
			t.Errorf("%q has %d verdict lines, want 1", op, n)
		}
	}
	for _, c := range Criteria {
		if n := strings.Count(text, "  "+c.ID+"  "); n != len(client.MigratedOperations()) {
			t.Errorf("criterion %s has %d rows, want %d", c.ID, n, len(client.MigratedOperations()))
		}
	}
}
