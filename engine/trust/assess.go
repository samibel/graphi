package trust

import (
	"fmt"
	"sort"
	"strings"
)

// This file is the assessment model of the trust core (PRD §13.12, shaped by
// contract doc §1/§3): the closed verdict enum, the finding/limitation value
// types, the canonical finding order, and the canonical assessment
// serialization. It carries NO policy logic — a policy (Facts + Scope +
// Policy → Verdict, contract doc §3.0) produces the findings and sets the
// verdict; this layer only gives it the model to fill and the canonical bytes
// to emit. The zero Verdict ("") marks an assessment no policy has judged yet.

// AssessmentSchemaVersion versions the encoded assessment document. Bump only
// on breaking changes to the shape or value domain (contract doc §6).
const AssessmentSchemaVersion = 1

// Verdict is the closed trust-verdict enum (contract doc §1.5, PRD §11.5).
// Verdicts qualify policy assessments only — they never mix into the
// snapshot-state field (contract doc §1.8), and nothing in this layer ever
// derives one: only the policy layer assigns a non-zero Verdict.
type Verdict string

const (
	// VerdictPass — every policy rule held on the evaluated facts.
	VerdictPass Verdict = "PASS"
	// VerdictWarn — the policy found degradations that need attention but do
	// not block the use case.
	VerdictWarn Verdict = "WARN"
	// VerdictFail — the policy found blocking violations for its use case.
	VerdictFail Verdict = "FAIL"
	// VerdictUnverified — the facts required for a judgment are missing, not
	// generation-bound, or the scope is unresolvable/unsupported; never a
	// positive signal (PRD §7.5).
	//
	// PRD v1.0 §5/§6 renamed this from UNKNOWN, the value v0.8.0 shipped
	// (delta doc §A1). The new name is the more precise one: what the
	// fail-closed policies detect is the absence of *verifiable* evidence, not
	// mere ignorance. The Go identifier was renamed rather than aliased so an
	// un-migrated call site fails to compile instead of quietly emitting the
	// old wire value; `prdv1_wire_test.go` pins both halves.
	VerdictUnverified Verdict = "UNVERIFIED"
)

// Finding severities. Closed working set for this layer: every registry code
// defaults to one of these, and the canonical finding order ranks them
// error > warning > info.
const (
	// SeverityInfo — visibility only, never blocking on its own.
	SeverityInfo = "info"
	// SeverityWarning — a degradation the policy may escalate.
	SeverityWarning = "warning"
	// SeverityError — a violated precondition of the policy's use case.
	SeverityError = "error"
)

// Scope kinds — the closed §1.7 set (PRD §11.7). ScopeResultSet is frozen as
// vocabulary but its semantics are a deliberate leaves-open item (contract doc
// §5.4); nothing in this layer produces it.
const (
	ScopeRepository = "repository"
	ScopePackage    = "package"
	ScopeFile       = "file"
	ScopeSymbol     = "symbol"
	ScopeResultSet  = "result-set"
)

// ScopeRef names the area an assessment, finding, or limitation talks about
// (PRD §13.11). Kind is one of the closed §1.7 set; every other field is ""
// when not applicable to the kind. A resolved symbol scope carries ID (the
// NodeId), Path, and Symbol; an ambiguous or not-found target keeps the asked
// kind with ID empty — an unresolved scope is visible as such, never dressed
// up as a healthy one (fail closed, PRD §27).
type ScopeRef struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Path    string `json:"path"`
	Package string `json:"package"`
	Symbol  string `json:"symbol"`
}

// PolicyRef names the policy that judged an assessment (PRD §13.12; version
// discipline per contract doc §3.0 — the version is part of the output). The
// zero value means no policy was requested (the §2.3 presence rule: present
// with zero values, never omitted).
//
// ID is the canonical versioned identifier PRD v1.0 §6 names ("review-v1"),
// and is what the surfaces accept as the `--policy` token. It is always
// Policy.ID() — derived from Name and Version, never stored independently — so
// a rules change that bumps Version moves the identifier with it and the three
// fields cannot disagree. Name and Version stay on the wire beside it: they are
// the decomposition the version discipline operates on, and dropping them would
// be a breaking removal for no gain (delta doc §A2).
type PolicyRef struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version int    `json:"version"`
}

// Finding is one explained policy observation (PRD §26 FR-12): a registry
// code, the severity it fired at, the evidence dimension it belongs to, the
// scope it applies to, and the observed-vs-threshold pair backing the message
// (messages carry no unsupported claims — §26 acceptance criteria). Code MUST
// come from the closed v1 registry in findings.go; NewFinding is the
// constructing gate. Observed and Threshold are canonical strings ("" when not
// applicable) so the encoded form stays byte-stable — no any-typed values on
// a canonical document.
type Finding struct {
	Code      string   `json:"code"`
	Severity  string   `json:"severity"`
	Dimension string   `json:"dimension"`
	Scope     ScopeRef `json:"scope"`
	Observed  string   `json:"observed"`
	Threshold string   `json:"threshold"`
	Message   string   `json:"message"`
}

// Limitation is one coverage limit attached to an assessment (PRD §13.10
// reduced to the frozen wire shape {code, severity, count, action}, contract
// doc §2.2). Count is 0 for standing structural limits that are not countable
// (e.g. CROSS_REPOSITORY_UNAVAILABLE).
type Limitation struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Count    int    `json:"count"`
	Action   string `json:"action"`
}

