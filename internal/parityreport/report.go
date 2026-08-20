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

	"github.com/samibel/graphi/core/profile"
)

// SchemaVersion versions this report envelope. Bump when a field is added,
// removed or re-meaned, so a consumer can tell whether what it must parse
// changed.
//
// 1 -> 2 (SW-158): the three LIFECYCLE rows. ClassResult gained KillPoint,
// RefA/RefB, Repetitions, DistinctIncDigests, Reproducible, CoverageLimit and
// Control; Report gained CoverageLimits; and Finalize now scores
// kind: "crash_condition" rows instead of ignoring them. Every addition is
// additive, so a schema-1 reader still parses a schema-2 document — but it would
// silently miss a FAILING recovery row, which is exactly why the version moves.
const SchemaVersion = 2

// HarnessVersion identifies the harness that produced a report. It is recorded
// in every report so a verdict can never be attributed to the wrong harness.
// "/2" is the SW-158 harness: the same change-class rows plus the three
// lifecycle rows.
const HarnessVersion = "parity-matrix/2"

// KindChangeClass and KindCrashCondition mirror docs/rc/parity-classes.yaml's
// `kind` vocabulary as WIRE VALUES. They are declared here because Finalize
// scores the two kinds differently and must not do so on a bare string literal:
// FR-7's completeness count is over change classes ONLY, while a crash condition
// that FAILS must still fail the run. Conflating the two is what produced
// backlog.md:55's "16 change classes".
const (
	KindChangeClass    = "change_class"
	KindCrashCondition = "crash_condition"
)

