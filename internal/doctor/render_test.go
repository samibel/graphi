package doctor

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func allStatuses() []CheckResult {
	return []CheckResult{
		{ID: "pass-1", Category: "cat-a", Status: StatusPass, Message: "ok", Action: ""},
		{ID: "warn-1", Category: "cat-b", Status: StatusWarn, Message: "warned", Action: "fix it"},
		{ID: "fail-1", Category: "cat-a", Status: StatusFail, Message: "failed", Action: "run graphi setup"},
		{ID: "info-1", Category: "cat-b", Status: StatusInfo, Message: "note"},
		{ID: "unverified-1", Category: "cat-a", Status: StatusUnverified, Message: "unknown", Action: "investigate"},
	}
}

func TestRenderJSONSchema(t *testing.T) {
	buf := &bytes.Buffer{}
	report := Report{Results: allStatuses()}
	if err := RenderJSON(buf, report); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	var got JSONReport
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}
	if got.SchemaVersion != JSONSchemaVersion {
		t.Fatalf("schema_version: got %d, want %d", got.SchemaVersion, JSONSchemaVersion)
	}
	if got.SchemaVersion != 2 {
		t.Fatalf("schema_version: got %d, want 2", got.SchemaVersion)
	}
	if got.Outcome.Status != StatusFail {
		t.Fatalf("outcome.status: got %q, want fail", got.Outcome.Status)
	}
	if got.Outcome.ExitCode != 1 {
		t.Fatalf("outcome.exit_code: got %d, want 1", got.Outcome.ExitCode)
	}
	if len(got.Checks) != 5 {
		t.Fatalf("checks count: got %d, want 5", len(got.Checks))
	}
	for _, c := range got.Checks {
		if c.ID == "" || c.Category == "" || c.Message == "" || !c.Status.Valid() {
			t.Fatalf("invalid check: %+v", c)
		}
	}
}

// TestRenderJSONContractKeys pins the raw JSON key names of the PRD contract:
// top-level `outcome` (not `overall`) and per-check `action` (not `next_step`).
func TestRenderJSONContractKeys(t *testing.T) {
	buf := &bytes.Buffer{}
	if err := RenderJSON(buf, Report{Results: allStatuses()}); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &top); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}
	if _, ok := top["outcome"]; !ok {
		t.Fatalf("missing top-level `outcome` key: %s", buf.String())
	}
	if _, ok := top["overall"]; ok {
		t.Fatalf("legacy top-level `overall` key must be gone: %s", buf.String())
	}
	if _, ok := top["schema_version"]; !ok {
		t.Fatalf("missing `schema_version` key: %s", buf.String())
	}
	var checks []map[string]json.RawMessage
	if err := json.Unmarshal(top["checks"], &checks); err != nil {
		t.Fatalf("parse checks: %v", err)
	}
	if len(checks) == 0 {
		t.Fatal("no checks in JSON output")
	}
	for _, c := range checks {
		for _, key := range []string{"id", "status", "message", "action"} {
			if _, ok := c[key]; !ok {
				t.Fatalf("check missing %q key: %v", key, c)
			}
		}
		if _, ok := c["next_step"]; ok {
			t.Fatalf("legacy `next_step` key must be gone: %v", c)
		}
	}
}

