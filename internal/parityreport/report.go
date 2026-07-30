// Package parityreport is the versioned report schema for the real-repository
// full-vs-incremental parity matrix (internal/parity, cmd/parity).
//
// WHY THIS IS A NEW PACKAGE AND NOT internal/evalreport — this is a hard
// boundary, not a style choice. internal/evalreport/freshness.go:59 defines
// RequiredChangeClasses, and cmd/eval/changeseq.go:36-39 derives
// changeSequenceCycle from len(evalreport.RequiredChangeClasses). Adding a class
// there reshapes the change sequence cmd/eval generates for the freshness and
// update distributions — an INSTRUMENT change. The WP4 evidence row names
// exactly that situation: "identical product bytes measured by a DIFFERENT
// instrument" are not the same numbers, and it would invalidate the baseline
// SW-143 is waiting to run. The parity harness therefore keeps its own class
// list and its own report schema, in its own packages, and never imports,
// extends or modifies internal/evalreport.
//
// The shape is MODELLED on internal/evalreport (a versioned envelope, explicit
// provenance, per-row verdicts, honest UNKNOWN/incomplete states) without
// sharing a line of it.
package parityreport

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// SchemaVersion versions this report envelope. Bump when a field is added,
// removed or re-meaned, so a consumer can tell whether what it must parse
// changed.
const SchemaVersion = 1

// HarnessVersion identifies the harness that produced a report. It is recorded
// in every report so a verdict can never be attributed to the wrong harness.
const HarnessVersion = "parity-matrix/1"

// CandidateSHA is the P0 candidate, v0.7.1. The harness does NOT exist at this
// SHA, so no report may ever claim it ran there — see Provenance.Statement.
const CandidateSHA = "80d67ed586723ab22704cf7aada316138cb1360e"

// FR7ChangeClasses is the size of the authoritative matrix: PRD FR-7 lists
// EXACTLY 15 change classes (heading "Änderungsklassen", prefix "Mindestens:"),
// and the PRD's own prose at :807 names the number independently
// ("die vollständige 15-Klassen-Matrix ist noch offen").
//
// It is pinned HERE, as a constant, and Finalize refuses to call any run
// complete over fewer — because "complete" measured against whatever row list a
// caller happened to pass is not a completeness check at all. Delta PRD §9's
// SW-144 bullet list has 17 entries; the extra two are CRASH CONDITIONS, not
// change classes, and conflating them is exactly what produced backlog.md:55's
// wrong "16 change classes". Raising this number must be a deliberate edit made
// together with docs/rc/parity-classes.yaml.
const FR7ChangeClasses = 15

// Verdicts. A row is PASS or FAIL; there is no threshold and no warning tier
// (PRD §12.3 is binary). DEFERRED and SKIPPED are run-shape states, never
// outcomes: neither may be read as a pass, and both make a run incomplete for
// publication purposes.
const (
	// VerdictPass — snapshot bytes of the incrementally-updated graph and a
	// fresh full index of the same final tree are byte-identical.
	VerdictPass = "PASS"
	// VerdictFail — they are not. Every mismatch is blocking (Delta PRD :1092).
	VerdictFail = "FAIL"
	// VerdictDeferred — the class is declared harness_row: "deferred" in
	// docs/rc/parity-classes.yaml and belongs to another story (SW-158).
	VerdictDeferred = "DEFERRED"
	// VerdictSkipped — the class did not run in THIS invocation (tier cap, a
	// -classes filter, or no repository within the cap exhibits the structure
	// the class needs). Never a pass.
	VerdictSkipped = "SKIPPED"
	// VerdictError — the harness itself could not execute the row. Distinct
	// from FAIL on purpose: a broken harness is not a product finding, and
	// conflating them is how a harness bug becomes a published defect.
	VerdictError = "ERROR"
)

// Run-level outcomes.
const (
	OutcomePass       = "PASS"
	OutcomeFail       = "FAIL"
	OutcomeIncomplete = "INCOMPLETE"
)

