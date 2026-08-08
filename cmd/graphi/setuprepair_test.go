package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/doctor"
	"github.com/samibel/graphi/internal/mcpconfig"
)

// repairConfigAt writes a Claude-shaped config holding the given servers into
// its own directory (so the directory snapshot below sees the config, any
// backup and any leaked temp file) and returns the path.
func repairConfigAt(t *testing.T, servers map[string]any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".claude.json")
	b, err := json.MarshalIndent(map[string]any{"mcpServers": servers, "unrelated": "keep-me"}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// zeroConfigEntry is a graphi MCP entry with no pin at all — the shape that
// contends.
func zeroConfigEntry() map[string]any {
	return map[string]any{"type": "stdio", "command": "/usr/local/bin/graphi", "args": []any{"mcp"}}
}

// contendingServers is the defect this story exists for: the setup-managed
// "graphi" entry plus a hand-added one, both zero-config, plus a non-graphi
// neighbour that must never be touched.
func contendingServers() map[string]any {
	return map[string]any{
		"graphi":      zeroConfigEntry(),
		"graphi-mars": zeroConfigEntry(),
		"other":       map[string]any{"type": "stdio", "command": "/usr/bin/other-tool", "args": []any{"serve"}},
	}
}

func repairClientAt(t *testing.T, path string) mcpconfig.Client {
	t.Helper()
	c, ok := mcpconfig.ClientByID("claude")
	if !ok {
		t.Fatal(`mcpconfig has no "claude" client`)
	}
	return c.WithConfigPath(path)
}

// repairSnapshotDir records every file in dir with its bytes so a later compare
// proves not just that the config is byte-identical but that no backup and no
// temp file was produced either.
func repairSnapshotDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	out := map[string]string{}
	for _, e := range ents {
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		out[e.Name()] = string(raw)
	}
	return out
}

func repairAssertDirUnchanged(t *testing.T, dir string, before map[string]string) {
	t.Helper()
	after := repairSnapshotDir(t, dir)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("config directory changed — the command wrote something it promised not to\nbefore=%v\nafter=%v", before, after)
	}
}

// repairArgsOf reads one server entry's args back off disk.
func repairArgsOf(t *testing.T, path, name string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	e, ok := doc.MCPServers[name]
	if !ok {
		t.Fatalf("server %q missing from %s: %s", name, path, raw)
	}
	return e.Args
}

// runRepair drives the command function with captured output.
func runRepair(t *testing.T, path string, pins map[string]string, dryRun bool) (rc int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	rc = runSetupRepair([]mcpconfig.Client{repairClientAt(t, path)}, pins, dryRun, &out, &errb)
	return rc, out.String(), errb.String()
}

// TestSetupRepair_PinsExtrasAndKeepsManagedZeroConfig pins AC2: the
// setup-managed `graphi` entry stays zero-config and every OTHER contending
// entry gains `-root <path>`. It also pins AC7's no-collateral-damage half: the
// non-graphi neighbour is untouched and no entry disappears.
func TestSetupRepair_PinsExtrasAndKeepsManagedZeroConfig(t *testing.T) {
	path := repairConfigAt(t, contendingServers())
	mars := t.TempDir()

	rc, stdout, stderr := runRepair(t, path, map[string]string{"graphi-mars": mars}, false)
	if rc != 0 {
		t.Fatalf("rc = %d, want 0 (stderr: %s)", rc, stderr)
	}
	if got, want := repairArgsOf(t, path, "graphi-mars"), []string{"mcp", "-root", mars}; !reflect.DeepEqual(got, want) {
		t.Fatalf("graphi-mars args = %v, want %v", got, want)
	}
	if got, want := repairArgsOf(t, path, "graphi"), []string{"mcp"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("managed entry no longer zero-config: args = %v, want %v", got, want)
	}
	if got, want := repairArgsOf(t, path, "other"), []string{"serve"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("non-graphi entry modified: args = %v, want %v", got, want)
	}
	if !strings.Contains(stdout, "keep graphi zero-config") {
		t.Fatalf("report does not say the managed entry is kept zero-config:\n%s", stdout)
	}
}

