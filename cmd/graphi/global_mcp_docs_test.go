package main

import (
	"os"
	"strings"
	"testing"
)

func TestGlobalMCPDocumentationStatesSafetyLimits(t *testing.T) {
	raw, err := os.ReadFile("../../docs/global-mcp-registration.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	for _, required := range []string{
		"--no-auto-register",
		"--adopt",
		"backup",
		"index freshness",
		"not repository isolation",
		"does not claim that a model will always choose",
		"NOT RUN",
		"Claude Code 2.1.287",
		"Codex CLI 0.157.1",
		"Devin CLI 3000.11.3",
		"non-cooperating client",
	} {
		if !strings.Contains(doc, required) {
			t.Errorf("global MCP documentation omits %q", required)
		}
	}
	for _, private := range []string{"/Users/", "/home/", "sk-"} {
		if strings.Contains(doc, private) {
			t.Errorf("global MCP documentation contains private-looking marker %q", private)
		}
	}
}
