// Package mcpconfig resolves the Claude Code MCP client config location and
// performs idempotent, non-destructive upserts of graphi's MCP stdio server
// entry. It is stdlib-only and makes zero network calls (local-first, offline).
//
// It is used by `graphi setup` (SW-044) to go from a fresh install to a
// configured Claude Code MCP tool with no manual JSON editing.
package mcpconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
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
	Type    string            `json:"type"`           // "stdio"
	Command string            `json:"command"`        // absolute path to the binary
	Args    []string          `json:"args,omitempty"` // e.g. ["mcp"]
	Env     map[string]string `json:"env,omitempty"`  // optional env
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
	snapshot, err := loadSnapshot(path)
	if err != nil {
		return nil, err
	}
	return snapshot.doc, nil
}

type configSnapshot struct {
	doc    map[string]any
	raw    []byte
	exists bool
}

func loadSnapshot(path string) (configSnapshot, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return configSnapshot{doc: map[string]any{}}, nil
		}
		return configSnapshot{}, fmt.Errorf("mcpconfig: read %s: %w", path, err)
	}
	var doc map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&doc); err != nil {
		return configSnapshot{}, fmt.Errorf("mcpconfig: parse %s: %w", path, err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return configSnapshot{}, fmt.Errorf("mcpconfig: parse %s: %w", path, err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return configSnapshot{doc: doc, raw: raw, exists: true}, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

// Plan determines the Action for upserting entry under mcpServers[name] given
// the current document. It performs no I/O. Comparison is semantic
// (map-normalized) so JSON key-order and nil-vs-empty-map differences do not
// cause spurious updates.
//
// Plan is the Claude-Code-keyed convenience over planKey ("mcpServers").
func Plan(doc map[string]any, name string, entry ServerEntry) (Action, error) {
	return planKey(doc, "mcpServers", name, entry)
}

// planKey is Plan generalized over the top-level servers key, so a client whose
// stdio servers live under a different key (e.g. VS Code's "servers") shares the
// exact same semantic-compare logic.
func planKey(doc map[string]any, serversKey, name string, entry ServerEntry) (Action, error) {
	servers, err := serverMap(doc, serversKey)
	if err != nil {
		return "", err
	}
	cur, ok := servers[name]
	if !ok {
		return ActionCreated, nil
	}
	merged, err := mergeServerEntry(cur, entry)
	if err != nil {
		return "", fmt.Errorf("mcpconfig: server %q: %w", name, err)
	}
	if equalJSON(cur, merged) {
		return ActionUnchanged, nil
	}
	return ActionUpdated, nil
}

func serverMap(doc map[string]any, serversKey string) (map[string]any, error) {
	raw, exists := doc[serversKey]
	if !exists || raw == nil {
		return map[string]any{}, nil
	}
	servers, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("mcpconfig: %s must be a JSON object", serversKey)
	}
	return servers, nil
}

// mergeServerEntry changes only fields Graphi owns. Unknown fields, manual
// enabled/disabled state, permissions, and foreign environment keys survive.
func mergeServerEntry(current any, desired ServerEntry) (map[string]any, error) {
	cur, ok := current.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("existing entry must be a JSON object")
	}
	merged := make(map[string]any, len(cur)+4)
	for key, value := range cur {
		merged[key] = value
	}
	merged["command"] = desired.Command
	if len(desired.Args) == 0 {
		delete(merged, "args")
	} else {
		merged["args"] = append([]string(nil), desired.Args...)
	}
	if _, exists := merged["type"]; !exists && desired.Type != "" {
		merged["type"] = desired.Type
	}
	if len(desired.Env) > 0 {
		env := map[string]any{}
		if raw, exists := merged["env"]; exists {
			var envOK bool
			env, envOK = raw.(map[string]any)
			if !envOK {
				return nil, fmt.Errorf("existing env must be a JSON object")
			}
			envCopy := make(map[string]any, len(env)+len(desired.Env))
			for key, value := range env {
				envCopy[key] = value
			}
			env = envCopy
		}
		for key, value := range desired.Env {
			env[key] = value
		}
		merged["env"] = env
	}
	return merged, nil
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
	return applyKey(path, "mcpServers", name, entry, dryRun)
}

// applyKey is Apply generalized over the top-level servers key. Every safety
// property (atomic temp+rename, fail-closed timestamped backup, post-write
// verify+restore, preservation of unrelated keys) is identical regardless of
// which key the client lists its servers under.
func applyKey(path, serversKey, name string, entry ServerEntry, dryRun bool) (Result, error) {
	return applyKeyWithHooks(path, serversKey, name, entry, dryRun, writerHooks{})
}

type writerHooks struct {
	beforeCommit func(path string) error
	backup       func(path string, original []byte) (string, error)
}

var configPathLocks sync.Map

func pathMutex(path string) *sync.Mutex {
	canonical, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		canonical = filepath.Clean(path)
	}
	value, _ := configPathLocks.LoadOrStore(canonical, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func applyKeyWithHooks(path, serversKey, name string, entry ServerEntry, dryRun bool, hooks writerHooks) (Result, error) {
	if strings.TrimSpace(name) == "" {
		return Result{}, fmt.Errorf("mcpconfig: empty server name")
	}
	if dryRun {
		return planSnapshot(path, serversKey, name, entry)
	}

	mu := pathMutex(path)
	mu.Lock()
	defer mu.Unlock()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Result{}, fmt.Errorf("mcpconfig: mkdir: %w", err)
	}
	unlock, err := lockConfig(path)
	if err != nil {
		return Result{}, err
	}
	defer unlock()

	snapshot, err := loadSnapshot(path)
	if err != nil {
		return Result{}, err
	}
	act, err := planKey(snapshot.doc, serversKey, name, entry)
	if err != nil {
		return Result{}, err
	}
	diff := redactedDiff(name, act, entry)
	if act == ActionUnchanged {
		return Result{Action: act, Diff: diff}, nil
	}

	servers, err := serverMap(snapshot.doc, serversKey)
	if err != nil {
		return Result{}, err
	}
	if current, exists := servers[name]; exists {
		servers[name], err = mergeServerEntry(current, entry)
		if err != nil {
			return Result{}, fmt.Errorf("mcpconfig: server %q: %w", name, err)
		}
	} else {
		servers[name] = entry
	}
	snapshot.doc[serversKey] = servers

	backupPath, err := writeAtomicWithBackupSnapshot(path, snapshot.doc, snapshot, hooks)
	if err != nil {
		return Result{}, err
	}
	return Result{Action: act, Diff: diff, BackupPath: backupPath}, nil
}

func planSnapshot(path, serversKey, name string, entry ServerEntry) (Result, error) {
	snapshot, err := loadSnapshot(path)
	if err != nil {
		return Result{}, err
	}
	act, err := planKey(snapshot.doc, serversKey, name, entry)
	if err != nil {
		return Result{}, err
	}
	return Result{Action: act, Diff: redactedDiff(name, act, entry)}, nil
}

func redactedDiff(name string, action Action, entry ServerEntry) string {
	envKeys := make([]string, 0, len(entry.Env))
	for key := range entry.Env {
		envKeys = append(envKeys, key)
	}
	sort.Strings(envKeys)
	return fmt.Sprintf("action: %s\nserver: %s\nmanaged: command,args,env-keys:%v\n", action, name, envKeys)
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
			return "", nil
		}
		return "", fmt.Errorf("mcpconfig: read for backup %s: %w", path, err)
	}
	return backupBytes(path, raw)
}