// Assessment is the trust assessment document (PRD §13.12): which policy
// judged which scope over which snapshot state, with the explaining findings,
// the attached coverage limitations, and the deterministic next-step
// recommendations. Contract rules (contract doc §2.3) apply to the encoded
// form: every field always present, empty slices never null, lists canonically
// sorted.
type Assessment struct {
	SchemaVersion   int          `json:"schema_version"`
	Policy          PolicyRef    `json:"policy"`
	Scope           ScopeRef     `json:"scope"`
	SnapshotState   State        `json:"snapshot_state"`
	Verdict         Verdict      `json:"verdict"`
	Findings        []Finding    `json:"findings"`
	Limitations     []Limitation `json:"limitations"`
	Recommendations []string     `json:"recommendations"`
	// ChecksPassed is the explicit "all checks passed" list PRD §26 demands
	// ("kein Verdict ohne Findings oder explizite „all checks passed“-Liste"):
	// one fixed gloss per policy check that ran and held on the evaluated
	// facts, in the policy's static rule order. A PASS with no findings is
	// explained by this list alone; checks that fired appear as findings
	// instead, and checks that could not run (missing evidence) appear in
	// neither list — the finding that explains the missing evidence covers
	// them. Additive v1 field of the assessment document (contract doc §2.3
	// rule 7).
	ChecksPassed []string `json:"checks_passed"`
}

// severityRank orders severities most-severe-first for the canonical sorts.
// Anything outside the closed set ranks after info so the order stays total
// even over a hand-built (non-NewFinding) value.
func severityRank(s string) int {
	switch s {
	case SeverityError:
		return 0
	case SeverityWarning:
		return 1
	case SeverityInfo:
		return 2
	default:
		return 3
	}
}

// compareScopes is the total lexical order over ScopeRef used as a sort
// tiebreak: field-wise, in declaration order.
func compareScopes(a, b ScopeRef) int {
	if c := strings.Compare(a.Kind, b.Kind); c != 0 {
		return c
	}
	if c := strings.Compare(a.ID, b.ID); c != 0 {
		return c
	}
	if c := strings.Compare(a.Path, b.Path); c != 0 {
		return c
	}
	if c := strings.Compare(a.Package, b.Package); c != 0 {
		return c
	}
	return strings.Compare(a.Symbol, b.Symbol)
}

// compareFindings is the canonical finding order (PRD §26 "Findings sind
// sortiert"): severity rank descending (error > warning > info), then Code
// ascending, then Scope. The remaining fields extend the comparison so the
// order is total and the canonical bytes never depend on producer order.
func compareFindings(a, b Finding) int {
	if ra, rb := severityRank(a.Severity), severityRank(b.Severity); ra != rb {
		return ra - rb
	}
	if c := strings.Compare(a.Severity, b.Severity); c != 0 {
		return c
	}
	if c := strings.Compare(a.Code, b.Code); c != 0 {
		return c
	}
	if c := compareScopes(a.Scope, b.Scope); c != 0 {
		return c
	}
	if c := strings.Compare(a.Observed, b.Observed); c != 0 {
		return c
	}
	if c := strings.Compare(a.Threshold, b.Threshold); c != 0 {
		return c
	}
	return strings.Compare(a.Message, b.Message)
}

// SortFindings sorts fs in place into the canonical finding order. Policies
// call it after collecting rule findings and BEFORE deriving recommendations,
// since the recommendation order follows the finding order that produced it.
func SortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool { return compareFindings(fs[i], fs[j]) < 0 })
}

// sortLimitations sorts ls in place into the canonical limitation order —
// the same severity-rank-then-code convention as findings, extended over the
// remaining fields for totality.
func sortLimitations(ls []Limitation) {
	sort.SliceStable(ls, func(i, j int) bool {
		a, b := ls[i], ls[j]
		if ra, rb := severityRank(a.Severity), severityRank(b.Severity); ra != rb {
			return ra < rb
		}
		if a.Severity != b.Severity {
			return a.Severity < b.Severity
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		if a.Count != b.Count {
			return a.Count < b.Count
		}
		return a.Action < b.Action
	})
}

// EncodeAssessment serializes an Assessment to its canonical byte form under
// the same conventions as Encode: HTML escaping disabled, no indentation, no
// trailing newline, nil slices normalized to empty (contract doc §2.3 rule 2).
// Unlike Snapshot, an Assessment's lists are filled by policy rules rather
// than one canonicalizing builder, so this encoder owns the canonical list
// order: Findings and Limitations are copied and re-sorted (the caller's
// slices are never mutated). Recommendations and ChecksPassed keep the
// caller's order — the finding-derived order Recommendations produced and the
// policy's static rule order are part of the meaning, not an encoder courtesy.
func EncodeAssessment(a Assessment) ([]byte, error) {
	findings := make([]Finding, len(a.Findings))
	copy(findings, a.Findings)
	SortFindings(findings)
	a.Findings = findings

	limitations := make([]Limitation, len(a.Limitations))
	copy(limitations, a.Limitations)
	sortLimitations(limitations)
	a.Limitations = limitations

	if a.Recommendations == nil {
		a.Recommendations = []string{}
	}
	if a.ChecksPassed == nil {
		a.ChecksPassed = []string{}
	}
	b, err := encodeCanonical(a)
	if err != nil {
		return nil, fmt.Errorf("trust: encode assessment: %w", err)
	}
	return b, nil
}
