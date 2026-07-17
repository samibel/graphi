// Package evidence is the M0-05 (SW-119) evidence-index + gate-dashboard tool. It
// turns a tracked YAML source of truth (docs/rc/evidence-index.yaml) into a
// human-readable gate dashboard (docs/rc/evidence-index.md) carrying the plan §7
// columns, and enforces one honesty rule that mechanizes WP0's gate:
//
//	"Jede 9/10-Behauptung lässt sich auf ein versioniertes Rohartefakt
//	 zurückführen. Kein manuell gepflegtes „grün" ohne Beleg."
//	(plan §6 WP0)
//
// The rule: a row may only read PASS if it carries BOTH a non-empty Evidence URI
// AND a non-empty SHA/Digest. A gate with no explicit status defaults to UNKNOWN,
// so a newly added gate starts honest and must be argued green. UNKNOWN counts as
// not passed (plan §2.4) — it is never rendered as PASS.
//
// It mirrors internal/coverage: a checked-in YAML source, a generated Markdown
// artifact, a pure RenderMarkdown so freshness is a byte compare, and a -check /
// -generate cmd. Like layerguard and coverage it is UNRANKED CI tooling (it sits
// outside cmd→surfaces→engine→core), imports only the standard library, and adds
// no dependency to the default build path.
//
// Determinism is a hard requirement: rows render in the source-file order (which
// follows the plan: WP0…WP10, then the M0…M5 milestone exit gates), no map
// iteration touches the output, so regenerating twice is byte-identical.
package evidence

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Repo-relative paths to the checked-in evidence-index source and rendered table.
const (
	EvidenceYAMLPath = "docs/rc/evidence-index.yaml"
	EvidenceMDPath   = "docs/rc/evidence-index.md"
)

// Gate status values. These are exactly the plan §7 dashboard states. A blank
// status normalizes to StatusUnknown at load, so a new gate starts honest.
const (
	StatusPass    = "PASS"
	StatusFail    = "FAIL"
	StatusUnknown = "UNKNOWN"
)

// Candidate cites the frozen candidate from SW-116's record rather than restating
// it. ReleaseDigest mirrors that record: where no published digest is bound to the
// candidate it reads UNKNOWN, and the dashboard must not disagree with SW-116.
type Candidate struct {
	Source        string // the SW-116 record this is cited from
	SHA           string // candidate merge SHA
	ReleaseDigest string // published release digest, or UNKNOWN (never blank)
}

// Gate is one row of the dashboard: one gate drawn from the execution plan, with
// the plan §7 columns plus the plan Section it came from (each row cites its
// source). ID is a stable key (e.g. "WP0", "M0"); Gate is the human name; Section
// is the plan citation (e.g. "plan §6 WP0").
type Gate struct {
	ID          string
	Gate        string
	Section     string
	Threshold   string
	Current     string
	Status      string
	EvidenceURI string
	SHA         string
	Owner       string
	NextAction  string
	Due         string
}

// Index is the whole evidence index: the cited candidate plus the gate rows in
// source-file (plan) order.
type Index struct {
	Candidate Candidate
	Gates     []Gate
}

// Load reads and parses the checked-in evidence-index YAML at path. It accepts the
// same strict, dependency-free block subset internal/coverage uses (so the default
// build path gains no YAML dependency). A blank gate status is normalized to
// UNKNOWN. Structural problems are a hard error so the source cannot rot silently.
func Load(path string) (Index, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Index{}, fmt.Errorf("evidence: read index %q: %w", path, err)
	}
	idx, err := parseIndexYAML(string(raw))
	if err != nil {
		return Index{}, fmt.Errorf("evidence: parse index %q: %w", path, err)
	}
	return idx, nil
}

