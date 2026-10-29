// Package validate is the graphi-shunt plugin contract checker. It makes the
// otherwise-declarative Claude Code plugin (hooks.json, shell gates, SKILL.md,
// manifests) CHECKABLE in CI, mirroring extensions/github-action/validate: it
// parses the shipped artifacts and asserts the invariants the plugin relies on.
//
//   - hooks.json registers exactly the Grep and Bash PreToolUse gates, each
//     pointing at an existing, executable, POSIX-sh script with a bounded timeout
//   - the gates and their shared library carry the fail-open guards
//   - plugin.json and the repo-root marketplace.json agree on name and path
//   - SKILL.md has the front matter Claude Code needs
//   - the deny text and the skill name only stable-tier graphi tools
//
// The behavioural contract (which searches are blocked or allowed) is covered by
// gate_test.go, which executes the real scripts against fixture hook payloads.
package validate

import (
	"encoding/json"
	"fmt"
	"strings"
)

// GateScripts are the PreToolUse gates hooks.json must register, keyed by the
// tool-name matcher Claude Code evaluates.
var GateScripts = map[string]string{
	"Grep": "gate-grep.sh",
	"Bash": "gate-bash.sh",
}

// DenyReasonTools are the graphi tools the deny text recommends. Every one must
// be a stable-tier tool (surfaces/mcp/testdata/mcp-descriptors-stdio-stable.json).
var DenyReasonTools = []string{
	"search", "definition", "callers", "callees", "references", "impact",
	"agent_brief", "related_files",
}

// LabsToolNames must never be recommended by the plugin: they are opt-in and
// absent from the default `graphi mcp` tool list.
var LabsToolNames = []string{
	"repo_overview", "task_context", "symbol_context", "test_impact",
	"change_impact", "graph_health", "strict_query", "skillgen",
}

// FailOpenMarkers must appear in the shared library: the escape hatch plus the
// dependency probes that make the gate inert when it cannot run safely.
var FailOpenMarkers = []string{
	"GRAPHI_SHUNT_DISABLE",
	"command -v jq",
	"command -v graphi",
	"db.sqlite",
}

type hookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

type hookMatcher struct {
	Matcher string        `json:"matcher"`
	Hooks   []hookCommand `json:"hooks"`
}

type hooksFile struct {
	Hooks map[string][]hookMatcher `json:"hooks"`
}

// ValidateHooksJSON checks hooks.json and returns the script file names the
// registered gates reference (relative to hooks/scripts), plus any violations.
func ValidateHooksJSON(text string) (scripts []string, errs []string) {
	var f hooksFile
	if err := json.Unmarshal([]byte(text), &f); err != nil {
		return nil, []string{fmt.Sprintf("hooks.json does not parse: %v", err)}
	}
	if len(f.Hooks) != 1 {
		errs = append(errs, fmt.Sprintf("hooks.json must register only PreToolUse hooks, got events %v", keys(f.Hooks)))
	}
	pre := f.Hooks["PreToolUse"]
	if len(pre) != len(GateScripts) {
		errs = append(errs, fmt.Sprintf("PreToolUse must have exactly %d matchers (%v), got %d", len(GateScripts), keys(GateScripts), len(pre)))
	}
	seen := map[string]bool{}
	for _, m := range pre {
		want, ok := GateScripts[m.Matcher]
		if !ok {
			errs = append(errs, fmt.Sprintf("unexpected PreToolUse matcher %q", m.Matcher))
			continue
		}
		seen[m.Matcher] = true
		if len(m.Hooks) != 1 {
			errs = append(errs, fmt.Sprintf("matcher %q must run exactly one hook, got %d", m.Matcher, len(m.Hooks)))
			continue
		}
		h := m.Hooks[0]
		if h.Type != "command" {
			errs = append(errs, fmt.Sprintf("matcher %q hook type = %q, want command", m.Matcher, h.Type))
		}
		wantCmd := `"${CLAUDE_PLUGIN_ROOT}/hooks/scripts/` + want + `"`
		if h.Command != wantCmd {
			errs = append(errs, fmt.Sprintf("matcher %q command = %q, want %q", m.Matcher, h.Command, wantCmd))
		}
		if h.Timeout <= 0 || h.Timeout > 10 {
			errs = append(errs, fmt.Sprintf("matcher %q timeout = %d, want 1..10 seconds (hooks run on every tool call)", m.Matcher, h.Timeout))
		}
		scripts = append(scripts, want)
	}
	for matcher := range GateScripts {
		if !seen[matcher] {
			errs = append(errs, fmt.Sprintf("missing PreToolUse matcher %q", matcher))
		}
	}
	return scripts, errs
}

