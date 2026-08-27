package conformance

import (
	"errors"
	"fmt"
	"strings"
)

// Check ids. They are stable strings because a report is something a CI job
// greps and an author quotes in a bug report.
const (
	// CheckSpec: the operation spec is internally valid and complete.
	CheckSpec = "spec"
	// CheckPermissions: the contribution stays inside the read-only envelope
	// ADR 0013 I3 fixes for V1 extensions.
	CheckPermissions = "permissions"
	// CheckAPIVersion: this host's API version falls inside the declared range.
	CheckAPIVersion = "api-version"
	// CheckSurfaceMetadata: the spec carries everything a surface projection
	// needs in order to advertise it.
	CheckSurfaceMetadata = "surface-metadata"
	// CheckSurfaceProjection: each supplied projector renders valid metadata.
	CheckSurfaceProjection = "surface-projection"
	// CheckDeterminism: the same fixture inputs produce the same bytes, twice.
	CheckDeterminism = "determinism"
	// CheckPortHonesty: the handler touched exactly the ports it declared.
	CheckPortHonesty = "port-honesty"

	// CheckManifest: the pack manifest validates, positionally.
	CheckManifest = "manifest"
	// CheckArtifactSchema: the pack artifact validates against its kind's schema.
	CheckArtifactSchema = "artifact-schema"
	// CheckMergeDeterminism: merging the pack twice produces identical bytes.
	CheckMergeDeterminism = "merge-determinism"
	// CheckProvenance: every merged item carries the pack's id, version and hash.
	CheckProvenance = "provenance"
)

// Status is a check outcome. There are two, deliberately: a harness with a
// "warning" tier is a harness whose failures can be argued with.
type Status string

const (
	// StatusPass: the check held.
	StatusPass Status = "pass"
	// StatusFail: the check did not hold.
	StatusFail Status = "fail"
)

// Result is one check's outcome.
type Result struct {
	Check  string `json:"check"`
	Status Status `json:"status"`
	// Detail says what held, or what did not and how to see it. It is filled on
	// a pass too: "deterministic across 3 fixtures, 2 runs each" is the evidence,
	// and a passing check with no evidence is indistinguishable from a check that
	// did not run.
	Detail string `json:"detail"`
}

// Report is the outcome of verifying one subject.
type Report struct {
	// Subject names what was verified — an operation id or a pack id.
	Subject string `json:"subject"`
	// Results are in the order the checks ran, which is fixed.
	Results []Result `json:"results"`
}

// OK reports whether every check passed.
func (r Report) OK() bool {
	for _, res := range r.Results {
		if res.Status != StatusPass {
			return false
		}
	}
	return len(r.Results) > 0
}

// Failures returns only the failing results.
func (r Report) Failures() []Result {
	var out []Result
	for _, res := range r.Results {
		if res.Status != StatusPass {
			out = append(out, res)
		}
	}
	return out
}

// Err returns nil when the report is clean and an aggregate error otherwise.
//
// A report with NO results is an error too. An empty run is the one outcome that
// would otherwise read as success while proving nothing, and a harness that can
// return "certified" for a subject it never examined is worse than no harness.
func (r Report) Err() error {
	if len(r.Results) == 0 {
		return fmt.Errorf("conformance: %s: no checks ran; an empty report is not a pass", r.subject())
	}
	failures := r.Failures()
	if len(failures) == 0 {
		return nil
	}
	msgs := make([]error, 0, len(failures))
	for _, f := range failures {
		msgs = append(msgs, fmt.Errorf("conformance: %s: %s: %s", r.subject(), f.Check, f.Detail))
	}
	return errors.Join(msgs...)
}

// String renders the report as one line per check.
func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", r.subject())
	for _, res := range r.Results {
		fmt.Fprintf(&b, "  %-4s %-20s %s\n", res.Status, res.Check, res.Detail)
	}
	return b.String()
}

func (r Report) subject() string {
	if r.Subject == "" {
		return "(unnamed subject)"
	}
	return r.Subject
}

// recorder accumulates results in check order.
type recorder struct {
	subject string
	results []Result
}

func (rec *recorder) pass(check, detail string) {
	rec.results = append(rec.results, Result{Check: check, Status: StatusPass, Detail: detail})
}

func (rec *recorder) fail(check, format string, a ...any) {
	rec.results = append(rec.results, Result{Check: check, Status: StatusFail, Detail: fmt.Sprintf(format, a...)})
}

// record folds an error into a result: nil is the pass, anything else the fail.
func (rec *recorder) record(check, passDetail string, err error) bool {
	if err != nil {
		rec.fail(check, "%v", err)
		return false
	}
	rec.pass(check, passDetail)
	return true
}

func (rec *recorder) report() Report {
	return Report{Subject: rec.subject, Results: rec.results}
}