// TestSetupRepair_NoRootIsEverDerived is AC3, the criterion SW-161 failed. It
// stages every source a previous implementation did (or could) mine for a root
// — the entry's own `cwd` key, GRAPHI_ROOT in the process environment and in
// the entry's env map, and a real directory that looks like a store — and
// supplies NO --pin. Repair must still refuse: a root comes from the user or it
// does not come at all. If any inference were reintroduced, this exits 0 with a
// written config and the test fails on both counts.
func TestSetupRepair_NoRootIsEverDerived(t *testing.T) {
	bait := t.TempDir() // a real, existing directory — inference would happily use it
	entry := zeroConfigEntry()
	entry["cwd"] = bait                                // the SW-161 source, proven to be an inference
	entry["env"] = map[string]any{"GRAPHI_ROOT": bait} // an env-map source
	path := repairConfigAt(t, map[string]any{
		"graphi":      zeroConfigEntry(),
		"graphi-mars": entry,
	})
	dir := filepath.Dir(path)
	t.Setenv("GRAPHI_ROOT", bait) // the process-environment source
	t.Setenv("PWD", bait)
	before := repairSnapshotDir(t, dir)

	rc, _, stderr := runRepair(t, path, nil, false)
	if rc == 0 {
		t.Fatalf("repair succeeded with no user-supplied root — a root was derived from somewhere (stderr: %s)", stderr)
	}
	repairAssertDirUnchanged(t, dir, before)
	if strings.Contains(stderr, bait) {
		t.Fatalf("the report names %q as a candidate root — it was derived, not supplied:\n%s", bait, stderr)
	}
}

// TestSetupRepair_UndeterminableRootReportsAndExitsNonZero pins AC4 by trying to
// make it OPEN: with no --pin for a contending entry the command must NAME the
// client and the server, change nothing, and exit non-zero. Dropping the guard
// (silently skipping) would give rc 0 and no report.
func TestSetupRepair_UndeterminableRootReportsAndExitsNonZero(t *testing.T) {
	path := repairConfigAt(t, contendingServers())
	dir := filepath.Dir(path)
	before := repairSnapshotDir(t, dir)

	rc, _, stderr := runRepair(t, path, nil, false)
	if rc == 0 {
		t.Fatal("rc = 0 with an undeterminable root — the fail-closed guard is open")
	}
	if !strings.Contains(stderr, "Claude Code") || !strings.Contains(stderr, `"graphi-mars"`) {
		t.Fatalf("report does not name the client and the server:\n%s", stderr)
	}
	if !strings.Contains(stderr, "--pin graphi-mars=") {
		t.Fatalf("report does not tell the user how to supply the repository:\n%s", stderr)
	}
	repairAssertDirUnchanged(t, dir, before)
}

// TestSetupRepair_ValidatesRootBeforeWriting pins AC5 — the criterion SW-161
// lacked, which let a repair turn a contending entry into a DEAD one, print
// success and exit 0. Each root here is one `graphi mcp -root` refuses at bind
// time. With the validation dropped, every case would write the bad root and
// exit 0.
func TestSetupRepair_ValidatesRootBeforeWriting(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"does not exist":  filepath.Join(t.TempDir(), "no-such-repo"),
		"is a file":       file,
		"is under a file": filepath.Join(file, "nested"),
	}
	for name, root := range cases {
		t.Run(name, func(t *testing.T) {
			path := repairConfigAt(t, contendingServers())
			dir := filepath.Dir(path)
			before := repairSnapshotDir(t, dir)

			rc, _, stderr := runRepair(t, path, map[string]string{"graphi-mars": root}, false)
			if rc == 0 {
				t.Fatalf("rc = 0 for a root that %s — a repair may never produce an entry that cannot bind", name)
			}
			if !strings.Contains(stderr, "graphi-mars") {
				t.Fatalf("report does not name the entry it refused:\n%s", stderr)
			}
			repairAssertDirUnchanged(t, dir, before)
		})
	}
}

// TestSetupRepair_RefusalIsPerEntry: a bad root for one entry does not cost a
// good root for another. The valid entry is pinned, the invalid one is reported
// and left alone, and the command still exits non-zero (AC4/AC5 say "for that
// entry").
func TestSetupRepair_RefusalIsPerEntry(t *testing.T) {
	servers := contendingServers()
	servers["graphi-venus"] = zeroConfigEntry()
	path := repairConfigAt(t, servers)
	mars := t.TempDir()

	rc, _, stderr := runRepair(t, path, map[string]string{
		"graphi-mars":  mars,
		"graphi-venus": filepath.Join(t.TempDir(), "no-such-repo"),
	}, false)
	if rc == 0 {
		t.Fatalf("rc = 0 despite an invalid root (stderr: %s)", stderr)
	}
	if got, want := repairArgsOf(t, path, "graphi-mars"), []string{"mcp", "-root", mars}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the valid entry was not pinned: args = %v, want %v", got, want)
	}
	if got, want := repairArgsOf(t, path, "graphi-venus"), []string{"mcp"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the refused entry was written anyway: args = %v, want %v", got, want)
	}
}

