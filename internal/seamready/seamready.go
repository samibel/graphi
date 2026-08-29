// Package seamready is the SW-254 (AX-14) cutover-readiness assessment for
// graphi's executor seam: per operation in client.MigratedOperations(), it
// evaluates the six criteria a flip to `active` is gated on and prints
// READY, NOT_READY or UNKNOWN.
//
// # Why this is computed and not argued
//
// Ten Labs operations dual-run on the seam in `shadow`. Whether any of them may
// become `active` was, before this package, answered by reading
// stories/SW-238/preconditions.md, `graphi doctor -divergence --json`,
// internal/seamreach/reachability.txt, three test files and a CI leg, and
// holding them in one's head. Nothing computed the answer, so the answer was
// whatever the last reader remembered. This package is that reading, done
// mechanically, with the same honesty rule the evidence index applies: a
// criterion is PASS only when a named, re-runnable artifact backs it; a
// declaration the tree cannot confirm reads UNKNOWN; and UNKNOWN is never PASS
// and never READY.
//
// # The three states and the verdict
//
//	PASS      a named artifact exists and says so
//	FAIL      a positive finding against the criterion (a recorded mismatch, an
//	          operation that was not on the seam at the tag it claims, a kill
//	          switch that does not accept `legacy`)
//	UNKNOWN   nothing decides it: no declaration, a declaration the tree cannot
//	          confirm, an unset threshold, too few observations, no checkout
//
//	READY     iff all six PASS
//	NOT_READY iff any FAIL
//	UNKNOWN   otherwise
//
// The rules are the amended precondition (a) in stories/SW-238/preconditions.md
// and its siblings (b)..(e), reduced to what a machine can read. They are NOT a
// new gate vocabulary: `K`, "explained mismatch", the run ids and the test names
// are the words those preconditions already use.
//
// # What it does not do
//
// It flips nothing. It is unranked tooling like internal/seamreach — not
// imported by the product (cionly_test.go asserts it) — and it makes no
// network call: a CI run id is DECLARED in docs/rc/seam-readiness.yaml with the
// sha it ran at, and the tool checks the sha is a commit the checkout knows,
// never that the run exists. The day it prints READY for an operation, the
// flip is its own story.
package seamready

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/samibel/graphi/internal/divergence"
)

// SchemaV1 is the declaration's and the JSON output's schema identifier.
const SchemaV1 = "seam-readiness-v1"

// DeclarationPath is where the declared artifacts live, relative to the
// repository root — which is where `go run ./cmd/seamready` is invoked from.
const DeclarationPath = "docs/rc/seam-readiness.yaml"

// State is one criterion's outcome.
type State string

const (
	StatePass    State = "PASS"
	StateFail    State = "FAIL"
	StateUnknown State = "UNKNOWN"
)

// Verdict is one operation's outcome.
type Verdict string

const (
	VerdictReady    Verdict = "READY"
	VerdictNotReady Verdict = "NOT_READY"
	VerdictUnknown  Verdict = "UNKNOWN"
)

// Criterion is one of the six cutover criteria, in canonical order.
type Criterion struct {
	ID   string
	Name string
}

// Criteria is the closed, ordered set. Every operation gets exactly these
// rows, in this order, so the output is diffable across runs and operations.
var Criteria = []Criterion{
	{ID: "c1", Name: "shadow-release-line"},
	{ID: "c2", Name: "divergence"},
	{ID: "c3", Name: "argument-fidelity"},
	{ID: "c4", Name: "performance-budget"},
	{ID: "c5", Name: "capability-provenance-parity"},
	{ID: "c6", Name: "rollback"},
}

// criterionIndex is Criteria as a lookup, for declaration validation.
var criterionIndex = func() map[string]int {
	m := make(map[string]int, len(Criteria))
	for i, c := range Criteria {
		m[c.ID] = i
	}
	return m
}()

