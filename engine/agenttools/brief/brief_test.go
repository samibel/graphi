package brief

import (
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/agenttools/contract"
)

func TestAssemble(t *testing.T) {
	p := Params{ProjectName: "graphi", Topic: "agent_brief"}
	r, err := Assemble(p)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	if r.Outcome != contract.OutcomeOK {
		t.Fatalf("expected outcome ok, got %s", r.Outcome)
	}
	if err := contract.ValidateResult(r); err != nil {
		t.Fatalf("ValidateResult: %v", err)
	}
	if r.Summary == "" || !strings.Contains(r.Summary, "agent_brief") {
		t.Fatalf("topic not reflected in summary: %s", r.Summary)
	}
	if len(r.Items) == 0 {
		t.Fatal("expected non-empty items")
	}
}

func TestMarkdown(t *testing.T) {
	p := Params{ProjectName: "graphi"}
	r, err := Assemble(p)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	md, err := Markdown(r)
	if err != nil {
		t.Fatalf("Markdown: %v", err)
	}
	for _, heading := range []string{"# Agent Brief", "## Project identity", "## Start-here files", "## Key symbols", "## Known facts", "## Hotspots", "## Suggested next MCP calls"} {
		if !strings.Contains(md, heading) {
			t.Fatalf("markdown missing heading %q:\n%s", heading, md)
		}
	}
	if strings.Contains(md, "source-body") {
		t.Fatal("markdown should not contain source-body dumps")
	}
}

func TestMarkdownNil(t *testing.T) {
	if _, err := Markdown(nil); err == nil {
		t.Fatal("expected error for nil result")
	}
}
