package coverage

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// Capability is one row of the coverage matrix: a capability id, its category,
// lifecycle status, owning epic, and a short human note. ID+Category identify a
// row uniquely. For the four code-derived categories the drift guard cross-checks
// Status against the live registries.
type Capability struct {
	ID       string
	Category string
	Status   string // shipped | partial | planned
	Epic     string
	Note     string
}

// Valid statuses for a capability row.
const (
	StatusShipped = "shipped"
	StatusPartial = "partial"
	StatusPlanned = "planned"
)

// codeDerivedCategories are the categories whose rows are machine-checked against
// the live registries. Other categories (e.g. epic, feature-unit) are
// informational and ignored by the live diff.
var codeDerivedCategories = map[string]bool{
	CategoryParser:   true,
	CategoryAnalyzer: true,
	CategoryMCPTool:  true,
	CategorySurface:  true,
}

// Key returns the unique "category/id" key for a capability row.
func (c Capability) Key() string { return c.Category + "/" + c.ID }

// IsLiveStatus reports whether the status implies the capability is actually
// registered/present in code (shipped or partial), as opposed to planned.
func (c Capability) IsLiveStatus() bool {
	return c.Status == StatusShipped || c.Status == StatusPartial
}

// LoadMatrix reads and parses the checked-in coverage matrix YAML at path. It
// accepts the strict, simple block-list subset this repo emits — a top-level
// `capabilities:` sequence of flat maps with the keys id, category, status,
// epic, note — and intentionally does NOT pull in a general YAML dependency
// (graphi stays lean and CGo-free). Rows are returned sorted by (category, id);
// malformed rows are a hard error so the matrix file cannot rot silently.
func LoadMatrix(path string) ([]Capability, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("coverage: read matrix %q: %w", path, err)
	}
	caps, err := parseMatrixYAML(string(raw))
	if err != nil {
		return nil, fmt.Errorf("coverage: parse matrix %q: %w", path, err)
	}
	sortCapabilities(caps)
	return caps, nil
}

// parseMatrixYAML parses the constrained subset. Grammar (whitespace-significant):
//
//	# comments and blank lines are ignored anywhere
//	capabilities:
//	  - id: <scalar>
//	    category: <scalar>
//	    status: <scalar>
//	    epic: <scalar>
//	    note: <scalar>
//
// A new row begins at a `- ` list marker; scalars may be bare or double-quoted.
func parseMatrixYAML(text string) ([]Capability, error) {
	var (
		caps      []Capability
		cur       *Capability
		inCaps    bool
		seenField bool
	)
	flush := func() error {
		if cur == nil {
			return nil
		}
		if cur.ID == "" || cur.Category == "" || cur.Status == "" {
			return fmt.Errorf("incomplete row (need id, category, status): %+v", *cur)
		}
		if cur.Status != StatusShipped && cur.Status != StatusPartial && cur.Status != StatusPlanned {
			return fmt.Errorf("row %q has invalid status %q (want shipped|partial|planned)", cur.Key(), cur.Status)
		}
		caps = append(caps, *cur)
		cur = nil
		return nil
	}

	for lineNo, rawLine := range strings.Split(text, "\n") {
		line := stripComment(rawLine)
		if strings.TrimSpace(line) == "" {
			continue
		}
		trimmed := strings.TrimSpace(line)

		if !inCaps {
			if trimmed == "capabilities:" {
				inCaps = true
			}
			continue
		}

		if strings.HasPrefix(trimmed, "- ") {
			// Start a new row; the marker line itself carries the first field.
			if err := flush(); err != nil {
				return nil, fmt.Errorf("line %d: %w", lineNo+1, err)
			}
			cur = &Capability{}
			seenField = true
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		}
		if cur == nil {
			return nil, fmt.Errorf("line %d: field outside any list item: %q", lineNo+1, trimmed)
		}
		key, val, err := splitField(trimmed)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo+1, err)
		}
		if err := assignField(cur, key, val); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo+1, err)
		}
		_ = seenField
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if !inCaps {
		return nil, fmt.Errorf("no top-level 'capabilities:' key found")
	}
	return caps, nil
}

func assignField(c *Capability, key, val string) error {
	switch key {
	case "id":
		c.ID = val
	case "category":
		c.Category = val
	case "status":
		c.Status = val
	case "epic":
		c.Epic = val
	case "note":
		c.Note = val
	default:
		return fmt.Errorf("unknown field %q", key)
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
// double-quoted scalar. Conservative: it respects quotes so notes may contain #.
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

// sortCapabilities orders rows deterministically by (category, id).
func sortCapabilities(caps []Capability) {
	sort.Slice(caps, func(i, j int) bool {
		if caps[i].Category != caps[j].Category {
			return caps[i].Category < caps[j].Category
		}
		return caps[i].ID < caps[j].ID
	})
}
