package mcpconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// contendedConfig writes a config holding THREE contending zero-config graphi
// entries (the setup-managed "graphi" plus hand-added "graphi-mars" and
// "graphi-venus"), an already-pinned graphi entry, a NON-graphi entry, and an
// unrelated top-level key. It returns the path.
func contendedConfig(t *testing.T, key string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	writeJSON(t, path, map[string]any{
		key: map[string]any{
			"graphi":       map[string]any{"type": "stdio", "command": "/usr/local/bin/graphi", "args": []any{"mcp"}},
			"graphi-mars":  map[string]any{"type": "stdio", "command": "/usr/local/bin/graphi", "args": []any{"mcp"}},
			"graphi-venus": map[string]any{"type": "stdio", "command": "/usr/local/bin/graphi", "args": []any{"mcp", "-labs"}},
			"graphi-pinned": map[string]any{"type": "stdio", "command": "/usr/local/bin/graphi",
				"args": []any{"mcp", "-root", "/repos/already"}},
			"other": map[string]any{"type": "stdio", "command": "/usr/bin/other-tool", "args": []any{"serve"}},
		},
		"unrelated": "keep-me",
	})
	return path
}

// snapshotDir records every file in dir with its bytes, so a later compare can
// prove not only that the config is byte-identical but that no backup and no
// temp file was produced either.
func snapshotDir(t *testing.T, dir string) map[string]string {
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

func assertDirUnchanged(t *testing.T, dir string, before map[string]string) {
	t.Helper()
	after := snapshotDir(t, dir)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("directory changed\nbefore=%v\nafter=%v", before, after)
	}
}

func entryOf(t *testing.T, path, key, name string) map[string]any {
	t.Helper()
	doc := readJSON(t, path)
	servers, _ := doc[key].(map[string]any)
	if servers == nil {
		t.Fatalf("servers key %q missing in %s", key, path)
	}
	e, ok := servers[name].(map[string]any)
	if !ok {
		t.Fatalf("server %q missing in %s (servers=%v)", name, path, servers)
	}
	return e
}

func argsOf(t *testing.T, entry map[string]any) []string {
	t.Helper()
	raw, _ := entry["args"].([]any)
	out := make([]string, len(raw))
	for i, a := range raw {
		out[i], _ = a.(string)
	}
	return out
}