func TestRenderJSONDeterministic(t *testing.T) {
	report := Report{Results: allStatuses()}
	var a, b bytes.Buffer
	if err := RenderJSON(&a, report); err != nil {
		t.Fatalf("render a: %v", err)
	}
	// Reorder the input deliberately.
	reordered := []CheckResult{report.Results[4], report.Results[3], report.Results[2], report.Results[1], report.Results[0]}
	if err := RenderJSON(&b, Report{Results: reordered}); err != nil {
		t.Fatalf("render b: %v", err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatalf("reordered input produced different JSON:\n%s\nvs\n%s", a.String(), b.String())
	}
}

func TestRenderHumanContainsSummary(t *testing.T) {
	buf := &bytes.Buffer{}
	report := Report{Results: allStatuses()}
	if err := RenderHuman(buf, report); err != nil {
		t.Fatalf("RenderHuman: %v", err)
	}
	out := buf.String()
	if !bytes.Contains(buf.Bytes(), []byte("status=fail")) {
		t.Fatalf("human output missing summary: %s", out)
	}
	if !bytes.Contains(buf.Bytes(), []byte("→ action: run graphi setup")) {
		t.Fatalf("human output missing action arrow: %s", out)
	}
}

// TestRenderJSONDetailOmitEmpty pins SW-159 AC5/AC6: `detail` is present when
// the check carries one and ABSENT — not `""` — when it does not, which is what
// json:"detail,omitempty" promises. The schema version stays 2 either way,
// because Detail is an existing optional member of the schema.
func TestRenderJSONDetailOmitEmpty(t *testing.T) {
	results := []CheckResult{
		{ID: "mcp", Category: "mcp", Status: StatusWarn, Message: "one or more MCP clients need attention",
			Action: "re-run `graphi setup` to update registrations", Detail: "Claude Code: not registered\nCursor: registered and current"},
		{ID: "path", Category: "path", Status: StatusPass, Message: "ok"},
	}
	buf := &bytes.Buffer{}
	if err := RenderJSON(buf, Report{Results: results}); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &top); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}
	var version int
	if err := json.Unmarshal(top["schema_version"], &version); err != nil {
		t.Fatalf("parse schema_version: %v", err)
	}
	if version != 2 {
		t.Fatalf("adding detail must NOT bump the schema: got %d, want 2", version)
	}
	var checks []map[string]json.RawMessage
	if err := json.Unmarshal(top["checks"], &checks); err != nil {
		t.Fatalf("parse checks: %v", err)
	}
	if len(checks) != 2 {
		t.Fatalf("checks count: got %d, want 2", len(checks))
	}
	byID := map[string]map[string]json.RawMessage{}
	for _, c := range checks {
		var id string
		if err := json.Unmarshal(c["id"], &id); err != nil {
			t.Fatalf("parse id: %v", err)
		}
		byID[id] = c
	}
	raw, ok := byID["mcp"]["detail"]
	if !ok {
		t.Fatalf("mcp check must carry `detail`: %s", buf.String())
	}
	var detail string
	if err := json.Unmarshal(raw, &detail); err != nil {
		t.Fatalf("parse detail: %v", err)
	}
	if detail != "Claude Code: not registered\nCursor: registered and current" {
		t.Fatalf("detail round-trip: %q", detail)
	}
	if _, ok := byID["path"]["detail"]; ok {
		t.Fatalf("an empty detail must be omitted, not rendered as \"\": %s", buf.String())
	}
}

// TestRenderHumanIndentsDetailBeneathCheck pins SW-159 AC4: Detail renders
// beneath its check line, indented, and a check without Detail keeps the
// existing single-line format.
func TestRenderHumanIndentsDetailBeneathCheck(t *testing.T) {
	results := []CheckResult{
		{ID: "mcp", Category: "mcp", Status: StatusWarn, Message: "one or more MCP clients need attention",
			Action: "re-run `graphi setup` to update registrations", Detail: "Claude Code: not registered\nCursor: registered and current"},
		{ID: "path", Category: "path", Status: StatusPass, Message: "graphi and go are on PATH"},
	}
	buf := &bytes.Buffer{}
	if err := RenderHuman(buf, Report{Results: results}); err != nil {
		t.Fatalf("RenderHuman: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("expected mcp line + 2 detail lines + path line + summary, got %d:\n%s", len(lines), buf.String())
	}
	if !strings.HasPrefix(lines[0], "⚠ [mcp] one or more MCP clients need attention") {
		t.Fatalf("mcp check line changed: %q", lines[0])
	}
	if !strings.Contains(lines[0], "→ action: re-run `graphi setup` to update registrations") {
		t.Fatalf("mcp action arrow lost: %q", lines[0])
	}
	for i, want := range []string{"Claude Code: not registered", "Cursor: registered and current"} {
		got := lines[1+i]
		if strings.TrimLeft(got, " ") != want {
			t.Fatalf("detail line %d: got %q, want %q indented", i, got, want)
		}
		if got == strings.TrimLeft(got, " ") {
			t.Fatalf("detail line %d is not indented: %q", i, got)
		}
	}
	// A check without Detail keeps its single-line format, unchanged.
	if lines[3] != "✓ [path] graphi and go are on PATH" {
		t.Fatalf("detail-free check line changed: %q", lines[3])
	}
}

func TestRenderHumanNoActionForPass(t *testing.T) {
	buf := &bytes.Buffer{}
	report := Report{Results: []CheckResult{{ID: "p", Category: "c", Status: StatusPass, Message: "ok", Action: "should not print"}}}
	if err := RenderHuman(buf, report); err != nil {
		t.Fatalf("RenderHuman: %v", err)
	}
	if bytes.Contains(buf.Bytes(), []byte("→ action")) {
		t.Fatalf("pass status should not print an action: %s", buf.String())
	}
}
