package mcpconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// machineState names a reproducible clean-machine starting state for `graphi
// setup`. Each state has a deterministic expected outcome (SW-049 AC-1..AC-4).
type machineState string

const (
	stateVirgin           machineState = "virgin"            // no config file at all
	stateForeignBlock     machineState = "foreign-block"     // valid config, only a foreign mcpServers entry
	stateAlreadyConfigured machineState = "already-configured" // identical graphi entry already present
	stateUnwritable       machineState = "unwritable"        // read-only dir → write must fail
)

// newFixture builds a named clean-machine fixture under a per-test tmpdir, driven
// entirely by an isolated config path. It NEVER touches the real ~/.claude.json.
// It returns the config path and the canonical graphi entry to apply.
func newFixture(t *testing.T, state machineState) (path string, entry ServerEntry) {
	t.Helper()
	dir := t.TempDir()
	path = filepath.Join(dir, ".claude.json")
	entry = GraphiEntry("/usr/local/bin/graphi", nil)

	switch state {
	case stateVirgin:
		// no file written
	case stateForeignBlock:
		writeJSON(t, path, map[string]any{
			"theme": "dark",
			"mcpServers": map[string]any{
				"chrome-devtools": map[string]any{"type": "stdio", "command": "npx", "args": []string{"chrome-devtools-mcp"}},
			},
		})
	case stateAlreadyConfigured:
		// Write a config that already contains the EXACT canonical graphi entry.
		writeJSON(t, path, map[string]any{
			"mcpServers": map[string]any{
				"graphi": entry,
			},
		})
	case stateUnwritable:
		// Pre-existing valid config, but the parent dir is made read-only so the
		// atomic temp+rename cannot create a sibling temp file.
		writeJSON(t, path, map[string]any{
			"mcpServers": map[string]any{
				"chrome-devtools": map[string]any{"type": "stdio", "command": "npx"},
			},
		})
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatalf("chmod dir read-only: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) // so TempDir cleanup can remove it
	}
	return path, entry
}

func listBackups(t *testing.T, path string) []string {
	t.Helper()
	matches, err := filepath.Glob(path + ".bak-*")
	if err != nil {
		t.Fatalf("glob backups: %v", err)
	}
	return matches
}

func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// S1 virgin → created, file written, NO backup (nothing to back up).
func TestFixture_Virgin_Created_NoBackup(t *testing.T) {
	path, entry := newFixture(t, stateVirgin)
	res, err := Apply(path, "graphi", entry, false)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.Action != ActionCreated {
		t.Fatalf("action = %s, want created", res.Action)
	}
	if res.BackupPath != "" {
		t.Fatalf("virgin state must produce NO backup, got %q", res.BackupPath)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if len(listBackups(t, path)) != 0 {
		t.Fatalf("unexpected backup files: %v", listBackups(t, path))
	}
}

// S2 foreign-block → graphi added, foreign byte-preserved, timestamped backup made.
func TestFixture_ForeignBlock_AddsGraphi_BackupAndPreserve(t *testing.T) {
	path, entry := newFixture(t, stateForeignBlock)
	before := readBytes(t, path)

	res, err := Apply(path, "graphi", entry, false)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.Action != ActionCreated {
		t.Fatalf("action = %s, want created (graphi entry was absent)", res.Action)
	}
	if res.BackupPath == "" {
		t.Fatal("foreign-block state must produce a timestamped backup")
	}
	if !strings.Contains(res.BackupPath, ".bak-") {
		t.Fatalf("backup path not timestamped: %s", res.BackupPath)
	}
	// Backup is byte-identical to the original.
	bak := readBytes(t, res.BackupPath)
	if string(bak) != string(before) {
		t.Fatal("backup is not byte-identical to the original")
	}
	// Foreign sibling preserved; graphi added.
	doc, _ := Load(path)
	servers := doc["mcpServers"].(map[string]any)
	if _, ok := servers["chrome-devtools"]; !ok {
		t.Fatalf("foreign sibling lost: %v", servers)
	}
	if _, ok := servers["graphi"]; !ok {
		t.Fatalf("graphi entry not added: %v", servers)
	}
}

// S3 already-configured → unchanged, NO write, NO new backup (AC-2 convergence).
func TestFixture_AlreadyConfigured_Unchanged_NoBackup(t *testing.T) {
	path, entry := newFixture(t, stateAlreadyConfigured)
	before := readBytes(t, path)

	res, err := Apply(path, "graphi", entry, false)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.Action != ActionUnchanged {
		t.Fatalf("action = %s, want unchanged", res.Action)
	}
	if res.BackupPath != "" {
		t.Fatalf("unchanged state must NOT create a backup, got %q", res.BackupPath)
	}
	if len(listBackups(t, path)) != 0 {
		t.Fatalf("unchanged state created backups: %v", listBackups(t, path))
	}
	if string(readBytes(t, path)) != string(before) {
		t.Fatal("unchanged state modified the file")
	}
}

// S4 unwritable → write fails, original byte-identical, non-zero (error returned).
func TestFixture_Unwritable_FailsAndLeavesOriginalIntact(t *testing.T) {
	path, entry := newFixture(t, stateUnwritable)
	before := readBytes(t, path)

	_, err := Apply(path, "graphi", entry, false)
	if err == nil {
		t.Fatal("expected an error writing to a read-only dir")
	}
	if string(readBytes(t, path)) != string(before) {
		t.Fatal("original config was modified despite write failure (must be byte-identical)")
	}
}

// Idempotency convergence: run Apply 3x → exactly one canonical entry; runs 2+ are
// ActionUnchanged with no write and no new backup (AC-2).
func TestIdempotency_Convergence(t *testing.T) {
	path, entry := newFixture(t, stateVirgin)

	res, err := Apply(path, "graphi", entry, false)
	if err != nil || res.Action != ActionCreated {
		t.Fatalf("run1: act=%s err=%v", res.Action, err)
	}
	for i := 2; i <= 3; i++ {
		res, err = Apply(path, "graphi", entry, false)
		if err != nil {
			t.Fatalf("run%d: %v", i, err)
		}
		if res.Action != ActionUnchanged {
			t.Fatalf("run%d action = %s, want unchanged (idempotent)", i, res.Action)
		}
		if res.BackupPath != "" {
			t.Fatalf("run%d created a backup on an unchanged run: %q", i, res.BackupPath)
		}
	}
	// Exactly one canonical graphi entry.
	doc, _ := Load(path)
	servers := doc["mcpServers"].(map[string]any)
	if len(servers) != 1 {
		t.Fatalf("expected exactly 1 mcpServers entry, got %d: %v", len(servers), servers)
	}
	if !entryMatches(servers["graphi"], entry) {
		t.Fatalf("canonical graphi entry drifted: %v", servers["graphi"])
	}
	// No .bak files should exist at all (the only write was a virgin create).
	if len(listBackups(t, path)) != 0 {
		t.Fatalf("virgin create + idempotent runs must leave no backups: %v", listBackups(t, path))
	}
}

// S2-then-S2 yields S3 behavior on the second run (foreign-block converges).
func TestForeignBlock_SecondRunConvergesToUnchanged(t *testing.T) {
	path, entry := newFixture(t, stateForeignBlock)
	if _, err := Apply(path, "graphi", entry, false); err != nil {
		t.Fatalf("run1: %v", err)
	}
	res, err := Apply(path, "graphi", entry, false)
	if err != nil {
		t.Fatalf("run2: %v", err)
	}
	if res.Action != ActionUnchanged {
		t.Fatalf("run2 action = %s, want unchanged (S2→S3 convergence)", res.Action)
	}
	// Exactly one backup (from run1 only); run2 made none.
	if n := len(listBackups(t, path)); n != 1 {
		t.Fatalf("expected exactly 1 backup after two runs, got %d", n)
	}
}

// Backup-failure-aborts: if the backup cannot be written, Apply fails CLOSED and
// the live config stays byte-identical (it is NOT overwritten).
func TestBackupFailureAbortsBeforeTouchingConfig(t *testing.T) {
	path, entry := newFixture(t, stateForeignBlock)
	before := readBytes(t, path)

	// Make the directory read-only so the .bak file cannot be created. The
	// backup step must fail BEFORE the atomic write, leaving the original intact.
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	_, err := Apply(path, "graphi", entry, false)
	if err == nil {
		t.Fatal("expected fail-closed error when backup cannot be written")
	}
	_ = os.Chmod(dir, 0o700)
	if string(readBytes(t, path)) != string(before) {
		t.Fatal("live config was modified despite fail-closed backup (must be byte-identical)")
	}
	if len(listBackups(t, path)) != 0 {
		t.Fatalf("a partial backup leaked: %v", listBackups(t, path))
	}
}

// backupSuffix is filesystem-safe and UTC-stamped.
func TestBackupSuffix_Format(t *testing.T) {
	tm, err := time.Parse(time.RFC3339, "2026-06-23T14:58:54Z")
	if err != nil {
		t.Fatal(err)
	}
	if s := backupSuffix(tm); s != ".bak-20260623T145854Z" {
		t.Fatalf("backup suffix = %q, want .bak-20260623T145854Z", s)
	}
}

// restore brings the file back byte-identical from a backup (AC-4 insurance).
func TestRestore_ByteIdentical(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")
	bak := filepath.Join(dir, ".claude.json.bak")
	orig := []byte(`{"mcpServers":{"foreign":{}}}`)
	if err := os.WriteFile(bak, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("CORRUPTED"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := restore(bak, path); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if string(readBytes(t, path)) != string(orig) {
		t.Fatal("restore was not byte-identical")
	}
}

