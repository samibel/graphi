package validate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pluginDir resolves extensions/claude-plugin relative to this test file.
func pluginDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Dir(wd)
}

// repoRoot resolves the repository root (extensions/claude-plugin/validate -> ../../..).
func repoRoot(t *testing.T) string {
	t.Helper()
	return filepath.Dir(filepath.Dir(pluginDir(t)))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func stableTools(t *testing.T) []string {
	t.Helper()
	raw := readFile(t, filepath.Join(repoRoot(t), "surfaces", "mcp", "testdata", "mcp-descriptors-stdio-stable.json"))
	var fixture struct {
		Tools []struct {
			Name string `json:"name"`
			Tier string `json:"tier"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(raw), &fixture); err != nil || len(fixture.Tools) == 0 {
		t.Fatalf("stable descriptor fixture has an unexpected shape: %v", err)
	}
	var names []string
	for _, tool := range fixture.Tools {
		if tool.Tier != "stable" {
			t.Fatalf("tool %q has tier %q in the stable fixture", tool.Name, tool.Tier)
		}
		names = append(names, tool.Name)
	}
	return names
}

func fail(t *testing.T, what string, errs []string) {
	t.Helper()
	if len(errs) != 0 {
		t.Fatalf("%s contract violations:\n  - %s", what, strings.Join(errs, "\n  - "))
	}
}

func TestRealHooksJSONSatisfiesContract(t *testing.T) {
	dir := pluginDir(t)
	scripts, errs := ValidateHooksJSON(readFile(t, filepath.Join(dir, "hooks", "hooks.json")))
	fail(t, "hooks.json", errs)
	for _, s := range scripts {
		path := filepath.Join(dir, "hooks", "scripts", s)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("hooks.json references a missing script: %v", err)
		}
		if info.Mode()&0o111 == 0 {
			t.Fatalf("%s must be executable (mode %v)", s, info.Mode())
		}
		text := readFile(t, path)
		fail(t, s, ValidateScript(s, text))
		fail(t, s, ValidateGate(s, text))
	}
}

func TestRealLibSatisfiesContract(t *testing.T) {
	dir := pluginDir(t)
	text := readFile(t, filepath.Join(dir, "hooks", "scripts", "shunt-lib.sh"))
	fail(t, "shunt-lib.sh", ValidateScript("shunt-lib.sh", text))
	fail(t, "shunt-lib.sh", ValidateLib(text))
}

func TestManifestsAgree(t *testing.T) {
	dir := pluginDir(t)
	name, errs := ValidatePluginJSON(readFile(t, filepath.Join(dir, ".claude-plugin", "plugin.json")))
	fail(t, "plugin.json", errs)
	market := readFile(t, filepath.Join(repoRoot(t), ".claude-plugin", "marketplace.json"))
	fail(t, "marketplace.json", ValidateMarketplace(market, name, "./extensions/claude-plugin"))
}

func TestSkillFrontMatterAndToolNames(t *testing.T) {
	dir := pluginDir(t)
	skill := readFile(t, filepath.Join(dir, "skills", "graphi-code-graph", "SKILL.md"))
	name, errs := ValidateSkill(skill)
	fail(t, "SKILL.md", errs)
	if name != "graphi-code-graph" {
		t.Fatalf("skill name = %q, want graphi-code-graph (must match its directory)", name)
	}
	stable := stableTools(t)
	fail(t, "SKILL.md tool names", ValidateToolNames("SKILL.md", skill, stable, true))

	lib := readFile(t, filepath.Join(dir, "hooks", "scripts", "shunt-lib.sh"))
	fail(t, "deny text tool names", ValidateToolNames("shunt-lib.sh", lib, stable, true))
}

func TestStableToolFixtureCoversDenyReasonTools(t *testing.T) {
	stable := map[string]bool{}
	for _, s := range stableTools(t) {
		stable[s] = true
	}
	for _, want := range DenyReasonTools {
		if !stable[want] {
			t.Fatalf("deny-reason tool %q is not stable-tier", want)
		}
	}
}

// --- negative tests: the validators must FAIL on a broken artifact ---------

func TestHooksJSONRejectsDrift(t *testing.T) {
	cases := map[string]string{
		"extra event":     `{"hooks":{"PreToolUse":[],"PostToolUse":[]}}`,
		"missing matcher": `{"hooks":{"PreToolUse":[{"matcher":"Grep","hooks":[{"type":"command","command":"${CLAUDE_PLUGIN_ROOT}/hooks/scripts/gate-grep.sh","timeout":5}]}]}}`,
		"wrong command":   `{"hooks":{"PreToolUse":[{"matcher":"Grep","hooks":[{"type":"command","command":"gate-grep.sh","timeout":5}]},{"matcher":"Bash","hooks":[{"type":"command","command":"${CLAUDE_PLUGIN_ROOT}/hooks/scripts/gate-bash.sh","timeout":5}]}]}}`,
		"no timeout":      `{"hooks":{"PreToolUse":[{"matcher":"Grep","hooks":[{"type":"command","command":"${CLAUDE_PLUGIN_ROOT}/hooks/scripts/gate-grep.sh"}]},{"matcher":"Bash","hooks":[{"type":"command","command":"${CLAUDE_PLUGIN_ROOT}/hooks/scripts/gate-bash.sh","timeout":5}]}]}}`,
		"read matcher":    `{"hooks":{"PreToolUse":[{"matcher":"Read","hooks":[]}]}}`,
		"not json":        `nope`,
	}
	for name, text := range cases {
		if _, errs := ValidateHooksJSON(text); len(errs) == 0 {
			t.Errorf("%s: expected violations, got none", name)
		}
	}
}

func TestScriptRejectsBashisms(t *testing.T) {
	if errs := ValidateScript("x.sh", "#!/bin/bash\nif [[ -n $x ]]; then :; fi\n"); len(errs) < 2 {
		t.Fatalf("expected shebang and bashism violations, got %v", errs)
	}
}

func TestLibRejectsMissingFailOpenGuards(t *testing.T) {
	if errs := ValidateLib("#!/bin/sh\n"); len(errs) != len(FailOpenMarkers) {
		t.Fatalf("expected %d missing-marker violations, got %v", len(FailOpenMarkers), errs)
	}
}

func TestGateRejectsDenyBeforeEnablement(t *testing.T) {
	text := "#!/bin/sh\n. shunt-lib.sh\nshunt_deny x\nshunt_enabled\n"
	if errs := ValidateGate("g.sh", text); len(errs) == 0 {
		t.Fatal("expected a violation for deny-before-enabled ordering")
	}
}

func TestMarketplaceRejectsMismatch(t *testing.T) {
	m := `{"name":"graphi","plugins":[{"name":"graphi-shunt","source":"./elsewhere"}]}`
	if errs := ValidateMarketplace(m, "graphi-shunt", "./extensions/claude-plugin"); len(errs) == 0 {
		t.Fatal("expected a source mismatch violation")
	}
	if errs := ValidateMarketplace(m, "other", "./x"); len(errs) == 0 {
		t.Fatal("expected a missing-plugin violation")
	}
}

func TestSkillRejectsMissingFrontMatter(t *testing.T) {
	if _, errs := ValidateSkill("# no front matter\n"); len(errs) == 0 {
		t.Fatal("expected a front-matter violation")
	}
	if _, errs := ValidateSkill("---\nname: x\n---\nbody\n"); len(errs) == 0 {
		t.Fatal("expected a missing-description violation")
	}
}

func TestToolNamesRejectLabs(t *testing.T) {
	text := "search definition callers callees references impact agent_brief related_files task_context"
	if errs := ValidateToolNames("x", text, DenyReasonTools, true); len(errs) == 0 {
		t.Fatal("expected a Labs-tool violation")
	}
	if errs := ValidateToolNames("x", "search", DenyReasonTools, true); len(errs) == 0 {
		t.Fatal("expected missing-tool violations")
	}
}
