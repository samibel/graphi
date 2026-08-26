package evidence

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// AX-00 (SW-220) AC-4 — the aggregated protection-gate view.
//
// graphi already has strong per-invariant gates, each in its own workflow:
// parity, cgo-conformance, the egress canary, the layer-direction guard, the
// coverage matrix, the reproducible release build, and the binary-size budget.
// What a maintainer could not do before this file is ASK ONE QUESTION — "what is
// this commit's gate posture?" — because the seven answers live in seven places
// and GitHub's `needs:` cannot span workflow files.
//
// This is a VIEW, not a re-implementation. It executes no gate and re-derives no
// verdict. It reads a checked-in DECLARATION of which workflow/job owns each
// invariant, and a directory of result records those jobs emit, and renders the
// aggregate. The one rule it enforces is the rule the evidence index already
// enforces, applied to gates instead of rows:
//
//	an absent, unreadable, invalid or unbacked result renders UNKNOWN.
//	UNKNOWN is never rendered as PASS.
//
// That is the whole point of the artifact for a strangler refactor: the failure
// mode to design against is not a red gate, it is a gate that quietly stopped
// running while the dashboard kept looking green.

// ProtectionGatesPath is the repo-relative checked-in declaration of the gates
// this view aggregates.
const ProtectionGatesPath = "docs/rc/protection-gates.yaml"

// WorkflowsDir is the repo-relative directory the declaration's workflow files
// are resolved against.
const WorkflowsDir = ".github/workflows"

// ProtectionGate is one declared invariant and the CI job that owns it. The
// declaration is data, not code, so retargeting a gate is a source edit.
type ProtectionGate struct {
	ID       string // stable key, also the expected result-file basename
	Gate     string // human name
	Enforces string // the invariant in one sentence
	Workflow string // workflow file under .github/workflows
	Job      string // job key inside that workflow
	Cadence  string // when it runs (e.g. "every PR", "dispatch/nightly")
}

// GateResult is one gate's recorded outcome for a commit, as emitted by the job
// that owns it. Every field is optional on the wire; missing fields degrade the
// row to UNKNOWN rather than being guessed at.
type GateResult struct {
	Gate        string `json:"gate"`
	Status      string `json:"status"`
	EvidenceURI string `json:"evidence_uri"`
	SHA         string `json:"sha"`
	Run         string `json:"run"`
}

// GateRow is one rendered row: the declaration plus the resolved status and the
// reason that status is what it is. Reason is never empty for a non-PASS row —
// "UNKNOWN" with no explanation is the failure this instrument exists to avoid.
type GateRow struct {
	ProtectionGate
	Status string
	Reason string
	Run    string
}

// GateView is the whole aggregate for one commit.
type GateView struct {
	SHA  string
	Rows []GateRow
	// DeclarationErrors are gates whose declared workflow/job does not resolve
	// in this checkout. They are rot in the instrument itself, so unlike a
	// missing result they are an ERROR, not merely an UNKNOWN row.
	DeclarationErrors []Violation
}

// Pass reports whether the declaration itself is intact. It deliberately says
// nothing about the gate statuses: this view has no verdict to give — parity,
// for example, runs dispatch/nightly and is legitimately UNKNOWN on a PR.
// Turning "all seven PASS" into a blocking condition is a separate, deliberate
// decision, not a side effect of being able to see them.
func (v GateView) Pass() bool { return len(v.DeclarationErrors) == 0 }

// LoadProtectionGates reads and parses the checked-in declaration. It accepts
// the same dependency-free block subset the evidence index uses.
func LoadProtectionGates(path string) ([]ProtectionGate, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("evidence: read protection gates %q: %w", path, err)
	}
	gates, err := parseProtectionGatesYAML(string(raw))
	if err != nil {
		return nil, fmt.Errorf("evidence: parse protection gates %q: %w", path, err)
	}
	if len(gates) == 0 {
		return nil, fmt.Errorf("evidence: protection gates %q declares no gates", path)
	}
	return gates, nil
}