// TestContendingEntries_NamesAndManagedFlag pins the read half repair is built
// on: only zero-config graphi entries are reported (an already `-root`-pinned
// graphi entry and a non-graphi entry are not), sorted, and the setup-managed
// "graphi" key is flagged Managed so repair can leave it zero-config.
func TestContendingEntries_NamesAndManagedFlag(t *testing.T) {
	path := contendedConfig(t, "mcpServers")
	c := fakeClient("claude", "mcpServers", path)

	got, err := c.ContendingEntries()
	if err != nil {
		t.Fatalf("ContendingEntries: %v", err)
	}
	want := []ContendingEntry{
		{Name: "graphi", Managed: true},
		{Name: "graphi-mars", Managed: false},
		{Name: "graphi-venus", Managed: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("entries:\ngot  %v\nwant %v", got, want)
	}
}

// TestContendingEntry_CarriesNoRootCandidate pins AC3 structurally: the type
// repair reads its work from models a NAME and nothing that could be mistaken
// for a repository. SW-161 shipped a Cwd field here and inferred a root from it;
// a reviewer proved nothing writes, consumes or preserves that key. If a field
// like it ever comes back, this test says so.
func TestContendingEntry_CarriesNoRootCandidate(t *testing.T) {
	typ := reflect.TypeOf(ContendingEntry{})
	var fields []string
	for i := 0; i < typ.NumField(); i++ {
		fields = append(fields, typ.Field(i).Name)
	}
	if want := []string{"Name", "Managed"}; !reflect.DeepEqual(fields, want) {
		t.Fatalf("ContendingEntry fields = %v, want exactly %v — a contending entry states no repository, so repair must read none from it", fields, want)
	}
}

// TestPinRoots_AppendsRootAndPreservesEverythingElse pins AC2/AC7/AC8: the
// named entries gain `-root <path>` APPENDED to their existing args, every other
// entry (including the managed one, the already-pinned one and the non-graphi
// one) is byte-for-byte what it was, and the unrelated top-level key survives.
func TestPinRoots_AppendsRootAndPreservesEverythingElse(t *testing.T) {
	for _, key := range []string{"mcpServers", "servers"} {
		t.Run(key, func(t *testing.T) {
			path := contendedConfig(t, key)
			c := fakeClient("claude", key, path)
			mars, venus := t.TempDir(), t.TempDir()

			res, err := c.PinRoots(map[string]string{"graphi-mars": mars, "graphi-venus": venus}, false)
			if err != nil {
				t.Fatalf("PinRoots: %v", err)
			}
			if res.Action != ActionUpdated {
				t.Fatalf("action = %q, want %q", res.Action, ActionUpdated)
			}
			if res.BackupPath == "" {
				t.Fatal("no backup path returned — the pre-existing config must be backed up")
			}

			if got, want := argsOf(t, entryOf(t, path, key, "graphi-mars")), []string{"mcp", "-root", mars}; !reflect.DeepEqual(got, want) {
				t.Fatalf("graphi-mars args = %v, want %v", got, want)
			}
			// The existing "-labs" arg is neither removed nor reordered.
			if got, want := argsOf(t, entryOf(t, path, key, "graphi-venus")), []string{"mcp", "-labs", "-root", venus}; !reflect.DeepEqual(got, want) {
				t.Fatalf("graphi-venus args = %v, want %v", got, want)
			}
			if got, want := argsOf(t, entryOf(t, path, key, "graphi")), []string{"mcp"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("managed entry was modified: args = %v, want %v", got, want)
			}
			if got, want := argsOf(t, entryOf(t, path, key, "other")), []string{"serve"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("non-graphi entry was modified: args = %v, want %v", got, want)
			}
			doc := readJSON(t, path)
			if doc["unrelated"] != "keep-me" {
				t.Fatalf("unrelated top-level key lost: %v", doc["unrelated"])
			}
			servers, _ := doc[key].(map[string]any)
			if len(servers) != 5 {
				t.Fatalf("server count changed to %d — repair must never delete an entry (%v)", len(servers), servers)
			}
		})
	}
}

// TestPinRoots_RefusesNonGraphiEntry pins AC7's fail-closed half by trying to
// make it OPEN: ask repair to pin the entry whose command is /usr/bin/other-tool.
// It must refuse BEFORE writing anything — if the isGraphiCommand guard were
// dropped, "other" would gain `-root` and this test would fail on both counts.
func TestPinRoots_RefusesNonGraphiEntry(t *testing.T) {
	path := contendedConfig(t, "mcpServers")
	dir := filepath.Dir(path)
	c := fakeClient("claude", "mcpServers", path)
	before := snapshotDir(t, dir)

	if _, err := c.PinRoots(map[string]string{"other": t.TempDir()}, false); err == nil {
		t.Fatal("PinRoots pinned a non-graphi entry — the guard is open")
	}
	assertDirUnchanged(t, dir, before)
}

// TestPinRoots_RefusesMissingOrNonObjectEntry keeps the other two structural
// refusals fail-closed: an entry that is not there at all, and one that is not a
// JSON object. Both must error before any write.
func TestPinRoots_RefusesMissingOrNonObjectEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeJSON(t, path, map[string]any{"mcpServers": map[string]any{"scalar": "not-an-object"}})
	dir := filepath.Dir(path)
	c := fakeClient("claude", "mcpServers", path)
	root := t.TempDir()

	for _, name := range []string{"absent", "scalar"} {
		before := snapshotDir(t, dir)
		if _, err := c.PinRoots(map[string]string{name: root}, false); err == nil {
			t.Fatalf("PinRoots(%q) succeeded; want an error", name)
		}
		assertDirUnchanged(t, dir, before)
	}
}

// TestPinRoots_ValidatesRootBeforeWriting pins AC5 at the write boundary by
// trying to make it OPEN. Each bad root is one `graphi mcp -root` would refuse
// at bind time; if the validation were dropped, PinRoots would happily write it,
// return nil, and leave a config that looks repaired and cannot bind.
func TestPinRoots_ValidatesRootBeforeWriting(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"nonexistent":  filepath.Join(t.TempDir(), "no-such-repo"),
		"not-a-dir":    file,
		"empty":        "",
		"only-spaces":  "   ",
		"under-a-file": filepath.Join(file, "nested"),
	}
	for name, root := range cases {
		t.Run(name, func(t *testing.T) {
			path := contendedConfig(t, "mcpServers")
			dir := filepath.Dir(path)
			c := fakeClient("claude", "mcpServers", path)
			before := snapshotDir(t, dir)

			if _, err := c.PinRoots(map[string]string{"graphi-mars": root}, false); err == nil {
				t.Fatalf("PinRoots wrote an unvalidated root %q — a repair may never produce an entry that cannot bind", root)
			}
			assertDirUnchanged(t, dir, before)
		})
	}
}