// Provenance is the run-level attribution block. Every field here exists to
// stop a reader from over-reading the result.
type Provenance struct {
	// CandidateSHA is the frozen P0 candidate the product tree is compared
	// AGAINST. It is never the SHA the run happened at.
	CandidateSHA string `json:"candidate_sha"`
	// RunSHA is the HEAD the harness actually ran at.
	RunSHA string `json:"run_sha"`
	// Statement is the ONLY sanctioned phrasing of the relationship between the
	// two. NewProvenance builds it; nothing else may.
	Statement string `json:"statement"`
	// ProductDiffEmpty records that the product is byte-identical to the
	// candidate. It is decided by the BUILT BINARY comparison below, with the
	// path diff as informational context — see CollectProvenance.
	ProductDiffEmpty bool `json:"product_diff_empty"`
	// ProductDiffDetail carries the evidence behind that verdict, whichever way
	// it went.
	ProductDiffDetail string `json:"product_diff_detail,omitempty"`
	// ProductBinaryHead / ProductBinaryCandidate are the sha256 digests of
	// `go build -trimpath -buildvcs=false ./cmd/graphi` at the run SHA and at
	// the candidate. Equal digests are the authoritative statement that the
	// harness is measuring the candidate's product; -trimpath is what makes the
	// two comparable across different build directories.
	ProductBinaryHead      string `json:"product_binary_head,omitempty"`
	ProductBinaryCandidate string `json:"product_binary_candidate,omitempty"`
	// WorktreeClean records that the run happened on a clean worktree.
	WorktreeClean bool `json:"worktree_clean"`
	// WorktreeDirtyDetail carries the dirty paths when it is not.
	WorktreeDirtyDetail string `json:"worktree_dirty_detail,omitempty"`
	// RunnerClass names the machine class (e.g. "ubuntu-latest"). A run with no
	// runner class is not publishable: an unattributed measurement is an
	// anecdote.
	RunnerClass string `json:"runner_class"`
	// GoVersion and OSArch pin the toolchain and platform, because a row that
	// differs between two otherwise-identical dispatches is an environment
	// finding that must be explainable.
	GoVersion string `json:"go_version"`
	OSArch    string `json:"os_arch"`
	// GeneratedAt is the RFC3339 UTC timestamp of the run.
	GeneratedAt string `json:"generated_at"`
	// HarnessVersion and SchemaVersion identify the producer.
	HarnessVersion string `json:"harness_version"`
	SchemaVersion  int    `json:"schema_version"`
}

// NewProvenance builds the provenance block with the ONE sanctioned statement of
// the candidate relationship.
//
// The phrasing is load-bearing and is enforced here rather than left to a
// caller's prose. The harness does not exist at the candidate SHA, so a record
// saying the run happened AT the candidate would be false. What is true, and
// all that is claimed, is that the PRODUCT SOURCE is byte-identical to it.
func NewProvenance(runSHA string) Provenance {
	return Provenance{
		CandidateSHA:   CandidateSHA,
		RunSHA:         runSHA,
		Statement:      "product source byte-identical to v0.7.1 at " + CandidateSHA,
		HarnessVersion: HarnessVersion,
		SchemaVersion:  SchemaVersion,
	}
}

// RepoRef records one pinned repository exactly as it was materialized.
type RepoRef struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
	Ref  string `json:"ref,omitempty"`
	// PinnedSHA is the manifest's pin; HeadSHA is what the clone actually
	// resolved to. They are recorded separately so a re-pointed upstream tag is
	// visible rather than absorbed.
	PinnedSHA string `json:"pinned_sha,omitempty"`
	HeadSHA   string `json:"head_sha,omitempty"`
	Tier      int    `json:"tier"`
	GoFiles   int    `json:"go_files,omitempty"`
}

