package evidence

import (
	"fmt"
	"strings"
)

// Violation is one honesty-rule breach found in the evidence index, tagged with
// the gate it belongs to so CI can fail with a precise, human-readable pointer.
type Violation struct {
	GateID string
	Reason string
}

// Report is the check outcome. A report with no violations is a PASS.
type Report struct {
	Violations []Violation
}

// Pass reports whether the index is honest (no violations).
func (r Report) Pass() bool { return len(r.Violations) == 0 }

// Check enforces the honesty rules that make this dashboard trustworthy. The
// central one (plan §6 WP0) is: a PASS row MUST carry both a non-empty Evidence
// URI and a non-empty SHA/Digest — a row cannot claim green without pointing at a
// versioned artifact. It also enforces the structural invariants that keep the
// dashboard auditable: every gate cites a plan section, every status is a valid
// value, and the cited candidate carries an explicit SHA and release digest
// (UNKNOWN is allowed, blank is not — a blank digest could be read as "fine").
func Check(idx Index) Report {
	var rep Report
	add := func(id, reason string) {
		rep.Violations = append(rep.Violations, Violation{GateID: id, Reason: reason})
	}

	// The candidate must be cited explicitly, and its release digest must never be
	// blank: SW-116 records it as UNKNOWN, and a blank here could be rounded up to
	// passed. UNKNOWN is a legal, honest value; "" is not.
	if strings.TrimSpace(idx.Candidate.SHA) == "" {
		add("candidate", "candidate SHA is blank — cite the frozen candidate SHA from SW-116's record")
	}
	if strings.TrimSpace(idx.Candidate.ReleaseDigest) == "" {
		add("candidate", "candidate release_digest is blank — state UNKNOWN explicitly (SW-116); a blank digest is not a pass")
	}

	seen := map[string]bool{}
	for _, g := range idx.Gates {
		id := g.ID
		if strings.TrimSpace(id) == "" {
			add("(unnamed)", "gate has a blank id")
			continue
		}
		if seen[id] {
			add(id, "duplicate gate id")
		}
		seen[id] = true

		if strings.TrimSpace(g.Section) == "" {
			add(id, "gate does not cite its plan section (each row must name where it came from)")
		}
		switch g.Status {
		case StatusPass, StatusFail, StatusUnknown:
			// valid
		default:
			add(id, fmt.Sprintf("invalid status %q (want PASS, FAIL or UNKNOWN)", g.Status))
		}

		// THE honesty rule: a PASS must be backed by a versioned artifact.
		if g.Status == StatusPass {
			if strings.TrimSpace(g.EvidenceURI) == "" {
				add(id, "status is PASS but Evidence URI is empty — a PASS must point at a versioned artifact")
			}
			if strings.TrimSpace(g.SHA) == "" {
				add(id, "status is PASS but SHA/Digest is empty — a PASS must carry a versioned digest")
			}
		}
	}
	return rep
}

// Format renders a deterministic, human-readable report suitable for CI logs.
func (r Report) Format() string {
	if r.Pass() {
		return "evidence-index check PASS — every PASS row carries an Evidence URI and a SHA/Digest; UNKNOWN rows are honest.\n"
	}
	var b strings.Builder
	b.WriteString("evidence-index check FAILED — the index would render a claim it cannot back:\n")
	lines := make([]string, 0, len(r.Violations))
	for _, v := range r.Violations {
		lines = append(lines, fmt.Sprintf("    - [%s] %s", v.GateID, v.Reason))
	}
	// Stable order: violations are produced in index order, which is deterministic.
	b.WriteString(strings.Join(lines, "\n"))
	b.WriteString("\n\nFix docs/rc/evidence-index.yaml, then regenerate: go run ./cmd/evidence -generate\n")
	return b.String()
}