func backupBytes(path string, raw []byte) (string, error) {
	base := path + backupSuffix(time.Now())
	for attempt := 0; attempt < 1000; attempt++ {
		bakPath := base
		if attempt > 0 {
			bakPath = fmt.Sprintf("%s-%d", base, attempt)
		}
		file, err := os.OpenFile(bakPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("mcpconfig: write backup %s: %w", bakPath, err)
		}
		if _, err := file.Write(raw); err != nil {
			_ = file.Close()
			_ = os.Remove(bakPath)
			return "", fmt.Errorf("mcpconfig: write backup %s: %w", bakPath, err)
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			_ = os.Remove(bakPath)
			return "", fmt.Errorf("mcpconfig: sync backup %s: %w", bakPath, err)
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(bakPath)
			return "", fmt.Errorf("mcpconfig: close backup %s: %w", bakPath, err)
		}
		return bakPath, nil
	}
	return "", fmt.Errorf("mcpconfig: too many backups for %s", path)
}

// writeAtomicWithBackup backs up an existing file (fail-closed), then writes doc
// atomically (temp + rename). If a step after the successful rename fails, it
// restores the original byte-identical from the backup. It returns the backup
// path (empty when the file did not previously exist).
func writeAtomicWithBackup(path string, doc map[string]any) (string, error) {
	snapshot, err := loadSnapshot(path)
	if err != nil {
		return "", err
	}
	return writeAtomicWithBackupSnapshot(path, doc, snapshot, writerHooks{})
}

