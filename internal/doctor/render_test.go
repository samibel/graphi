package doctor

import (
	"bytes"
	"encoding/json"
	"testing"
)

func allStatuses() []CheckResult {
	return []CheckResult{
		{ID: "pass-1", Category: "cat-a", Status: StatusPass, Message: "ok", NextStep: ""},
		{ID: "warn-1", Category: "cat-b", Status: StatusWarn, Message: "warned", NextStep: "fix it"},
		{ID: "fail-1", Category: "cat-a", Status: StatusFail, Message: "failed", NextStep: "run graphi setup"},
		{ID: "info-1", Category: "cat-b", Status: StatusInfo, Message: "note"},
		{ID: "unverified-1", Category: "cat-a", Status: StatusUnverified, Message: "unknown", NextStep: "investigate"},
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
		t.Fatalf("schema_version: got %q, want %q", got.SchemaVersion, JSONSchemaVersion)
	}
	if got.Overall.Status != StatusFail {
		t.Fatalf("overall.status: got %q, want fail", got.Overall.Status)
	}
	if got.Overall.ExitCode != 1 {
		t.Fatalf("overall.exit_code: got %d, want 1", got.Overall.ExitCode)
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
	if !bytes.Contains(buf.Bytes(), []byte("next step")) {
		t.Fatalf("human output missing next step: %s", out)
	}
}

func TestRenderHumanNoNextStepForPass(t *testing.T) {
	buf := &bytes.Buffer{}
	report := Report{Results: []CheckResult{{ID: "p", Category: "c", Status: StatusPass, Message: "ok"}}}
	if err := RenderHuman(buf, report); err != nil {
		t.Fatalf("RenderHuman: %v", err)
	}
	if bytes.Contains(buf.Bytes(), []byte("next step")) {
		t.Fatalf("pass status should not print next step: %s", buf.String())
	}
}
