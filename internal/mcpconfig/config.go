// Package mcpconfig resolves the Claude Code MCP client config location and
// performs idempotent, non-destructive upserts of graphi's MCP stdio server
// entry. It is stdlib-only and makes zero network calls (local-first, offline).
//
// It is used by `graphi setup` (SW-044) to go from a fresh install to a
// configured Claude Code MCP tool with no manual JSON editing.
package mcpconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
)

// EnvOverride is the environment variable name that, when set, replaces the
// default config path. Primarily for testing and non-standard installs.
const EnvOverride = "CLAUDE_CONFIG_PATH"

// DefaultName is the Claude Code global config filename (verified live).
const DefaultName = ".claude.json"

// ServerEntry is the mcpServers.<name> shape Claude Code expects for a stdio
// server. It matches the verified live format in ~/.claude.json.
type ServerEntry struct {
	Type    string            `json:"type"`              // "stdio"
	Command string            `json:"command"`           // absolute path to the binary
	Args    []string          `json:"args,omitempty"`    // e.g. ["mcp"]
	Env     map[string]string `json:"env,omitempty"`     // optional env
}

// Action is the outcome of a setup upsert against the current config.
type Action string

const (
	ActionCreated   Action = "created"   // entry was absent and is now added
	ActionUpdated   Action = "updated"   // entry existed but differed and is now replaced
	ActionUnchanged Action = "unchanged" // entry already matched exactly; no write needed
)

// GraphiEntry builds the canonical graphi MCP server entry for the given binary
// path. args defaults to ["mcp"] when empty.
func GraphiEntry(binary string, args []string) ServerEntry {
	if len(args) == 0 {
		args = []string{"mcp"}
	}
	return ServerEntry{Type: "stdio", Command: binary, Args: args, Env: map[string]string{}}
}

// ConfigPath resolves the config file path: $CLAUDE_CONFIG_PATH if set, else
// ~/.claude.json under the user's home directory. It returns an error only if
// the home directory cannot be determined.
func ConfigPath() (string, error) {
	if v := os.Getenv(EnvOverride); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("mcpconfig: resolve home: %w", err)
	}
	return filepath.Join(home, DefaultName), nil
}

// Load reads and JSON-decodes the config at path. A missing file is treated as
// an empty config (map is non-nil, empty) so callers can create-on-first-use.
// The returned map is the full document so unknown keys can be preserved.
func Load(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("mcpconfig: read %s: %w", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("mcpconfig: parse %s: %w", path, err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, nil
}

// Plan determines the Action for upserting entry under mcpServers[name] given
// the current document. It performs no I/O. Comparison is semantic
// (map-normalized) so JSON key-order and nil-vs-empty-map differences do not
// cause spurious updates.
func Plan(doc map[string]any, name string, entry ServerEntry) (Action, error) {
	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	cur, ok := servers[name]
	if !ok {
		return ActionCreated, nil
	}
	if equalJSON(cur, entry) {
		return ActionUnchanged, nil
	}
	return ActionUpdated, nil
}

// equalJSON reports whether a and b are semantically equal JSON values, ignoring
// key order and nil-vs-empty-container differences. Both sides are normalized
// through a marshal -> unmarshal into map[string]any cycle.
func equalJSON(a, b any) bool {
	return reflect.DeepEqual(normalizeJSON(a), normalizeJSON(b))
}

// normalizeJSON round-trips a value through JSON into the generic
// map/slice/float/string/bool/nil form so two semantically-equal values compare
// equal regardless of source type or key order.
func normalizeJSON(v any) any {
	buf, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out any
	_ = json.Unmarshal(buf, &out)
	return out
}

// Apply upserts entry under mcpServers[name] into the config at path. When
// dryRun is true it computes and returns the Action + a human-readable diff but
// writes NO file. Otherwise it writes atomically (temp + rename in the same
// directory), preserving every unrelated key. It never deletes sibling
// mcpServers entries or unknown top-level keys.
func Apply(path, name string, entry ServerEntry, dryRun bool) (Action, string, error) {
	doc, err := Load(path)
	if err != nil {
		return "", "", err
	}
	act, err := Plan(doc, name, entry)
	if err != nil {
		return "", "", err
	}

	diff := fmt.Sprintf("config: %s\naction: %s\nentry: {type:%s command:%s args:%v}\n",
		path, act, entry.Type, entry.Command, entry.Args)

	if dryRun || act == ActionUnchanged {
		return act, diff, nil
	}

	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers[name] = entry
	doc["mcpServers"] = servers

	return act, diff, writeAtomic(path, doc)
}

// writeAtomic marshals doc to indented JSON and writes it via temp-file + rename
// in the same directory (atomic on POSIX). The original file is left intact if
// marshalling fails.
func writeAtomic(path string, doc map[string]any) error {
	buf, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("mcpconfig: marshal: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mcpconfig: mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".claude.json.tmp-*")
	if err != nil {
		return fmt.Errorf("mcpconfig: temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op if rename succeeded
	if _, err := tmp.Write(buf); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("mcpconfig: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("mcpconfig: close temp: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("mcpconfig: chmod: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("mcpconfig: rename: %w", err)
	}
	return nil
}
