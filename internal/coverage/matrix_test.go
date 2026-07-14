package coverage

import (
	"strings"
	"testing"
)

func TestParseMatrixYAML_RoundTrip(t *testing.T) {
	in := `# a comment
capabilities:
  - id: go
    category: parser
    status: shipped
    tier: labs
    epic: EP-001
    note: "Go AST extractor; note with a # hash and a | pipe"
  - id: html
    category: parser
    status: planned
    tier: disabled
    epic: EP-001
    note: "deferred"
`
	caps, err := parseMatrixYAML(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(caps) != 2 {
		t.Fatalf("want 2 rows, got %d: %+v", len(caps), caps)
	}
	sortCapabilities(caps)
	if caps[0].ID != "go" || caps[0].Status != StatusShipped || caps[0].Tier != TierLabs || caps[0].Epic != "EP-001" {
		t.Errorf("row 0 wrong: %+v", caps[0])
	}
	if !strings.Contains(caps[0].Note, "# hash") || !strings.Contains(caps[0].Note, "| pipe") {
		t.Errorf("note quoting/escaping lost: %q", caps[0].Note)
	}
	if caps[1].ID != "html" || caps[1].Status != StatusPlanned || caps[1].Tier != TierDisabled {
		t.Errorf("row 1 wrong: %+v", caps[1])
	}
}

func TestParseMatrixYAML_Rejects(t *testing.T) {
	cases := map[string]string{
		"missing status": "capabilities:\n  - id: x\n    category: parser\n    tier: labs\n",
		"invalid status": "capabilities:\n  - id: x\n    category: parser\n    status: bogus\n    tier: labs\n",
		"missing tier":   "capabilities:\n  - id: x\n    category: parser\n    status: shipped\n",
		"invalid tier":   "capabilities:\n  - id: x\n    category: parser\n    status: shipped\n    tier: bogus\n",
		"unknown field":  "capabilities:\n  - id: x\n    category: parser\n    status: shipped\n    tier: labs\n    wat: y\n",
		"no top key":     "nope:\n  - id: x\n",
	}
	for name, in := range cases {
		if _, err := parseMatrixYAML(in); err == nil {
			t.Errorf("%s: expected parse error, got nil", name)
		}
	}
}
