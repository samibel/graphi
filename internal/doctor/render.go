package doctor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// JSONSchemaVersion is the current stable version of the doctor JSON output.
// Bump only on breaking changes to the schema shape or value domain.
// Version 2 renamed the top-level `overall` object to `outcome` and the
// per-check `next_step` field to `action`.
const JSONSchemaVersion = 2

// JSONReport is the stable top-level JSON output document.
// It is the contract of record for agents and CI:
//
//	{"schema_version": 2, "outcome": {...}, "checks": [{id, category, status, message, action}]}
type JSONReport struct {
	SchemaVersion int               `json:"schema_version"`
	Outcome       JSONOutcome       `json:"outcome"`
	Checks        []JSONCheckResult `json:"checks"`
}

// JSONOutcome captures the aggregate verdict.
type JSONOutcome struct {
	Status   Status `json:"status"`
	ExitCode int    `json:"exit_code"`
}

// JSONCheckResult is the stable per-check JSON shape.
// Every field is always present; empty strings are used instead of omission.
type JSONCheckResult struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Status   Status `json:"status"`
	Message  string `json:"message"`
	Action   string `json:"action"`
}

// RenderHuman writes a concise human-readable report to w.
// It emits one line per check (plus "→ action: ..." for non-pass results that
// carry a remediation) and a single summary line.
func RenderHuman(w io.Writer, report Report) error {
	results := SortedResults(report.Results)
	for _, r := range results {
		marker := statusMarker(r.Status)
		line := fmt.Sprintf("%s [%s] %s", marker, r.ID, r.Message)
		if r.Status != StatusPass && r.Status != StatusInfo && r.Action != "" {
			line += fmt.Sprintf(" → action: %s", r.Action)
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w, FormatSummary(report)); err != nil {
		return err
	}
	return nil
}

// RenderJSON writes the stable JSON report to w.
// Output is deterministic: checks are sorted by category then id, JSON keys are
// ordered, and HTML escaping is disabled so the bytes are byte-parity stable.
func RenderJSON(w io.Writer, report Report) error {
	results := SortedResults(report.Results)
	checks := make([]JSONCheckResult, 0, len(results))
	for _, r := range results {
		checks = append(checks, JSONCheckResult{
			ID:       r.ID,
			Category: r.Category,
			Status:   r.Status,
			Message:  r.Message,
			Action:   r.Action,
		})
	}
	out := JSONReport{
		SchemaVersion: JSONSchemaVersion,
		Outcome: JSONOutcome{
			Status:   report.WorstStatus(),
			ExitCode: ExitCodeFromReport(report),
		},
		Checks: checks,
	}
	buf := &bytes.Buffer{}
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return err
	}
	// json.Encoder appends a trailing newline; preserve exactly one newline.
	b := bytes.TrimRight(buf.Bytes(), "\n")
	_, err := w.Write(append(b, '\n'))
	return err
}

func statusMarker(s Status) string {
	switch s {
	case StatusPass:
		return "✓"
	case StatusWarn:
		return "⚠"
	case StatusFail:
		return "✗"
	case StatusInfo:
		return "ℹ"
	case StatusUnverified:
		return "?"
	default:
		return "?"
	}
}

// ParseJSONReport parses a JSONReport from a reader. Useful in tests.
func ParseJSONReport(r io.Reader) (*JSONReport, error) {
	var out JSONReport
	if err := json.NewDecoder(r).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// statusWeight assigns a sort order for status display within a category.
func statusWeight(s Status) int {
	switch s {
	case StatusFail:
		return 0
	case StatusWarn:
		return 1
	case StatusUnverified:
		return 2
	case StatusInfo:
		return 3
	case StatusPass:
		return 4
	default:
		return 5
	}
}

// SortResultsBySeverity sorts results by category, then by severity descending,
// then by id. This groups failing checks first within each category.
func SortResultsBySeverity(results []CheckResult) []CheckResult {
	out := make([]CheckResult, len(results))
	copy(out, results)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		wi, wj := statusWeight(out[i].Status), statusWeight(out[j].Status)
		if wi != wj {
			return wi < wj
		}
		return strings.ToLower(out[i].ID) < strings.ToLower(out[j].ID)
	})
	return out
}