// Row is one criterion's answer for one operation.
type Row struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// State is PASS, FAIL or UNKNOWN.
	State State `json:"state"`
	// Evidence names what the state rests on: tags, symbols, run ids, counts.
	// Empty when nothing was found.
	Evidence string `json:"evidence"`
	// Reason says why the state is not PASS, in one sentence; empty on PASS.
	Reason string `json:"reason"`
}

// OperationAssessment is one operation's verdict and its six rows.
type OperationAssessment struct {
	Operation string  `json:"operation"`
	Verdict   Verdict `json:"verdict"`
	Criteria  []Row   `json:"criteria"`
}

func (o OperationAssessment) states() []State {
	out := make([]State, len(o.Criteria))
	for i, r := range o.Criteria {
		out[i] = r.State
	}
	return out
}

// RecordSummary is what the tool read from the divergence record, echoed so a
// verification record can quote the input beside the output.
type RecordSummary struct {
	State        string `json:"state"`
	Directory    string `json:"directory"`
	Observations int    `json:"observations"`
	Mismatches   int    `json:"mismatches"`
	Skipped      int    `json:"skipped"`
	Unreadable   int    `json:"unreadable_segments"`
	Pruned       int    `json:"pruned_segments"`
	// Error is set when the record could not be read; every c2 row is then
	// UNKNOWN and this is why.
	Error string `json:"error,omitempty"`
}

// Assessment is the tool's whole output.
type Assessment struct {
	// K is the observation threshold precondition (a) needs, or nil while owner
	// decision 1 is not taken.
	K          *int                  `json:"k"`
	Record     RecordSummary         `json:"record"`
	Operations []OperationAssessment `json:"operations"`
}

// Reduce turns six states into a verdict. It is the AC-3 rule and nothing
// else, kept as a pure function so the table test can exhaust it.
//
// A slice shorter than Criteria is UNKNOWN even when every element is PASS:
// "all of the criteria I looked at pass" is not "all six pass".
func Reduce(states []State) Verdict {
	if len(states) != len(Criteria) {
		return VerdictUnknown
	}
	anyFail, allPass := false, true
	for _, s := range states {
		switch s {
		case StateFail:
			anyFail = true
			allPass = false
		case StatePass:
		default:
			allPass = false
		}
	}
	switch {
	case anyFail:
		return VerdictNotReady
	case allPass:
		return VerdictReady
	}
	return VerdictUnknown
}

// Git is the read-only view of the checkout the tool needs. A nil Git means
// the tool is not running inside a checkout, and every git-backed criterion
// reads UNKNOWN.
type Git interface {
	TagExists(tag string) (bool, error)
	FileAtTag(tag, path string) ([]byte, error)
	CommitExists(sha string) (bool, error)
}

// SymbolLookup reports whether a top-level function named symbol exists in
// the Go file at the repository-relative path. A missing file is (false, nil).
type SymbolLookup func(file, symbol string) (bool, error)

// Sources is everything Evaluate reads. It is a value so a test can hand in a
// hypothetical record, tree and checkout — which is how the PASS and FAIL
// branches are proven reachable at all.
type Sources struct {
	// Migrated is the canonical operation order — client.MigratedOperations().
	Migrated []string
	// Record is the assessed divergence document; RecordErr is set when it
	// could not be read.
	Record    divergence.Document
	RecordErr error
	// Git is nil outside a checkout.
	Git Git
	// Symbols is nil when the tree is not available.
	Symbols SymbolLookup
	// EnvVarFor names the kill-switch variable for an operation
	// (cmd/internal/runtime.EnvCanaryModeFor).
	EnvVarFor func(operation string) string
	// LegacyAccepted returns the error client.ParseCanaryMode("legacy") returns.
	LegacyAccepted func() error
}