// TestSetupRepair_DryRunPreviewsAndWritesNothing pins AC6: the planned per-entry
// change is printed, the config DIRECTORY is byte-identical afterwards (no
// config write, no backup, no temp file), and the exit code reports that the
// preview RAN — including when the preview's finding is an entry it cannot
// repair.
func TestSetupRepair_DryRunPreviewsAndWritesNothing(t *testing.T) {
	t.Run("with a supplied root", func(t *testing.T) {
		path := repairConfigAt(t, contendingServers())
		dir := filepath.Dir(path)
		mars := t.TempDir()
		before := repairSnapshotDir(t, dir)

		rc, stdout, stderr := runRepair(t, path, map[string]string{"graphi-mars": mars}, true)
		if rc != 0 {
			t.Fatalf("rc = %d, want 0 (stderr: %s)", rc, stderr)
		}
		if !strings.Contains(stdout, "pin: graphi-mars args += -root "+mars) {
			t.Fatalf("preview does not state the planned per-entry change:\n%s", stdout)
		}
		if !strings.Contains(stdout, "[dry-run]") {
			t.Fatalf("preview is not marked as a dry run:\n%s", stdout)
		}
		repairAssertDirUnchanged(t, dir, before)
	})

	// AC6 is explicit that dry-run's exit code reports whether the PREVIEW ran,
	// not what it found: an entry with no root is a finding to act on.
	t.Run("with nothing to pin the entry to", func(t *testing.T) {
		path := repairConfigAt(t, contendingServers())
		dir := filepath.Dir(path)
		before := repairSnapshotDir(t, dir)

		rc, _, stderr := runRepair(t, path, nil, true)
		if rc != 0 {
			t.Fatalf("rc = %d, want 0 — dry-run's exit code must report the preview, not its findings", rc)
		}
		if !strings.Contains(stderr, "graphi-mars") {
			t.Fatalf("the preview did not report the entry it cannot repair:\n%s", stderr)
		}
		repairAssertDirUnchanged(t, dir, before)
	})
}

// TestSetupRepair_NonGraphiEntryIsNeverAPinTarget pins AC7's fail-closed half at
// the command boundary by trying to make it OPEN: name the non-graphi entry
// explicitly. It is not a contending graphi entry, so it is never planned, and
// the mcpconfig write half refuses it outright (see
// TestPinRoots_RefusesNonGraphiEntry).
func TestSetupRepair_NonGraphiEntryIsNeverAPinTarget(t *testing.T) {
	path := repairConfigAt(t, contendingServers())
	dir := filepath.Dir(path)
	before := repairSnapshotDir(t, dir)

	rc, stdout, _ := runRepair(t, path, map[string]string{"other": t.TempDir()}, false)
	if rc == 0 {
		t.Fatal("rc = 0 — naming a non-graphi entry must not count as repairing the contention")
	}
	if strings.Contains(stdout, "pin  other") {
		t.Fatalf("planned a pin against a non-graphi entry:\n%s", stdout)
	}
	repairAssertDirUnchanged(t, dir, before)
}

// TestSetupRepair_NoContentionReportsAndWritesNothing pins AC9.
func TestSetupRepair_NoContentionReportsAndWritesNothing(t *testing.T) {
	path := repairConfigAt(t, map[string]any{
		"graphi": zeroConfigEntry(),
		"other":  map[string]any{"type": "stdio", "command": "/usr/bin/other-tool", "args": []any{"serve"}},
	})
	dir := filepath.Dir(path)
	before := repairSnapshotDir(t, dir)

	rc, stdout, stderr := runRepair(t, path, nil, false)
	if rc != 0 {
		t.Fatalf("rc = %d, want 0 (stderr: %s)", rc, stderr)
	}
	if !strings.Contains(stdout, "nothing to repair") {
		t.Fatalf("no-contention run does not report that:\n%s", stdout)
	}
	repairAssertDirUnchanged(t, dir, before)
}