// CandidateSHA is the candidate the product tree is compared against.
//
// CANDIDATE MOVE (2026-08-20, deliberate, SW-188): the previous candidate was
// 3b8d43f (the ADR 0011 commit), which superseded 7574a49 (the ADR 0010
// commit), which superseded c4209dd (the ADR 0009 merge), which superseded
// the P0 v0.7.1 SHA 80d67ed586723ab22704cf7aada316138cb1360e. ADR 0013 (the
// SW-188 closure of JVMSOUND-003, JVMSOUND-004 and JVMHARN-001: comment
// nodes in `argument_list` are not arguments; `callableSig` carries array
// dimensionality; the Kotlin value-class name mangling `foo-<hash>` is
// recognized as a binding shape) changes product bytes on the JVM tier
// (anything depending on `engine/jvmresolve` — most of it, once
// `GRAPHI_JVM_TYPERESOLVE` is on) — so the candidate moves again by the same
// rule, and every measurement against 3b8d43f is historical.
//
// WHY THIS SHA. The SW-188 closure lands in a single commit (the code fix,
// the ADR, the positive regression tests, and the pin-test skip stubs all
// ride together) so the candidate is the obvious one: that commit. The
// commit SHA is 9f68784, and `./cmd/graphi` built with
// `-trimpath -buildvcs=false` at 9f68784 is the boundary the provenance
// gate reads verbatim. The ADR-0011 candidate's wording is now retired; the
// new provenance sentence names the ADR 0013 candidate, and the old wording
// is in the forbidden-phrasing list at `internal/parity/parity_test.go:651`.
//
// Each move is recorded before its first published measurement:
// docs/decisions/2026-08-parity-candidate-move-adr0013.md, which cites its
// ADR-0011 predecessor. The provenance statement keeps run SHA and candidate
// SHA separate, because a run may happen at any later commit whose product
// bytes are identical to the candidate's.
const CandidateSHA = "9f687849cec2b26311401191e90b60e40b5f6cee"

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
	// CandidateSHA is the frozen candidate the product tree is compared
	// AGAINST (the ADR 0010 fix commit since the second 2026-08-16 candidate
	// move). It is not necessarily the SHA the run happened at.
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
	// IndexProfile is the INDEX PROFILE the measured passes ran under (ADR
	// 0010 review round 1, finding 7). The harness passes no -profile so it
	// measures the product's resolved DEFAULT, and clears
	// GRAPHI_INDEX_PROFILE for the child so an inherited value cannot silently
	// change what was measured — this field is what makes that checkable from
	// the artifact instead of trusted.
	IndexProfile string `json:"index_profile"`
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
// caller's prose. The run may happen at any commit; what is claimed is never
// "the run happened at the candidate" but that the PRODUCT SOURCE (decided by
// the built-binary comparison) is byte-identical to it. Both SHAs are recorded
// so the claim is checkable.
func NewProvenance(runSHA string) Provenance {
	return Provenance{
		CandidateSHA:   CandidateSHA,
		RunSHA:         runSHA,
		IndexProfile:   string(profile.Balanced) + " (the product's resolved default; GRAPHI_INDEX_PROFILE cleared for the measured child processes)",
		Statement:      "product source byte-identical to the ADR 0013 candidate at " + CandidateSHA,
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
	// SourceFiles is the manifest's primary-language file count, used by
	// non-Go families where GoFiles is 0 by construction (WP-J7 / SW-176).
	SourceFiles int `json:"source_files,omitempty"`
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
	// AxisNote states, IN THE ROW, which cell of a family's axis crossing this
	// row ran under (WP-J7 / SW-176: binder off/on x profile default/fast).
	//
	// It is carried in the row and not only in the id because an id suffix is a
	// label a reader must decode, while this sentence says what was actually
	// configured. A JVM row measured with the declared-type binder OFF proves
	// nothing about the binder, and that must be legible from the artifact
	// without knowing the harness.
	AxisNote string `json:"axis_note,omitempty"`
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

	// ------------------------------------------------------------------
	// LIFECYCLE ROWS (SW-158). Empty on every change-class row.
	// ------------------------------------------------------------------

	// KillPoint cites the docs/adr/0004-ingest-recovery-disposition.md kill
	// point(s) this row places its signal at, in the ADR's OWN vocabulary
	// (K1…K8). AC-4 requires the citation rather than a parallel naming: a row
	// that invented its own boundary names would make the real-process
	// complement incomparable with the synthetic matrix it complements.
	KillPoint string `json:"kill_point,omitempty"`
	// RefA and RefB are the two git refs a branch-switch row moved between:
	// A is indexed, B is checked out, and the sync from A to B is what must
	// converge. Both are recorded (AC-5) because a row naming only its
	// destination is not reproducible.
	RefA string `json:"ref_a,omitempty"`
	RefB string `json:"ref_b,omitempty"`
	// Repetitions is EVERY execution of this row, not a summary of them.
	//
	// A lifecycle row is repeated because a single passing execution is not
	// evidence of convergence: PARITY-002 is already known to be
	// non-deterministic on grpc-go (three distinct incremental snapshots over
	// six executions of one pinned tree), so one green run proves only that
	// one green run happened. The row's verdict is FAIL if ANY repetition
	// diverged — a repetition is never retried into green (AC-8).
	Repetitions []Repetition `json:"repetitions,omitempty"`
	// DistinctIncDigests counts how many DIFFERENT incremental snapshot
	// digests the repetitions produced. 1 means the row reproduced; more means
	// the measurement itself varies and no single figure may be quoted.
	DistinctIncDigests int `json:"distinct_inc_digests,omitempty"`
	// DistinctFullDigests is the same count over the fresh-full side, which
	// separates "the product is non-deterministic on the incremental path"
	// from "the product is non-deterministic at all".
	DistinctFullDigests int `json:"distinct_full_digests,omitempty"`
	// Reproducible is DistinctIncDigests == 1 && DistinctFullDigests == 1 over
	// at least two repetitions. It is reported alongside the verdict and never
	// instead of it: a row can be reproducibly FAILING.
	Reproducible bool `json:"reproducible,omitempty"`
	// CoverageLimit states, IN THE ROW, why a row could not be exercised here
	// — a platform with no SIGKILL, for instance. AC-9: a skipped row must
	// appear as a disclosed limit, never as an absence.
	CoverageLimit string `json:"coverage_limit,omitempty"`
	// Control carries an UNINTERRUPTED counterpart of the same journey. It is
	// DIAGNOSIS, never the verdict: it separates "recovery did not converge"
	// from "this repository's incremental path does not converge even without a
	// crash", which are different defects and would otherwise be reported as
	// one.
	Control string `json:"control,omitempty"`
}

// Repetition is ONE execution of a lifecycle row, recorded in full.
//
// The whole sample is published rather than an aggregate. An aggregate would
// hide precisely what the SW-144 matrix already found on grpc-go: that the
// quantity beneath a stable verdict can itself vary run to run.
type Repetition struct {
	// N is the 1-based repetition index.
	N int `json:"n"`
	// KillPointID names the harness's kill point (e.g. "parse", "resolve"),
	// and ADRKillPoint the docs/adr/0004 kill point it lands at.
	KillPointID  string `json:"kill_point_id,omitempty"`
	ADRKillPoint string `json:"adr_kill_point,omitempty"`
	// ObservedPhase is the phase the binary's OWN progress stream had announced
	// when the signal was sent — the honest answer to "where did the kill
	// land?", read from outside the process rather than assumed. A row that
	// cannot say where it killed cannot say what it proved.
	ObservedPhase string `json:"observed_phase,omitempty"`
	// KillLanded is false when the pass finished before the marker arrived. It
	// is never absorbed: a repetition that did not exercise the condition makes
	// the row an ERROR, because a journey that never crashed cannot be evidence
	// that a crash recovers.
	KillLanded bool `json:"kill_landed"`
	// LockDuringPass and LockAfterKill are internal/ingestlock probe states of
	// the REAL cross-process ingest lock (meta/ingest.lock.db) taken from this
	// process while the subprocess was mid-pass and again after it died. "held"
	// then "free" is the cross-process evidence AC-2 asks for: the lock is a
	// SQLite file lock that the OS drops with the process, unlike the durable
	// recovery state, which survives it.
	LockDuringPass string `json:"lock_during_pass,omitempty"`
	LockAfterKill  string `json:"lock_after_kill,omitempty"`
	// CrashedNodes/CrashedEdges are the shape of the store AS THE KILLED
	// PROCESS LEFT IT, read before anything recovers it. They are the
	// independent corroboration of the ADR kill-point mapping: a full pass
	// killed at K1 must have committed no graph batch (0 nodes), while one
	// killed at K3 must have committed the WRITE and LINK batches (nodes
	// present). Without them the mapping would rest on the progress stream
	// alone. CrashedStoreNote carries the reason when the crashed store cannot
	// be read at all, which is itself a legitimate outcome of a SIGKILL and is
	// recorded rather than hidden.
	CrashedNodes     int    `json:"crashed_nodes,omitempty"`
	CrashedEdges     int    `json:"crashed_edges,omitempty"`
	CrashedStoreNote string `json:"crashed_store_note,omitempty"`
	// The two compared envelopes and their shapes.
	SnapshotIncSHA256  string `json:"snapshot_inc_sha256,omitempty"`
	SnapshotFullSHA256 string `json:"snapshot_full_sha256,omitempty"`
	IncNodes           int    `json:"inc_nodes,omitempty"`
	IncEdges           int    `json:"inc_edges,omitempty"`
	FullNodes          int    `json:"full_nodes,omitempty"`
	FullEdges          int    `json:"full_edges,omitempty"`
	// Equal is the assertion: byte equality of the two envelopes.
	Equal bool `json:"equal"`
	// Detail explains a non-Equal or non-Landed repetition.
	Detail     string `json:"detail,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

// CoverageLimit is a run-level disclosure that something the matrix claims to
// cover was NOT exercised on this run, and why.
//
// It exists so AC-9's rule has a place to be true in the machine-readable
// artifact and not only in the prose: a row that skipped must be findable by a
// reader who never opens the markdown.
type CoverageLimit struct {
	Row      string `json:"row"`
	Platform string `json:"platform,omitempty"`
	Reason   string `json:"reason"`
}

// StoreCounts are the two PRD §12.3 store-level counts, taken over the REAL
// repository graph after the class's change sequence — not inferred from the
// fixture-level proofs at engine/ingest/link_external_lifecycle_e2e_test.go:29
// and link_cascade_test.go:118.
type StoreCounts struct {
	Repo string `json:"repo"`
	// Class is the row whose final tree these counts were taken over.
	Class string `json:"class"`
	// Side names WHICH graph was counted: "full" (the fresh rebuild) or
	// "incremental" (the synced store).
	//
	// It exists because the first cut of this harness counted the REBUILD SIDE
	// ONLY and did not say so — the incremental graph was decoded and never
	// passed to the counter. No published figure was wrong (both sides read
	// 0/0), but "orphaned external nodes = 0" was silently a statement about one
	// of the two graphs the row compares, which is precisely the class of
	// undisclosed scope this record exists to prevent. Both sides are now
	// counted and both are labelled.
	Side string `json:"side"`
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
	// Family names the language family this matrix ran over: empty (or "go")
	// for the PRD FR-7 Go matrix, "jvm" for the WP-J7 JVM one (SW-176).
	//
	// It exists so the two report families cannot be confused by a reader or by
	// a tool: they cite DIFFERENT matrix sources, cover different change
	// classes, and a JVM report settles none of FR-7's rows. Before this field
	// the only discriminator was MatrixSource, which a human comparing two
	// JSON files would plausibly skim past.
	Family string `json:"family,omitempty"`
	// MaxTier and Classes filter record the SHAPE of this invocation, so a
	// partial run can never be mistaken for a full one.
	MaxTier      int      `json:"max_tier"`
	ClassFilter  []string `json:"class_filter,omitempty"`
	MatrixSource string   `json:"matrix_source"`

	Repos       []RepoRef       `json:"repos"`
	Classes     []ClassResult   `json:"classes"`
	StoreCounts []StoreCounts   `json:"store_counts"`
	NotCompared NotComparedNote `json:"not_compared"`
	// CoverageLimits lists every row that did not run here and why (AC-9). A
	// non-empty list also makes the run incomplete: the limits are disclosed
	// AND they cost publishability, so disclosure is not a way to publish
	// anyway.
	CoverageLimits []CoverageLimit `json:"coverage_limits,omitempty"`

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

// CountsSet returns the id -> counts line map for the Wave-0 COUNTS gate.
//
// WHY THIS EXISTS WHEN VerdictSet ALREADY DOES: the published PARITY-002
// record states outright that `-verdict-diff` was structurally blind to the
// grpc-go non-determinism — six executions agreed the row FAILs while the
// incremental EDGE COUNT flapped between them (69939/69940 against a stable
// full 69772). Two dispatches agreeing on verdicts therefore proves nothing
// about determinism; agreeing on per-row node/edge counts AND snapshot digests
// does. Repo and verdict are included so a count can never be compared across
// two rows that silently ran on different repositories or in different
// outcomes.
//
// Like VerdictSet it deliberately excludes timings, durations, clone paths and
// diagnostics — differences there are environment noise, not measurement.
func (r Report) CountsSet() map[string]string {
	out := make(map[string]string, len(r.Classes))
	for _, c := range r.Classes {
		out[c.ID] = fmt.Sprintf("repo=%s verdict=%s full=%d/%d inc=%d/%d sha_full=%s sha_inc=%s",
			c.Repo, c.Verdict, c.FullNodes, c.FullEdges, c.IncNodes, c.IncEdges,
			c.SnapshotFullSHA256, c.SnapshotIncSHA256)
	}
	return out
}

// CountsSetDigest renders the counts set as a stable, comparable string.
func (r Report) CountsSetDigest() string {
	cs := r.CountsSet()
	ids := make([]string, 0, len(cs))
	for id := range cs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := ""
	for _, id := range ids {
		out += id + "{" + cs[id] + "};"
	}
	return out
}

// Finalize computes Complete, Publishable, Outcome and GateNote from the rows
// and the provenance. It is the ONLY place a run's verdict is decided, so the
// fail-closed rules live in one auditable function.
//
// declaredChangeClasses is the number of kind: "change_class" rows in
// docs/rc/parity-classes.yaml, and declaredCrashConditions the number of
// kind: "crash_condition" rows. Both are passed in so the report cannot silently
// consider itself complete over a row list it shortened, and they are passed
// SEPARATELY because FR-7's completeness count is over change classes only —
// adding the crash conditions to it is the conflation that produced
// backlog.md:55's "16 change classes".
//
// SW-158 CHANGED THIS FUNCTION IN ONE LOAD-BEARING WAY. Crash-condition rows
// were previously skipped by the scoring loop entirely, because SW-144 had none
// that ran: a FAILING recovery row would have left Outcome reading PASS. Every
// mismatch is blocking (Delta PRD :1092), so the two kinds are now scored — with
// separate completeness counts and one shared failure signal.
func (r *Report) Finalize(declaredChangeClasses, declaredCrashConditions int) {
	r.SchemaVersion = SchemaVersion
	r.HarnessVersion = HarnessVersion
	if r.NotCompared.Rationale == "" {
		r.NotCompared = DefaultNotCompared()
	}

	var reasons []string
	anyFail, anyError, anySkipped := false, false, false
	decided, deferred := 0, 0
	crashDecided, crashDeferred := 0, 0
	for _, c := range r.Classes {
		if c.Kind != KindChangeClass && c.Kind != KindCrashCondition {
			continue
		}
		crash := c.Kind == KindCrashCondition
		switch c.Verdict {
		case VerdictPass, VerdictFail:
			if crash {
				crashDecided++
			} else {
				decided++
			}
			if c.Verdict == VerdictFail {
				anyFail = true
			}
		case VerdictDeferred:
			if crash {
				crashDeferred++
			} else {
				deferred++
			}
		case VerdictSkipped:
			anySkipped = true
		case VerdictError:
			anyError = true
		}
	}

	// A run is complete when every declared row of each kind is either decided
	// or legitimately deferred to another story. Deferral is a DECLARED shape in
	// the matrix YAML, not a runtime escape: a row cannot become deferred by
	// failing to run.
	//
	// The change-class count is checked against BOTH the caller's declared total
	// AND the FR-7 constant. Checking only the caller's total would let a
	// truncated row list certify itself: two rows out of two would read
	// "complete".
	classesComplete := declaredChangeClasses >= FR7ChangeClasses &&
		(decided+deferred == declaredChangeClasses)
	crashComplete := crashDecided+crashDeferred == declaredCrashConditions
	r.Complete = classesComplete && crashComplete && !anySkipped && !anyError
	if !r.Complete {
		reasons = append(reasons, fmt.Sprintf(
			"incomplete run: %d of %d declared change classes decided, %d deferred; "+
				"%d of %d declared crash conditions decided, %d deferred; skipped=%v, harness-error=%v (FR-7 requires %d declared classes)",
			decided, declaredChangeClasses, deferred,
			crashDecided, declaredCrashConditions, crashDeferred, anySkipped, anyError, FR7ChangeClasses))
	}
	// A disclosed coverage limit is disclosed AND costs publishability. AC-9
	// requires a platform skip to be visible in the report; it does not make the
	// run publishable, or "record the limit" would become the cheap way past the
	// gate.
	for _, cl := range r.CoverageLimits {
		reasons = append(reasons, "coverage limit on row "+cl.Row+" ("+cl.Platform+"): "+cl.Reason)
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

	r.GateNote = "PRD §12.3 / FR-7, both halves. Checklist row 13 is satisfied ONLY by " +
		"SW-144 AND SW-158 TOGETHER (adopted decision 4) — SW-144 contributed the 15 " +
		"change-class rows, SW-158 the branch-switch, interrupted-full-pass and " +
		"restart-and-recovery rows. NEITHER STORY ALONE WAS, OR MAY BE REPORTED AS, " +
		"\"SW-144 done\". This report settles no §12.2 performance gate and publishes no " +
		"latency, percentile or RSS figure. It also does NOT claim WP6: that gate's " +
		"\"recovery/crash-fault suite 100% green\" conjunct (docs/rc/evidence-index.yaml:125-135) " +
		"is one conjunct of a 90-day threshold whose clock has not started, so these rows are " +
		"an input to it and never a substitute for it."
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
