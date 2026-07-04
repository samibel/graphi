package doctor

import (
	"bytes"
	"encoding/json"
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