// parseProtectionGatesYAML parses the constrained subset:
//
//	gates:
//	  - id: <scalar>
//	    gate: <scalar>
//	    enforces: <scalar>
//	    workflow: <scalar>
//	    job: <scalar>
//	    cadence: <scalar>
func parseProtectionGatesYAML(text string) ([]ProtectionGate, error) {
	var (
		gates []ProtectionGate
		cur   *ProtectionGate
		open  bool
	)
	flush := func() {
		if cur != nil {
			gates = append(gates, *cur)
			cur = nil
		}
	}

	for lineNo, rawLine := range strings.Split(text, "\n") {
		line := stripComment(rawLine)
		if strings.TrimSpace(line) == "" {
			continue
		}
		trimmed := strings.TrimSpace(line)

		if line[0] != ' ' && line[0] != '\t' {
			flush()
			if trimmed != "gates:" {
				return nil, fmt.Errorf("line %d: unexpected top-level key %q (want gates:)", lineNo+1, trimmed)
			}
			open = true
			continue
		}
		if !open {
			return nil, fmt.Errorf("line %d: field before the gates: section: %q", lineNo+1, trimmed)
		}
		if strings.HasPrefix(trimmed, "- ") {
			flush()
			cur = &ProtectionGate{}
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		}
		if cur == nil {
			return nil, fmt.Errorf("line %d: field outside any gate item: %q", lineNo+1, trimmed)
		}
		key, val, err := splitField(trimmed)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo+1, err)
		}
		switch key {
		case "id":
			cur.ID = val
		case "gate":
			cur.Gate = val
		case "enforces":
			cur.Enforces = val
		case "workflow":
			cur.Workflow = val
		case "job":
			cur.Job = val
		case "cadence":
			cur.Cadence = val
		default:
			return nil, fmt.Errorf("line %d: unknown protection-gate field %q", lineNo+1, key)
		}
	}
	flush()
	return gates, nil
}

// LoadGateResults reads every declared gate's result record from dir. A missing
// directory is NOT an error: it is the ordinary case (no results collected), and
// it must produce a view of honest UNKNOWNs rather than a failure that tempts
// someone to skip the check entirely.
func LoadGateResults(dir string, gates []ProtectionGate) map[string]GateResult {
	results := make(map[string]GateResult, len(gates))
	if strings.TrimSpace(dir) == "" {
		return results
	}
	for _, gate := range gates {
		raw, err := os.ReadFile(filepath.Join(dir, gate.ID+".json"))
		if err != nil {
			continue
		}
		var result GateResult
		if err := json.Unmarshal(raw, &result); err != nil {
			// Record the unreadable result explicitly so the row can say
			// "unreadable" instead of the indistinguishable "absent".
			results[gate.ID] = GateResult{Gate: gate.ID, Status: "\x00malformed"}
			continue
		}
		results[gate.ID] = result
	}
	return results
}

// BuildGateView resolves each declared gate against the checkout (does the
// workflow/job still exist?) and against the collected results, and returns the
// aggregate. root is the module root; resultsDir may be empty.
func BuildGateView(root, resultsDir, sha string, gates []ProtectionGate) GateView {
	results := LoadGateResults(resultsDir, gates)
	view := GateView{SHA: sha}

	for _, gate := range gates {
		row := GateRow{ProtectionGate: gate, Status: StatusUnknown}

		if err := verifyGateDeclaration(root, gate); err != nil {
			view.DeclarationErrors = append(view.DeclarationErrors, Violation{GateID: gate.ID, Reason: err.Error()})
			row.Reason = "declaration does not resolve in this checkout: " + err.Error()
			view.Rows = append(view.Rows, row)
			continue
		}

		result, ok := results[gate.ID]
		switch {
		case !ok:
			row.Reason = "no result recorded for this commit"
		case result.Status == "\x00malformed":
			row.Reason = "result record is present but unreadable — a result nobody can parse is not evidence"
		default:
			row.Run = result.Run
			row.Status, row.Reason = classifyGateResult(result)
		}
		view.Rows = append(view.Rows, row)
	}
	return view
}

// classifyGateResult applies the honesty rule to one recorded result. It is the
// only place a status becomes PASS, and it can only do so with both an evidence
// URI and a digest behind it — the same bar internal/evidence.Check holds an
// index row to.
func classifyGateResult(result GateResult) (status, reason string) {
	switch strings.ToUpper(strings.TrimSpace(result.Status)) {
	case StatusPass:
		missing := make([]string, 0, 2)
		if strings.TrimSpace(result.EvidenceURI) == "" {
			missing = append(missing, "evidence_uri")
		}
		if strings.TrimSpace(result.SHA) == "" {
			missing = append(missing, "sha")
		}
		if len(missing) > 0 {
			return StatusUnknown, "reported PASS without " + strings.Join(missing, " and ") +
				" — an unbacked PASS is downgraded to UNKNOWN, never rendered green"
		}
		return StatusPass, "backed by a versioned artifact"
	case StatusFail:
		return StatusFail, "the gate ran and failed"
	case StatusStale:
		return StatusStale, "measured against a superseded candidate"
	case StatusUnknown:
		return StatusUnknown, "the job reported UNKNOWN"
	case "":
		return StatusUnknown, "result record carries no status"
	default:
		return StatusUnknown, fmt.Sprintf("result record carries an invalid status %q (want PASS, FAIL, UNKNOWN or STALE)", result.Status)
	}
}