// Evaluate assesses every migrated operation against the declaration. It
// returns an error only for a declaration that must not be evaluated at all —
// one naming an operation that is not on the seam, which is how a Stable
// operation would get into the output.
func Evaluate(d Declaration, src Sources) (Assessment, error) {
	onSeam := make(map[string]bool, len(src.Migrated))
	for _, op := range src.Migrated {
		onSeam[op] = true
	}
	declared := make(map[string]OperationDeclaration, len(d.Operations))
	for _, o := range d.Operations {
		if !onSeam[o.Operation] {
			return Assessment{}, fmt.Errorf("seamready: %s declares %q, which is not on the executor seam (migrated: %v)",
				DeclarationPath, o.Operation, src.Migrated)
		}
		declared[o.Operation] = o
	}

	a := Assessment{K: d.K, Record: summarize(src)}
	views := make(map[string]divergence.OperationView, len(src.Record.Operations))
	for _, v := range src.Record.Operations {
		views[v.Operation] = v
	}
	// Canonical order: the migrated list, never a map.
	for _, op := range src.Migrated {
		decl := declared[op] // zero value when undeclared: every declared row reads UNKNOWN
		view, observed := views[op]
		if !observed {
			view = divergence.OperationView{
				OperationRecord: divergence.OperationRecord{Operation: op},
				State:           divergence.StateUnknown,
				Reach:           divergence.ReachUnevaluated,
			}
		}
		oa := OperationAssessment{Operation: op, Criteria: make([]Row, 0, len(Criteria))}
		for _, c := range Criteria {
			art := decl.Criteria[c.ID]
			var row Row
			switch c.ID {
			case "c1":
				row = evalReleaseLine(op, art, src.Git)
			case "c2":
				row = evalDivergence(d.K, view, art, src.RecordErr)
			case "c3", "c5":
				row = evalSymbols(art, src.Symbols)
			case "c4":
				row = evalDeclaredRun(art, src.Git)
			case "c6":
				row = evalRollback(op, art, src)
			}
			row.ID, row.Name = c.ID, c.Name
			oa.Criteria = append(oa.Criteria, row)
		}
		oa.Verdict = Reduce(oa.states())
		a.Operations = append(a.Operations, oa)
	}
	return a, nil
}

func summarize(src Sources) RecordSummary {
	doc := src.Record
	s := RecordSummary{
		State:        string(doc.State),
		Directory:    doc.Directory,
		Observations: doc.Observations,
		Mismatches:   doc.Mismatches,
		Skipped:      doc.Skipped,
		Unreadable:   doc.Unreadable,
		Pruned:       doc.Pruned,
	}
	if src.RecordErr != nil {
		s.Error = src.RecordErr.Error()
		s.State = string(divergence.StateUnknown)
	}
	return s
}

// ---------------------------------------------------------------------------
// c1 — shadow-release-line
// ---------------------------------------------------------------------------

// canaryPath is the file whose content at a tag says what shipped.
const canaryPath = "surfaces/client/canary.go"

var shippedDefaultRe = regexp.MustCompile(`(?m)^const canaryModeDefault = CanaryMode(Shadow|Active)\b`)

