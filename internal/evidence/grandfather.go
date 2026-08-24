package evidence

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// GrandfatherPath is the repo-relative path of the AC-11 migration ratchet.
const GrandfatherPath = "docs/rc/citation-grandfather.yaml"

// GrandfatherEntry is one violation the citation rules are allowed to find and
// not fail on — for now. Every field is mandatory:
//
//	target — the exact violation key (Scope :: rule :: target) it suppresses
//	reason — why it cannot pass yet, in words a reviewer can check
//	owner  — the story id OR the filed defect id that drains it. An entry with
//	         no owner is a violation.
//
// The list is a RATCHET: it can only shrink. An entry whose target no longer
// produces a violation is UNUSED and fails -check with "delete this entry", so a
// drained entry cannot be left behind to quietly re-authorize a future breach.
type GrandfatherEntry struct {
	Target string
	Reason string
	Owner  string
	Line   int
}

// Grandfather is the parsed ratchet list.
type Grandfather struct {
	Entries []GrandfatherEntry
}

// ownerRe matches a story id: SW-NNN, with the optional letter/dot suffix the
// project uses for slices (SW-194-c, SW-194b.5).
var ownerRe = regexp.MustCompile(`^SW-[0-9]+[A-Za-z0-9.-]*$`)

// defectRe matches a FILED DEFECT id — the project's screaming-kebab shape,
// PARITY-RUBY-DRIVER-001 / PYTHONFANOUT-001 / LINK-002.
//
// Why a defect id is a legal owner. The ratchet's whole force is that someone
// is on the hook to drain the entry, and for a breach whose fix is a filed
// DEFECT rather than a scheduled story, the only honest owner is that defect.
// Requiring a story id in that case does not produce an owner — it produces a
// story id that LOOKS like one, and the nearest plausible story is the one that
// wrote the breach, which is already `done` and never lands the cited artifact.
// That is how every live entry in docs/rc/citation-grandfather.yaml came to
// point at a closed ticket (found by review of the SW-192..197 integration).
// The rule stays syntactic — this checker does not read the story tracker and
// cannot tell open from closed — but it no longer forces the lie.
var defectRe = regexp.MustCompile(`^[A-Z][A-Z0-9]*(-[A-Z0-9]+)*-[0-9]{3}$`)

// validOwner reports whether s names something that can drain an entry.
func validOwner(s string) bool {
	s = strings.TrimSpace(s)
	return ownerRe.MatchString(s) || defectRe.MatchString(s)
}

// LoadGrandfather reads the checked-in ratchet list. A missing file is legal and
// means an empty list — the end state this story is aiming at.
func LoadGrandfather(root string) (Grandfather, error) {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(GrandfatherPath)))
	if os.IsNotExist(err) {
		return Grandfather{}, nil
	}
	if err != nil {
		return Grandfather{}, fmt.Errorf("evidence: read %s: %w", GrandfatherPath, err)
	}
	return parseGrandfather(string(raw))
}

// parseGrandfather parses the same dependency-free block subset the evidence index
// uses:
//
//	entries:
//	  - target: <scalar>
//	    reason: <scalar>
//	    owner: <scalar>
func parseGrandfather(text string) (Grandfather, error) {
	var (
		g       Grandfather
		cur     *GrandfatherEntry
		section string
	)
	flush := func() {
		if cur != nil {
			g.Entries = append(g.Entries, *cur)
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
			if trimmed != "entries:" {
				return Grandfather{}, fmt.Errorf("line %d: unexpected top-level key %q (want entries:)", lineNo+1, trimmed)
			}
			section = "entries"
			continue
		}
		if section != "entries" {
			return Grandfather{}, fmt.Errorf("line %d: field before entries:", lineNo+1)
		}
		if strings.HasPrefix(trimmed, "- ") {
			flush()
			cur = &GrandfatherEntry{Line: lineNo + 1}
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		}
		if cur == nil {
			return Grandfather{}, fmt.Errorf("line %d: field outside any entry: %q", lineNo+1, trimmed)
		}
		key, val, err := splitField(trimmed)
		if err != nil {
			return Grandfather{}, fmt.Errorf("line %d: %w", lineNo+1, err)
		}
		switch key {
		case "target":
			cur.Target = val
		case "reason":
			cur.Reason = val
		case "owner":
			cur.Owner = val
		default:
			return Grandfather{}, fmt.Errorf("line %d: unknown entry field %q", lineNo+1, key)
		}
	}
	flush()
	return g, nil
}

// Validate returns the structural violations of the list itself: a blank target,
// a blank reason, a missing or malformed owner, or a duplicate target.
//
// These violations are NOT eligible for suppression: CheckCitations appends them
// after the suppression pass, so no entry can name the key of a complaint about
// the list and clear it. That ordering is the whole guarantee — Validate itself
// has no idea what is in the suppression set, and an earlier version of
// CheckCitations appended its output BEFORE the pass, which let a malformed entry
// silence the finding against itself (found by review at 1fc57ed:
// `owner: ""` plus an entry naming the resulting grandfather-no-owner key gave
// exit 0). See the ordering comment in citationcheck.go and
// TestAC11_RatchetCannotSuppressItsOwnStructuralViolations.
func (g Grandfather) Validate() []CitationViolation {
	var out []CitationViolation
	seen := map[string]bool{}
	for _, e := range g.Entries {
		scope := fmt.Sprintf("%s:%d", GrandfatherPath, e.Line)
		switch {
		case strings.TrimSpace(e.Target) == "":
			out = append(out, CitationViolation{Scope: scope, Rule: RuleGrandfatherMalformed,
				Target: "(blank)", Detail: "entry has a blank target — name the exact violation key it suppresses"})
			continue
		case strings.TrimSpace(e.Reason) == "":
			out = append(out, CitationViolation{Scope: scope, Rule: RuleGrandfatherMalformed,
				Target: e.Target, Detail: "entry has a blank reason — say why it cannot pass yet"})
		}
		if !validOwner(e.Owner) {
			out = append(out, CitationViolation{Scope: scope, Rule: RuleGrandfatherNoOwner,
				Target: e.Target, Detail: fmt.Sprintf("owner %q is neither a story id (SW-NNN) nor a filed defect id (PARITY-RUBY-DRIVER-001) — an entry nobody is on the hook for is a violation", e.Owner)})
		}
		if seen[e.Target] {
			out = append(out, CitationViolation{Scope: scope, Rule: RuleGrandfatherMalformed,
				Target: e.Target, Detail: "duplicate target — one entry per violation key"})
		}
		seen[e.Target] = true
	}
	return out
}