// verifyGateDeclaration checks that the workflow file the gate names exists and
// declares the job it names. Without this the declaration rots silently: a
// renamed job would leave the row permanently, and plausibly, UNKNOWN.
func verifyGateDeclaration(root string, gate ProtectionGate) error {
	for field, value := range map[string]string{"id": gate.ID, "gate": gate.Gate, "workflow": gate.Workflow, "job": gate.Job} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("declaration field %q is blank", field)
		}
	}
	path := filepath.Join(root, filepath.FromSlash(WorkflowsDir), gate.Workflow)
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("workflow %s/%s is not readable: %v", WorkflowsDir, gate.Workflow, err)
	}
	if !workflowDeclaresJob(string(raw), gate.Job) {
		return fmt.Errorf("workflow %s/%s declares no job %q", WorkflowsDir, gate.Workflow, gate.Job)
	}
	return nil
}

// workflowDeclaresJob reports whether a workflow's `jobs:` block contains a job
// with the given key. It is a deliberately small, dependency-free scan: find the
// top-level `jobs:` line, then accept the first indentation level under it.
func workflowDeclaresJob(yaml, job string) bool {
	inJobs := false
	jobIndent := -1
	for _, rawLine := range strings.Split(yaml, "\n") {
		line := stripComment(rawLine)
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent == 0 {
			inJobs = strings.TrimSpace(line) == "jobs:"
			jobIndent = -1
			continue
		}
		if !inJobs {
			continue
		}
		if jobIndent == -1 {
			jobIndent = indent
		}
		if indent != jobIndent {
			continue
		}
		if key, _, ok := strings.Cut(strings.TrimSpace(line), ":"); ok && strings.TrimSpace(key) == job {
			return true
		}
	}
	return false
}

// RenderGateView renders the aggregate as a deterministic Markdown document. It
// is a pure function of the view (rows keep declaration order, no map iteration
// reaches the output), so two renders of one view are byte-identical.
func RenderGateView(view GateView) string {
	var b strings.Builder
	b.WriteString("# graphi — protection-gate posture\n\n")
	b.WriteString("Aggregated view of the invariant gates that must survive the extension-kernel\n")
	b.WriteString("strangler refactor (AX-00 / SW-220 AC-4). This view **executes nothing and\n")
	b.WriteString("re-derives nothing** — it reads the checked-in declaration in\n")
	b.WriteString("`" + ProtectionGatesPath + "` and the result records the owning jobs emit.\n\n")
	b.WriteString("**The rule:** an absent, unreadable, statusless, invalid or unbacked result renders\n")
	b.WriteString("**UNKNOWN**. UNKNOWN is never rendered as PASS. A gate that quietly stopped running\n")
	b.WriteString("looks exactly like a gate that never ran — and nothing like a gate that passed.\n\n")

	sha := strings.TrimSpace(view.SHA)
	if sha == "" {
		sha = StatusUnknown
	}
	fmt.Fprintf(&b, "**Commit:** `%s`\n\n", sha)

	b.WriteString("**Status legend:** ✅ PASS · ❌ FAIL · ❔ UNKNOWN · ⚠️ STALE\n\n")
	b.WriteString("| Gate | Enforces | Owning job | Cadence | Status | Why |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, row := range view.Rows {
		owner := fmt.Sprintf("`%s` › `%s`", row.Workflow, row.Job)
		if strings.TrimSpace(row.Run) != "" {
			owner += fmt.Sprintf(" ([run](%s))", row.Run)
		}
		fmt.Fprintf(&b, "| **%s** — %s | %s | %s | %s | %s | %s |\n",
			mdCell(row.ID), mdCell(row.Gate), mdCell(row.Enforces), owner,
			mdCell(row.Cadence), statusBadge(row.Status), mdCell(row.Reason))
	}

	b.WriteString("\n")
	fmt.Fprintf(&b, "%s\n", summarizeGateView(view))

	if len(view.DeclarationErrors) > 0 {
		b.WriteString("\n**Declaration errors** — the instrument itself is broken; these rows can never")
		b.WriteString(" become anything but UNKNOWN until the declaration is repaired:\n\n")
		for _, violation := range view.DeclarationErrors {
			fmt.Fprintf(&b, "- [%s] %s\n", violation.GateID, violation.Reason)
		}
	}
	return b.String()
}

// summarizeGateView renders the one-line tally. UNKNOWN is counted and named
// out loud rather than folded into "not failing".
func summarizeGateView(view GateView) string {
	counts := map[string]int{}
	for _, row := range view.Rows {
		counts[row.Status]++
	}
	order := []string{StatusPass, StatusFail, StatusStale, StatusUnknown}
	parts := make([]string, 0, len(order))
	for _, status := range order {
		parts = append(parts, fmt.Sprintf("%s=%d", status, counts[status]))
	}
	return fmt.Sprintf("**Tally (%d gates):** %s. UNKNOWN counts as NOT PASSED.",
		len(view.Rows), strings.Join(parts, " · "))
}

// GateIDs returns the declared gate ids in sorted order — a small convenience
// for callers that need to name the expected result files.
func GateIDs(gates []ProtectionGate) []string {
	ids := make([]string, 0, len(gates))
	for _, gate := range gates {
		ids = append(ids, gate.ID)
	}
	sort.Strings(ids)
	return ids
}