// evalReleaseLine confirms, for each declared tag, that the tag exists and
// that at that tag the operation was in the migrated list AND the compiled-in
// default was shadow or active. It reads the file text at the tag because a
// tag cannot be linked against; the two patterns are the exact lines the
// property is stated on.
func evalReleaseLine(op string, art Artifact, git Git) Row {
	if len(art.ReleaseTags) == 0 {
		return Row{State: StateUnknown, Reason: "no release tag declared"}
	}
	if git == nil {
		return Row{State: StateUnknown, Reason: "not inside a git checkout; declared tags cannot be confirmed"}
	}
	opLine := regexp.MustCompile(`(?m)^\s*"` + regexp.QuoteMeta(op) + `",\s*$`)
	var confirmed, unconfirmed []string
	for _, tag := range art.ReleaseTags {
		ok, err := git.TagExists(tag)
		if err != nil {
			return Row{State: StateUnknown, Reason: fmt.Sprintf("git tag lookup failed: %v", err)}
		}
		if !ok {
			unconfirmed = append(unconfirmed, tag+" (no such tag)")
			continue
		}
		body, err := git.FileAtTag(tag, canaryPath)
		if err != nil {
			unconfirmed = append(unconfirmed, tag+" ("+canaryPath+" unreadable at tag)")
			continue
		}
		if !opLine.Match(body) {
			return Row{State: StateFail, Evidence: tag,
				Reason: fmt.Sprintf("%s exists but %q was not in migratedOperations at that tag", tag, op)}
		}
		if !shippedDefaultRe.Match(body) {
			return Row{State: StateFail, Evidence: tag,
				Reason: fmt.Sprintf("%s exists but canaryModeDefault was not shadow or active at that tag", tag)}
		}
		confirmed = append(confirmed, tag)
	}
	if len(confirmed) == 0 {
		return Row{State: StateUnknown, Evidence: strings.Join(unconfirmed, ", "),
			Reason: "no declared tag could be confirmed"}
	}
	ev := strings.Join(confirmed, " ") + " (tag exists; op in migratedOperations and default shadow/active at tag)"
	if len(unconfirmed) > 0 {
		ev += "; unconfirmed: " + strings.Join(unconfirmed, ", ")
	}
	return Row{State: StatePass, Evidence: ev}
}

// ---------------------------------------------------------------------------
// c2 — divergence
// ---------------------------------------------------------------------------

// evalDivergence is precondition (a) clauses a.2 .. a.4 as a machine reads
// them: observations >= K and zero unexplained mismatches, with skips
// disclosed and never counted as agreement.
//
// Order matters and is deliberate. A recorded, unexplained mismatch is FAIL
// before K is even consulted: the record is decisive about disagreement no
// matter how many agreements were wanted. Only then does an unset or unmet K
// make the row UNKNOWN.
func evalDivergence(k *int, view divergence.OperationView, art Artifact, readErr error) Row {
	if readErr != nil {
		return Row{State: StateUnknown, Reason: "divergence record unreadable: " + readErr.Error()}
	}
	explained := 0
	for _, m := range art.ExplainedMismatches {
		explained += m.Count
	}
	ev := fmt.Sprintf("observations %d, mismatches %d", view.Observations, view.Mismatches)
	if explained > 0 {
		ev += fmt.Sprintf(" (%d explained)", explained)
	}
	if view.Skipped > 0 {
		ev += fmt.Sprintf(", skipped %d", view.Skipped)
	}
	if view.Reach != "" && view.Reach != divergence.ReachUnevaluated {
		ev += ", reach " + string(view.Reach)
	}
	unexplained := view.Mismatches - explained
	switch {
	case unexplained > 0:
		return Row{State: StateFail, Evidence: ev,
			Reason: fmt.Sprintf("%d unexplained mismatch(es) recorded", unexplained)}
	case unexplained < 0:
		return Row{State: StateUnknown, Evidence: ev,
			Reason: fmt.Sprintf("declaration explains %d mismatch(es) but the record holds %d — the two disagree", explained, view.Mismatches)}
	}
	if k == nil {
		return Row{State: StateUnknown, Evidence: ev,
			Reason: "K unset — owner decision 1 not taken (stories/SW-238/preconditions.md)"}
	}
	if view.Observations < *k {
		return Row{State: StateUnknown, Evidence: ev,
			Reason: fmt.Sprintf("observations %d < K=%d", view.Observations, *k)}
	}
	return Row{State: StatePass, Evidence: ev + fmt.Sprintf(", K=%d met", *k)}
}

// ---------------------------------------------------------------------------
// c3 / c5 — named test symbols that exist in the tree
// ---------------------------------------------------------------------------