// TestPinRoots_OneBadRootAbortsTheWholeWrite: validation is a complete pass
// BEFORE any mutation, so a batch containing one bad root leaves the good entry
// untouched too. Nothing half-repaired ever reaches disk.
func TestPinRoots_OneBadRootAbortsTheWholeWrite(t *testing.T) {
	path := contendedConfig(t, "mcpServers")
	dir := filepath.Dir(path)
	c := fakeClient("claude", "mcpServers", path)
	before := snapshotDir(t, dir)

	_, err := c.PinRoots(map[string]string{
		"graphi-mars":  t.TempDir(),
		"graphi-venus": filepath.Join(t.TempDir(), "nope"),
	}, false)
	if err == nil {
		t.Fatal("PinRoots succeeded with one invalid root; want an error")
	}
	assertDirUnchanged(t, dir, before)
}

// TestPinRoots_DryRunWritesNothing pins AC6 at the library boundary: the plan is
// computed and reported, and the config DIRECTORY is byte-identical afterwards —
// no config write, no backup, no leftover temp file.
func TestPinRoots_DryRunWritesNothing(t *testing.T) {
	path := contendedConfig(t, "mcpServers")
	dir := filepath.Dir(path)
	c := fakeClient("claude", "mcpServers", path)
	root := t.TempDir()
	before := snapshotDir(t, dir)

	res, err := c.PinRoots(map[string]string{"graphi-mars": root}, true)
	if err != nil {
		t.Fatalf("PinRoots dry-run: %v", err)
	}
	if res.Action != ActionUpdated {
		t.Fatalf("dry-run action = %q, want %q (it must still report the plan)", res.Action, ActionUpdated)
	}
	if res.Diff == "" || !strings.Contains(res.Diff, "graphi-mars") || !strings.Contains(res.Diff, root) {
		t.Fatalf("dry-run diff does not state the planned per-entry change: %q", res.Diff)
	}
	if res.BackupPath != "" {
		t.Fatalf("dry-run produced a backup at %s", res.BackupPath)
	}
	assertDirUnchanged(t, dir, before)
}

// TestPinRoots_AlreadyPinnedEntryIsLeftAlone: an entry that already names what
// it serves is not rewritten and does not force a write.
func TestPinRoots_AlreadyPinnedEntryIsLeftAlone(t *testing.T) {
	path := contendedConfig(t, "mcpServers")
	dir := filepath.Dir(path)
	c := fakeClient("claude", "mcpServers", path)
	before := snapshotDir(t, dir)

	res, err := c.PinRoots(map[string]string{"graphi-pinned": t.TempDir()}, false)
	if err != nil {
		t.Fatalf("PinRoots: %v", err)
	}
	if res.Action != ActionUnchanged {
		t.Fatalf("action = %q, want %q", res.Action, ActionUnchanged)
	}
	assertDirUnchanged(t, dir, before)
}

// TestPinRoots_RoundTripEndsContention is AC10 at the library boundary: after a
// real repair, re-reading the config from disk and calling the SHIPPING
// ContendingGraphiServers — the exact function `graphi doctor` warns from —
// returns fewer than two entries.
func TestPinRoots_RoundTripEndsContention(t *testing.T) {
	path := contendedConfig(t, "mcpServers")
	c := fakeClient("claude", "mcpServers", path)

	before, err := c.ContendingGraphiServers()
	if err != nil {
		t.Fatalf("ContendingGraphiServers: %v", err)
	}
	if len(before) < 2 {
		t.Fatalf("fixture is not contending: %v", before)
	}

	if _, err := c.PinRoots(map[string]string{"graphi-mars": t.TempDir(), "graphi-venus": t.TempDir()}, false); err != nil {
		t.Fatalf("PinRoots: %v", err)
	}

	after, err := c.ContendingGraphiServers()
	if err != nil {
		t.Fatalf("ContendingGraphiServers after repair: %v", err)
	}
	if len(after) >= 2 {
		t.Fatalf("still contending after repair: %v", after)
	}
	if len(after) != 1 || after[0] != ManagedServerName {
		t.Fatalf("after repair = %v, want exactly the setup-managed %q entry left zero-config", after, ManagedServerName)
	}
}

// TestValidateRoot_MirrorsMcpRootBindConditions pins the validator itself
// against what cmd/internal/runtime.pinExplicitRoot enforces for
// `graphi mcp -root`: absolute-ised, must exist, must be a directory.
func TestValidateRoot_MirrorsMcpRootBindConditions(t *testing.T) {
	dir := t.TempDir()
	got, err := ValidateRoot(dir)
	if err != nil {
		t.Fatalf("ValidateRoot(%q): %v", dir, err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("ValidateRoot returned a non-absolute path %q", got)
	}
	// A relative path resolves against the process working directory and stays
	// a real directory — the value written must be the absolute one.
	rel, err := ValidateRoot(".")
	if err != nil || !filepath.IsAbs(rel) {
		t.Fatalf(`ValidateRoot("."): %q %v`, rel, err)
	}
}