func writeAtomicWithBackupSnapshot(path string, doc map[string]any, snapshot configSnapshot, hooks writerHooks) (string, error) {
	buf, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", fmt.Errorf("mcpconfig: marshal: %w", err)
	}
	return writeAtomicBytesWithBackupSnapshot(path, buf, snapshot, hooks, func(raw []byte) error {
		_, err := decodeDocument(raw)
		return err
	})
}

func writeAtomicBytesWithBackupSnapshot(path string, buf []byte, snapshot configSnapshot, hooks writerHooks, validate func([]byte) error) (string, error) {
	if err := validate(buf); err != nil {
		return "", fmt.Errorf("mcpconfig: validate new config: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("mcpconfig: mkdir: %w", err)
	}
	if hooks.beforeCommit != nil {
		if err := hooks.beforeCommit(path); err != nil {
			return "", fmt.Errorf("mcpconfig: before commit: %w", err)
		}
	}
	if err := verifySnapshot(path, snapshot); err != nil {
		return "", err
	}

	// Fail-closed backup: if the file exists and we cannot back it up, abort
	// BEFORE touching the live config so the original stays byte-identical.
	var bakPath string
	if snapshot.exists {
		backupFn := hooks.backup
		if backupFn == nil {
			backupFn = backupBytes
		}
		var backupErr error
		bakPath, backupErr = backupFn(path, snapshot.raw)
		if backupErr != nil {
			return "", fmt.Errorf("mcpconfig: backup: %w", backupErr)
		}
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("mcpconfig: temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op if rename succeeded
	if _, err := tmp.Write(buf); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("mcpconfig: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("mcpconfig: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("mcpconfig: close temp: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return "", fmt.Errorf("mcpconfig: chmod: %w", err)
	}
	if err := verifySnapshot(path, snapshot); err != nil {
		return "", err
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
		} else if !snapshot.exists {
			_ = os.Remove(path)
		}
		return "", fmt.Errorf("mcpconfig: post-write verify failed, original restored: %w", verifyErr)
	}

	return bakPath, nil
}

func decodeDocument(raw []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var doc map[string]any
	if err := decoder.Decode(&doc); err != nil {
		return nil, err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, nil
}

func verifySnapshot(path string, snapshot configSnapshot) error {
	current, err := os.ReadFile(path)
	if !snapshot.exists {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("mcpconfig: re-read before replace: %w", err)
		}
		return fmt.Errorf("mcpconfig: config appeared during update; refusing to overwrite")
	}
	if err != nil {
		return fmt.Errorf("mcpconfig: config changed during update: %w", err)
	}
	if !bytes.Equal(current, snapshot.raw) {
		return fmt.Errorf("mcpconfig: config changed during update; refusing to overwrite")
	}
	return nil
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
