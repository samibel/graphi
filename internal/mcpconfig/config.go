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
	"time"
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
//
// Platform matrix (SW-049 AgDR — global-config-only contract):
// "Supported platforms" means any OS where os.UserHomeDir() resolves
// (macOS, Linux, Windows). The config location is the single GLOBAL Claude Code
// config — $CLAUDE_CONFIG_PATH (override, used by tests / non-standard installs)
// → ~/.claude.json. This story does NOT discover project-scoped .mcp.json files;
// that is an explicit, deliberate follow-up. The global config is cross-platform
// by construction (os.UserHomeDir), so AC-1's "across supported platforms" is
// satisfied honestly via one canonical path rather than per-OS special cases.
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

// Result is the outcome of an Apply call. BackupPath names the timestamped
// .bak-<UTC> copy that was made of the original config before it was overwritten
// (empty when no backup was needed — virgin file, dry-run, or unchanged).
type Result struct {
	Action     Action
	Diff       string
	BackupPath string
}

// Apply upserts entry under mcpServers[name] into the config at path. When
// dryRun is true it computes and returns the Action + a human-readable diff but
// writes NO file. Otherwise it writes atomically (temp + rename in the same
// directory), preserving every unrelated key. It never deletes sibling
// mcpServers entries or unknown top-level keys.
//
// Safety contract (SW-049 AC-3/AC-4):
//   - Before overwriting an EXISTING file, a timestamped backup
//     (<path>.bak-<UTC compact RFC3339>) is created at 0600. If the backup
//     cannot be written, Apply fails CLOSED — it returns an error BEFORE touching
//     the live config, so the original stays byte-identical.
//   - The write itself is atomic (temp + rename). If any step AFTER the rename
//     fails, the original is restored byte-identical from the backup.
//   - The backup path is surfaced via Result so the caller can report it.
func Apply(path, name string, entry ServerEntry, dryRun bool) (Result, error) {
	doc, err := Load(path)
	if err != nil {
		return Result{}, err
	}
	act, err := Plan(doc, name, entry)
	if err != nil {
		return Result{}, err
	}

	diff := fmt.Sprintf("config: %s\naction: %s\nentry: {type:%s command:%s args:%v}\n",
		path, act, entry.Type, entry.Command, entry.Args)

	if dryRun || act == ActionUnchanged {
		return Result{Action: act, Diff: diff}, nil
	}

	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers[name] = entry
	doc["mcpServers"] = servers

	backupPath, err := writeAtomicWithBackup(path, doc)
	if err != nil {
		return Result{}, err
	}
	return Result{Action: act, Diff: diff, BackupPath: backupPath}, nil
}

// backupSuffix builds the timestamped backup suffix for now in UTC, using a
// filesystem-safe compact RFC3339 form (e.g. 20260623T145854Z).
func backupSuffix(now time.Time) string {
	return ".bak-" + now.UTC().Format("20060102T150405Z")
}

// backup copies the bytes of path to <path><backupSuffix> at 0600 and returns the
// backup path. A missing source file means there is nothing to back up (virgin
// state): it returns ("", nil). Any failure to read the source or write the
// backup is returned so callers can fail CLOSED before mutating the live config.
func backup(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // nothing to back up
		}
		return "", fmt.Errorf("mcpconfig: read for backup %s: %w", path, err)
	}
	bakPath := path + backupSuffix(time.Now())
	if err := os.WriteFile(bakPath, raw, 0o600); err != nil {
		return "", fmt.Errorf("mcpconfig: write backup %s: %w", bakPath, err)
	}
	return bakPath, nil
}

// writeAtomicWithBackup backs up an existing file (fail-closed), then writes doc
// atomically (temp + rename). If a step after the successful rename fails, it
// restores the original byte-identical from the backup. It returns the backup
// path (empty when the file did not previously exist).
func writeAtomicWithBackup(path string, doc map[string]any) (string, error) {
	buf, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", fmt.Errorf("mcpconfig: marshal: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("mcpconfig: mkdir: %w", err)
	}

	// Fail-closed backup: if the file exists and we cannot back it up, abort
	// BEFORE touching the live config so the original stays byte-identical.
	bakPath, err := backup(path)
	if err != nil {
		return "", err
	}

	tmp, err := os.CreateTemp(dir, ".claude.json.tmp-*")
	if err != nil {
		return "", fmt.Errorf("mcpconfig: temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op if rename succeeded
	if _, err := tmp.Write(buf); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("mcpconfig: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("mcpconfig: close temp: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return "", fmt.Errorf("mcpconfig: chmod: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		// The rename never partially applied; the original is intact. No restore
		// needed (the backup remains as the user-recoverable artifact).
		return "", fmt.Errorf("mcpconfig: rename: %w", err)
	}

	// Post-rename verification: confirm the live file is exactly the bytes we
	// intended. Any failure here triggers a byte-identical restore from backup
	// (AC-4 insurance for a post-rename failure).
	if verifyErr := verifyWritten(path, buf); verifyErr != nil {
		if bakPath != "" {
			if rerr := restore(bakPath, path); rerr != nil {
				return "", fmt.Errorf("mcpconfig: post-write verify failed (%v) AND restore failed: %w", verifyErr, rerr)
			}
		}
		return "", fmt.Errorf("mcpconfig: post-write verify failed, original restored: %w", verifyErr)
	}

	return bakPath, nil
}

// verifyWritten reads path back and confirms it equals want byte-for-byte.
func verifyWritten(path string, want []byte) error {
	got, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read back %s: %w", path, err)
	}
	if !bytesEqual(got, want) {
		return fmt.Errorf("written bytes (%d) differ from intended (%d)", len(got), len(want))
	}
	return nil
}

// restore copies the backup bytes back over path (byte-identical recovery).
func restore(bakPath, path string) error {
	raw, err := os.ReadFile(bakPath)
	if err != nil {
		return fmt.Errorf("read backup %s: %w", bakPath, err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("restore %s from %s: %w", path, bakPath, err)
	}
	return nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