// parseIndexYAML parses the constrained subset. Grammar (whitespace-significant;
// comments and blank lines ignored anywhere; scalars bare or double-quoted):
//
//	candidate:
//	  source: <scalar>
//	  sha: <scalar>
//	  release_digest: <scalar>
//	gates:
//	  - id: <scalar>
//	    gate: <scalar>
//	    section: <scalar>
//	    threshold: <scalar>
//	    current: <scalar>
//	    status: <scalar>
//	    evidence_uri: <scalar>
//	    sha: <scalar>
//	    owner: <scalar>
//	    next_action: <scalar>
//	    due: <scalar>
//
// A top-level `key:` with no value opens a section; a `- ` marker (indented, under
// gates:) starts a new row and carries its first field.
func parseIndexYAML(text string) (Index, error) {
	var (
		idx     Index
		section string // "candidate" | "gates" | ""
		cur     *Gate
	)
	flush := func() {
		if cur != nil {
			if cur.Status == "" {
				cur.Status = StatusUnknown
			} else {
				cur.Status = strings.ToUpper(cur.Status)
			}
			idx.Gates = append(idx.Gates, *cur)
			cur = nil
		}
	}

	for lineNo, rawLine := range strings.Split(text, "\n") {
		line := stripComment(rawLine)
		if strings.TrimSpace(line) == "" {
			continue
		}
		indented := line[0] == ' ' || line[0] == '\t'
		trimmed := strings.TrimSpace(line)

		// A top-level (unindented) `key:` line opens a section.
		if !indented {
			flush()
			switch trimmed {
			case "candidate:":
				section = "candidate"
			case "gates:":
				section = "gates"
			default:
				return Index{}, fmt.Errorf("line %d: unexpected top-level key %q (want candidate: or gates:)", lineNo+1, trimmed)
			}
			continue
		}

		switch section {
		case "candidate":
			key, val, err := splitField(trimmed)
			if err != nil {
				return Index{}, fmt.Errorf("line %d: %w", lineNo+1, err)
			}
			if err := assignCandidate(&idx.Candidate, key, val); err != nil {
				return Index{}, fmt.Errorf("line %d: %w", lineNo+1, err)
			}
		case "gates":
			if strings.HasPrefix(trimmed, "- ") {
				flush()
				cur = &Gate{}
				trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
			}
			if cur == nil {
				return Index{}, fmt.Errorf("line %d: field outside any gate item: %q", lineNo+1, trimmed)
			}
			key, val, err := splitField(trimmed)
			if err != nil {
				return Index{}, fmt.Errorf("line %d: %w", lineNo+1, err)
			}
			if err := assignGate(cur, key, val); err != nil {
				return Index{}, fmt.Errorf("line %d: %w", lineNo+1, err)
			}
		default:
			return Index{}, fmt.Errorf("line %d: field before any section header: %q", lineNo+1, trimmed)
		}
	}
	flush()
	return idx, nil
}

func assignCandidate(c *Candidate, key, val string) error {
	switch key {
	case "source":
		c.Source = val
	case "sha":
		c.SHA = val
	case "release_digest":
		c.ReleaseDigest = val
	default:
		return fmt.Errorf("unknown candidate field %q", key)
	}
	return nil
}

func assignGate(g *Gate, key, val string) error {
	switch key {
	case "id":
		g.ID = val
	case "gate":
		g.Gate = val
	case "section":
		g.Section = val
	case "threshold":
		g.Threshold = val
	case "current":
		g.Current = val
	case "status":
		g.Status = val
	case "evidence_uri":
		g.EvidenceURI = val
	case "sha":
		g.SHA = val
	case "owner":
		g.Owner = val
	case "next_action":
		g.NextAction = val
	case "due":
		g.Due = val
	default:
		return fmt.Errorf("unknown gate field %q", key)
	}
	return nil
}

// splitField splits "key: value" and unquotes a double-quoted value.
func splitField(s string) (key, val string, err error) {
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return "", "", fmt.Errorf("not a key: value field: %q", s)
	}
	key = strings.TrimSpace(s[:i])
	val = strings.TrimSpace(s[i+1:])
	if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
		val = val[1 : len(val)-1]
	}
	return key, val, nil
}

// stripComment removes a trailing/whole-line "# ..." comment that is not inside a
// double-quoted scalar, so a note may contain a #.
func stripComment(line string) string {
	inQuote := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			inQuote = !inQuote
		case '#':
			if !inQuote {
				return line[:i]
			}
		}
	}
	return line
}

// ModuleRoot resolves the module root directory (via `go env GOMOD`), exported so
// cmd/evidence can locate the checked-in files from any working directory under
// the module. Mirrors internal/coverage and internal/layerguard.
func ModuleRoot() (string, error) { return moduleRoot() }

var moduleRoot = sync.OnceValues(func() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", err
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == "/dev/null" {
		return "", fmt.Errorf("evidence: no go.mod found (GOMOD=%q)", gomod)
	}
	return filepath.Dir(gomod), nil
})