// ValidateScript checks one shell script for the POSIX-sh contract.
func ValidateScript(name, text string) []string {
	var errs []string
	if !strings.HasPrefix(text, "#!/bin/sh\n") {
		errs = append(errs, fmt.Sprintf("%s must start with a #!/bin/sh shebang (POSIX sh, no bashisms)", name))
	}
	for _, bashism := range []string{"[[ ", "declare ", "local ", "<<<", "function "} {
		if strings.Contains(text, bashism) {
			errs = append(errs, fmt.Sprintf("%s uses the bashism %q", name, strings.TrimSpace(bashism)))
		}
	}
	return errs
}

// ValidateLib checks that the shared library carries every fail-open marker.
func ValidateLib(text string) []string {
	var errs []string
	for _, m := range FailOpenMarkers {
		if !strings.Contains(text, m) {
			errs = append(errs, fmt.Sprintf("shunt-lib.sh is missing the fail-open marker %q", m))
		}
	}
	return errs
}

// ValidateGate checks one gate script wires the shared library and the
// enablement check before it can emit a decision.
func ValidateGate(name, text string) []string {
	var errs []string
	for _, need := range []string{"shunt-lib.sh", "shunt_enabled", "shunt_deny"} {
		if !strings.Contains(text, need) {
			errs = append(errs, fmt.Sprintf("%s must reference %q", name, need))
		}
	}
	if strings.Index(text, "shunt_enabled") > strings.Index(text, "shunt_deny") {
		errs = append(errs, fmt.Sprintf("%s calls shunt_deny before shunt_enabled", name))
	}
	return errs
}

type pluginManifest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
}

// ValidatePluginJSON checks the plugin manifest and returns its name.
func ValidatePluginJSON(text string) (name string, errs []string) {
	var p pluginManifest
	if err := json.Unmarshal([]byte(text), &p); err != nil {
		return "", []string{fmt.Sprintf("plugin.json does not parse: %v", err)}
	}
	if p.Name == "" {
		errs = append(errs, "plugin.json is missing name")
	}
	if p.Description == "" {
		errs = append(errs, "plugin.json is missing description")
	}
	if p.Version == "" {
		errs = append(errs, "plugin.json is missing version")
	}
	return p.Name, errs
}

type marketplaceFile struct {
	Name    string `json:"name"`
	Plugins []struct {
		Name   string `json:"name"`
		Source string `json:"source"`
	} `json:"plugins"`
}

// ValidateMarketplace checks the repo-root marketplace.json lists the plugin
// under pluginName with the given relative source path.
func ValidateMarketplace(text, pluginName, wantSource string) []string {
	var m marketplaceFile
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		return []string{fmt.Sprintf("marketplace.json does not parse: %v", err)}
	}
	var errs []string
	if m.Name == "" {
		errs = append(errs, "marketplace.json is missing name")
	}
	for _, p := range m.Plugins {
		if p.Name != pluginName {
			continue
		}
		if p.Source != wantSource {
			errs = append(errs, fmt.Sprintf("marketplace source = %q, want %q", p.Source, wantSource))
		}
		return errs
	}
	return append(errs, fmt.Sprintf("marketplace.json does not list plugin %q", pluginName))
}

// ValidateSkill checks SKILL.md front matter and returns the skill name.
func ValidateSkill(text string) (name string, errs []string) {
	if !strings.HasPrefix(text, "---\n") {
		return "", []string{"SKILL.md must start with YAML front matter"}
	}
	rest := text[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", []string{"SKILL.md front matter is not closed with ---"}
	}
	var desc string
	for _, line := range strings.Split(rest[:end], "\n") {
		switch {
		case strings.HasPrefix(line, "name:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
		case strings.HasPrefix(line, "description:"):
			desc = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
		}
	}
	if name == "" {
		errs = append(errs, "SKILL.md front matter is missing name")
	}
	if desc == "" {
		errs = append(errs, "SKILL.md front matter is missing description")
	}
	return name, errs
}

// ValidateToolNames checks that text recommends only tools in stable (when it
// is the deny text, every DenyReasonTools entry must also be present) and none
// of the Labs tools.
func ValidateToolNames(label, text string, stable []string, requireAll bool) []string {
	var errs []string
	isStable := map[string]bool{}
	for _, s := range stable {
		isStable[s] = true
	}
	for _, t := range DenyReasonTools {
		if !isStable[t] {
			errs = append(errs, fmt.Sprintf("deny-reason tool %q is not in the stable MCP tool list", t))
		}
		if requireAll && !strings.Contains(text, t) {
			errs = append(errs, fmt.Sprintf("%s does not mention the stable tool %q", label, t))
		}
	}
	for _, l := range LabsToolNames {
		if strings.Contains(text, l) {
			errs = append(errs, fmt.Sprintf("%s recommends the Labs tool %q (not in the default tool list)", label, l))
		}
	}
	return errs
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