func evalSymbols(art Artifact, look SymbolLookup) Row {
	if len(art.Tests) == 0 {
		return Row{State: StateUnknown, Reason: "no test symbol declared"}
	}
	if look == nil {
		return Row{State: StateUnknown, Reason: "source tree unavailable; declared symbols cannot be confirmed"}
	}
	var found, missing []string
	for _, ts := range art.Tests {
		ref := ts.File + "::" + ts.Symbol
		ok, err := look(ts.File, ts.Symbol)
		if err != nil {
			return Row{State: StateUnknown, Evidence: ref, Reason: fmt.Sprintf("symbol lookup failed: %v", err)}
		}
		if !ok {
			missing = append(missing, ref)
			continue
		}
		found = append(found, ref)
	}
	if len(missing) > 0 {
		return Row{State: StateUnknown, Evidence: strings.Join(found, " "),
			Reason: "declared symbol(s) not found in the tree: " + strings.Join(missing, " ")}
	}
	return Row{State: StatePass, Evidence: strings.Join(found, " ")}
}

// ---------------------------------------------------------------------------
// c4 / c6 — a declared CI run: workflow + run id + sha the checkout knows
// ---------------------------------------------------------------------------

// evalDeclaredRun accepts a run only when all three of workflow, run id and sha
// are declared and the sha is a commit the checkout knows. The run itself is
// never fetched (zero egress): what the checkout can confirm is that the
// declaration points at a real commit, and the honesty rule's other half — that
// the run id exists — is the declarer's, recorded with the sha so it can be
// audited.
func evalDeclaredRun(art Artifact, git Git) Row {
	if art.Workflow == "" && art.RunID == "" && art.SHA == "" {
		return Row{State: StateUnknown, Reason: "no CI run declared"}
	}
	var missing []string
	if art.Workflow == "" {
		missing = append(missing, "workflow")
	}
	if art.RunID == "" {
		missing = append(missing, "run_id")
	}
	if art.SHA == "" {
		missing = append(missing, "sha")
	}
	ref := fmt.Sprintf("%s run %s @ %s", art.Workflow, art.RunID, art.SHA)
	if len(missing) > 0 {
		return Row{State: StateUnknown, Evidence: ref, Reason: "declaration incomplete: missing " + strings.Join(missing, ", ")}
	}
	if git == nil {
		return Row{State: StateUnknown, Evidence: ref, Reason: "not inside a git checkout; the declared sha cannot be confirmed"}
	}
	ok, err := git.CommitExists(art.SHA)
	if err != nil {
		return Row{State: StateUnknown, Evidence: ref, Reason: fmt.Sprintf("git lookup failed: %v", err)}
	}
	if !ok {
		return Row{State: StateUnknown, Evidence: ref, Reason: "declared sha is not a commit this checkout knows"}
	}
	return Row{State: StatePass, Evidence: ref + " (sha is a known commit; run id declared, not fetched)"}
}

// ---------------------------------------------------------------------------
// c6 — rollback: the code half, then the declared CI leg
// ---------------------------------------------------------------------------

var envNameRe = regexp.MustCompile(`^GRAPHI_CANARY_[A-Z0-9_]+$`)

func evalRollback(op string, art Artifact, src Sources) Row {
	if src.LegacyAccepted == nil || src.EnvVarFor == nil {
		return Row{State: StateUnknown, Reason: "kill-switch sources not supplied"}
	}
	if err := src.LegacyAccepted(); err != nil {
		return Row{State: StateFail, Reason: "ParseCanaryMode(\"legacy\") rejects the rollback position: " + err.Error()}
	}
	env := src.EnvVarFor(op)
	if !envNameRe.MatchString(env) {
		return Row{State: StateFail, Reason: fmt.Sprintf("kill-switch variable for %q is %q, not a usable environment name", op, env)}
	}
	run := evalDeclaredRun(art, src.Git)
	code := fmt.Sprintf("legacy accepted; %s on the seam; %s", op, env)
	if run.State != StatePass {
		run.Evidence = strings.TrimSpace(code + "; " + run.Evidence)
		return run
	}
	run.Evidence = code + "; " + run.Evidence
	return run
}