// ClassResult is one change-class row of the matrix.
type ClassResult struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// Repo is the repository this class ACTUALLY ran on, and RepoHeadSHA the
	// commit it ran at. AC-6 exists so no reader assumes a class was exercised
	// on a repository it never touched.
	Repo        string `json:"repo,omitempty"`
	RepoHeadSHA string `json:"repo_head_sha,omitempty"`
	// SelectedBecause records WHY this repository was chosen — a manifest
	// stratification property, or the smallest in-cap repository whose real
	// source exhibits the structure the class needs.
	SelectedBecause string `json:"selected_because,omitempty"`
	Verdict         string `json:"verdict"`
	// Mutation describes the real edit applied to real source, precisely enough
	// to be re-applied by hand.
	Mutation string `json:"mutation,omitempty"`
	// SnapshotFullSHA256 / SnapshotIncSHA256 are digests of the two portable
	// snapshot envelopes that were compared. Equal digests are the PASS.
	SnapshotFullSHA256 string `json:"snapshot_full_sha256,omitempty"`
	SnapshotIncSHA256  string `json:"snapshot_inc_sha256,omitempty"`
	// Node/edge counts on each side, for a human reading a FAIL.
	FullNodes int `json:"full_nodes,omitempty"`
	FullEdges int `json:"full_edges,omitempty"`
	IncNodes  int `json:"inc_nodes,omitempty"`
	IncEdges  int `json:"inc_edges,omitempty"`
	// Diagnostic is the graphi compare / BranchDiffReport output, captured on
	// FAIL only. IT NEVER DECIDES THIS ROW — snapshot bytes do. It is a Labs
	// surface and a §12.3 gate must not depend on a Labs analyzer's
	// BranchDiffSchemaVersion.
	Diagnostic string `json:"diagnostic,omitempty"`
	// DiagnosticContradiction is set when the diff reports no deltas while the
	// snapshot bytes differ. That combination is itself a finding (AC-3) and is
	// recorded as one rather than resolved in either direction.
	DiagnosticContradiction bool `json:"diagnostic_contradiction,omitempty"`
	// KnownDefect names a tracked product defect this row is expected to
	// surface (e.g. PARITY-001). It does NOT downgrade the verdict: the row
	// still reads FAIL and the run still fails. It exists so a reader can tell
	// a known, filed, scheduled defect from a new one.
	KnownDefect string `json:"known_defect,omitempty"`
	// DeferredTo names the story that owns a DEFERRED row.
	DeferredTo string `json:"deferred_to,omitempty"`
	// Detail is the reason for a FAIL, SKIPPED or ERROR verdict.
	Detail     string `json:"detail,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

// StoreCounts are the two PRD §12.3 store-level counts, taken over the REAL
// repository graph after the class's change sequence — not inferred from the
// fixture-level proofs at engine/ingest/link_external_lifecycle_e2e_test.go:29
// and link_cascade_test.go:118.
type StoreCounts struct {
	Repo string `json:"repo"`
	// Class is the row whose final tree these counts were taken over.
	Class string `json:"class"`
	// OrphanedExternalNodes counts nodes of kind "external" with no inbound
	// edge. §12.3 requires 0.
	OrphanedExternalNodes int `json:"orphaned_external_nodes"`
	// StaleLinkerEdges counts edges whose from or to endpoint is not a node in
	// the graph. §12.3 requires 0.
	StaleLinkerEdges int `json:"stale_linker_edges"`
	// OrphanSample / StaleSample carry a bounded sample so a non-zero count is
	// actionable rather than a bare number.
	OrphanSample []string `json:"orphan_sample,omitempty"`
	StaleSample  []string `json:"stale_sample,omitempty"`
	Pass         bool     `json:"pass"`
}

// NotComparedNote states, in the report itself, what snapshot parity does NOT
// cover. A "100% parity" line with no stated scope is an overclaim (AC-13).
type NotComparedNote struct {
	Items       []string `json:"items"`
	Rationale   string   `json:"rationale"`
	Disposition string   `json:"disposition"`
}

// DefaultNotCompared is the blind spot as docs/adr/0004 dispositions it.
func DefaultNotCompared() NotComparedNote {
	return NotComparedNote{
		Items: []string{
			"intra-process taint findings",
			"embeddings / vectors",
			"index.profile metadata",
			"the ingest-meta sidecar",
			"the FTS index (deliberately not stored; re-derived on load)",
		},
		Rationale: "The comparison unit is the portable snapshot envelope " +
			"(core/graphstore/snapshot.go -> core/model.Graph.Marshal). Anything persisted " +
			"OUTSIDE model.Graph is invisible to it, and the FTS index is deliberately not " +
			"stored (core/graphstore/snapshot.go:49-51).",
		Disposition: "Already dispositioned DOCUMENTED HARMLESS (Labs tier) at kill point K4, " +
			"docs/adr/0004-ingest-recovery-disposition.md:37. This report cites that " +
			"disposition and does not reopen it: extending the snapshot envelope would bump " +
			"SnapshotFormatVersion, which is a product-byte change.",
	}
}

// Report is the machine-readable matrix outcome.
type Report struct {
	SchemaVersion  int        `json:"schema_version"`
	HarnessVersion string     `json:"harness_version"`
	Provenance     Provenance `json:"provenance"`
	// MaxTier and Classes filter record the SHAPE of this invocation, so a
	// partial run can never be mistaken for a full one.
	MaxTier      int      `json:"max_tier"`
	ClassFilter  []string `json:"class_filter,omitempty"`
	MatrixSource string   `json:"matrix_source"`

	Repos       []RepoRef       `json:"repos"`
	Classes     []ClassResult   `json:"classes"`
	StoreCounts []StoreCounts   `json:"store_counts"`
	NotCompared NotComparedNote `json:"not_compared"`

	// Complete is true only when EVERY declared, non-deferred change class has
	// a PASS or FAIL verdict. A run with a SKIPPED row is incomplete.
	Complete bool `json:"complete"`
	// Publishable is Complete AND every provenance precondition held. It is
	// deliberately separate from Outcome: a FAIL is publishable evidence; an
	// unattributed or incomplete run is not.
	Publishable bool `json:"publishable"`
	// NotPublishableBecause lists every failed precondition, so a refusal is
	// actionable rather than a bare "no".
	NotPublishableBecause []string `json:"not_publishable_because,omitempty"`
	// Outcome is PASS / FAIL / INCOMPLETE over the whole run.
	Outcome string `json:"outcome"`
	// GateNote states what this report does and does not settle.
	GateNote string `json:"gate_note"`
}

// VerdictSet returns the id -> verdict map, which is what AC-17's
// two-dispatches-agree check compares. It deliberately excludes timings,
// digests and durations: two dispatches must agree on VERDICTS, and requiring
// byte-identical reports would make an irrelevant timestamp difference look
// like a disagreement.
func (r Report) VerdictSet() map[string]string {
	out := make(map[string]string, len(r.Classes))
	for _, c := range r.Classes {
		out[c.ID] = c.Verdict
	}
	return out
}

// VerdictSetDigest renders the verdict set as a stable, comparable string.
func (r Report) VerdictSetDigest() string {
	vs := r.VerdictSet()
	ids := make([]string, 0, len(vs))
	for id := range vs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := ""
	for _, id := range ids {
		out += id + "=" + vs[id] + ";"
	}
	return out
}

// Finalize computes Complete, Publishable, Outcome and GateNote from the rows
// and the provenance. It is the ONLY place a run's verdict is decided, so the
// fail-closed rules live in one auditable function.
//
// declaredChangeClasses is the number of kind: "change_class" rows in
// docs/rc/parity-classes.yaml, passed in so the report cannot silently consider
// itself complete over a class list it shortened.
func (r *Report) Finalize(declaredChangeClasses int) {
	r.SchemaVersion = SchemaVersion
	r.HarnessVersion = HarnessVersion
	if r.NotCompared.Rationale == "" {
		r.NotCompared = DefaultNotCompared()
	}

	var reasons []string
	anyFail, anyError, anySkipped := false, false, false
	decided, deferred := 0, 0
	for _, c := range r.Classes {
		if c.Kind != "change_class" {
			continue
		}
		switch c.Verdict {
		case VerdictPass, VerdictFail:
			decided++
			if c.Verdict == VerdictFail {
				anyFail = true
			}
		case VerdictDeferred:
			deferred++
		case VerdictSkipped:
			anySkipped = true
		case VerdictError:
			anyError = true
		}
	}

	// A run is complete when every declared change class is either decided or
	// legitimately deferred to another story. Deferral is a DECLARED shape in
	// the matrix YAML, not a runtime escape: a row cannot become deferred by
	// failing to run.
	//
	// The count is checked against BOTH the caller's declared total AND the
	// FR-7 constant. Checking only the caller's total would let a truncated row
	// list certify itself: two rows out of two would read "complete".
	r.Complete = declaredChangeClasses >= FR7ChangeClasses &&
		(decided+deferred == declaredChangeClasses) && !anySkipped && !anyError
	if !r.Complete {
		reasons = append(reasons, fmt.Sprintf(
			"incomplete run: %d of %d declared change classes decided, %d deferred, skipped=%v, harness-error=%v (FR-7 requires %d declared classes)",
			decided, declaredChangeClasses, deferred, anySkipped, anyError, FR7ChangeClasses))
	}
	if !r.Provenance.WorktreeClean {
		reasons = append(reasons, "dirty worktree: "+r.Provenance.WorktreeDirtyDetail)
	}
	if !r.Provenance.ProductDiffEmpty {
		reasons = append(reasons, "product tree differs from the candidate: "+r.Provenance.ProductDiffDetail)
	}
	if r.Provenance.RunnerClass == "" {
		reasons = append(reasons, "no runner class recorded: an unattributed measurement is not evidence")
	}
	for _, rp := range r.Repos {
		if rp.PinnedSHA != "" && rp.HeadSHA != "" && !shaPrefixMatch(rp.PinnedSHA, rp.HeadSHA) {
			reasons = append(reasons, fmt.Sprintf(
				"manifest pin mismatch for %s: HEAD %s does not match pinned %s", rp.Name, rp.HeadSHA, rp.PinnedSHA))
		}
	}
	for _, sc := range r.StoreCounts {
		if !sc.Pass {
			anyFail = true
		}
	}

	r.NotPublishableBecause = reasons
	r.Publishable = len(reasons) == 0

	switch {
	case anyError || anySkipped || !r.Complete:
		r.Outcome = OutcomeIncomplete
	case anyFail:
		r.Outcome = OutcomeFail
	default:
		r.Outcome = OutcomePass
	}

	r.GateNote = "This report is the SW-144 half of PRD §12.3 / FR-7. Checklist row 13 is " +
		"satisfied ONLY by SW-144 AND SW-158 together (adopted decision 4): the recovery, " +
		"crash-injection and branch-switch rows are SW-158's and are DEFERRED here. Neither " +
		"story alone may be reported as \"SW-144 done\". This report settles no §12.2 " +
		"performance gate and publishes no latency, percentile or RSS figure."
}

// shaPrefixMatch reports whether pinned is a case-insensitive prefix of head —
// the same fail-closed pin rule internal/corpus/run.go:300 shaMatches applies.
func shaPrefixMatch(pinned, head string) bool {
	if len(pinned) > len(head) {
		return false
	}
	for i := 0; i < len(pinned); i++ {
		a, b := pinned[i], head[i]
		if 'A' <= a && a <= 'Z' {
			a += 'a' - 'A'
		}
		if 'A' <= b && b <= 'Z' {
			b += 'a' - 'A'
		}
		if a != b {
			return false
		}
	}
	return true
}

// Write writes the report as indented JSON to path.
func Write(r Report, path string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("parityreport: marshal: %w", err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("parityreport: write %q: %w", path, err)
	}
	return nil
}

// Read parses a report from path (used by the two-dispatch verdict-set
// comparison, AC-17).
func Read(path string) (Report, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Report{}, fmt.Errorf("parityreport: read %q: %w", path, err)
	}
	var r Report
	if err := json.Unmarshal(raw, &r); err != nil {
		return Report{}, fmt.Errorf("parityreport: parse %q: %w", path, err)
	}
	return r, nil
}