// TestSetupRepair_FlagWiring pins AC1: --repair honors --config, --client and
// --dry-run through the real `graphi setup` flag parsing, and registers
// NOTHING — the managed entry keeps the command it already had rather than
// being rewritten to this test binary.
func TestSetupRepair_FlagWiring(t *testing.T) {
	t.Run("--config + --dry-run writes nothing", func(t *testing.T) {
		path := repairConfigAt(t, contendingServers())
		dir := filepath.Dir(path)
		before := repairSnapshotDir(t, dir)

		if rc := runSetup([]string{"--repair", "--config", path, "--dry-run", "--pin", "graphi-mars=" + t.TempDir()}); rc != 0 {
			t.Fatalf("rc = %d, want 0", rc)
		}
		repairAssertDirUnchanged(t, dir, before)
	})

	t.Run("--config repairs and registers nothing", func(t *testing.T) {
		path := repairConfigAt(t, contendingServers())
		mars := t.TempDir()

		if rc := runSetup([]string{"--repair", "--config", path, "--pin", "graphi-mars=" + mars}); rc != 0 {
			t.Fatalf("rc = %d, want 0", rc)
		}
		if got, want := repairArgsOf(t, path, "graphi-mars"), []string{"mcp", "-root", mars}; !reflect.DeepEqual(got, want) {
			t.Fatalf("graphi-mars args = %v, want %v", got, want)
		}
		raw, _ := os.ReadFile(path)
		if !strings.Contains(string(raw), "/usr/local/bin/graphi") {
			t.Fatalf("--repair re-registered the managed entry against a different binary:\n%s", raw)
		}
		if !strings.Contains(string(raw), "keep-me") {
			t.Fatalf("--repair dropped an unrelated top-level key:\n%s", raw)
		}
	})

	t.Run("--client narrows without --config", func(t *testing.T) {
		path := repairConfigAt(t, contendingServers())
		t.Setenv(mcpconfig.EnvOverride, path)
		mars := t.TempDir()

		if rc := runSetup([]string{"--repair", "--client", "claude", "--pin", "graphi-mars=" + mars}); rc != 0 {
			t.Fatalf("rc = %d, want 0", rc)
		}
		if got, want := repairArgsOf(t, path, "graphi-mars"), []string{"mcp", "-root", mars}; !reflect.DeepEqual(got, want) {
			t.Fatalf("graphi-mars args = %v, want %v", got, want)
		}
	})

	t.Run("--pin without --repair is a usage error", func(t *testing.T) {
		if rc := runSetup([]string{"--pin", "graphi-mars=" + t.TempDir(), "--dry-run"}); rc != 1 {
			t.Fatalf("rc = %d, want 1", rc)
		}
	})

	t.Run("--repair with --project is a usage error", func(t *testing.T) {
		if rc := runSetup([]string{"--repair", "--project", "--dry-run"}); rc != 1 {
			t.Fatalf("rc = %d, want 1", rc)
		}
	})

	t.Run("malformed --pin is a usage error", func(t *testing.T) {
		for _, v := range []string{"graphi-mars", "=/repos/mars", "graphi-mars="} {
			if rc := runSetup([]string{"--repair", "--pin", v, "--dry-run"}); rc != 1 {
				t.Fatalf("--pin %q: rc = %d, want 1", v, rc)
			}
		}
	})
}

// TestSetupRepair_RoundTripEndsDoctorContention is AC10 end to end, and the
// assertion that ties this story to the real defect: `graphi doctor` reports
// contention for a client, `graphi setup --repair` runs, and re-reading the
// config from disk both the shipping ContendingGraphiServers and the shipping
// doctor check agree there is no contention left.
func TestSetupRepair_RoundTripEndsDoctorContention(t *testing.T) {
	path := repairConfigAt(t, contendingServers())
	env := &realEnv{mcpReader: claudeReaderAt(t, path)} // also sets CLAUDE_CONFIG_PATH

	res := doctor.MCPCheck(doctorTestBinary).Run(context.Background(), env)
	if !strings.Contains(res.Message, "contend on its ingest lock") {
		t.Fatalf("fixture is not contending per doctor: %q", res.Message)
	}
	if !strings.Contains(res.Action, "graphi setup --repair") {
		t.Fatalf("AC11: the mcp remediation does not point at the repair command: %q", res.Action)
	}

	if rc := runSetup([]string{"--repair", "--client", "claude", "--pin", "graphi-mars=" + t.TempDir()}); rc != 0 {
		t.Fatalf("repair rc = %d, want 0", rc)
	}

	names, err := repairClientAt(t, path).ContendingGraphiServers()
	if err != nil {
		t.Fatalf("ContendingGraphiServers: %v", err)
	}
	if len(names) >= 2 {
		t.Fatalf("still contending after repair: %v", names)
	}

	after := doctor.MCPCheck(doctorTestBinary).Run(context.Background(), env)
	if strings.Contains(after.Message, "contend on its ingest lock") {
		t.Fatalf("doctor still reports contention after repair: %q", after.Message)
	}
	if strings.Contains(after.Detail, "contend on its ingest lock") {
		t.Fatalf("doctor detail still reports contention after repair: %q", after.Detail)
	}
}
