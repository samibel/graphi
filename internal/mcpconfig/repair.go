package mcpconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ManagedServerName is the server key `graphi setup` registers graphi under
// (see Client.Apply). It is the ONE entry repair leaves zero-config: setup owns
// it, setup keeps it current, and it is the entry that follows whichever
// repository the client is working in. Every OTHER contending entry was added
// by hand and must say which repository it serves instead of racing for the
// same one.
const ManagedServerName = "graphi"

// ContendingEntry is one server entry that spawns a graphi binary in ZERO-CONFIG
// mcp mode — no `-db`/`-daemon`/`-root` pin — and therefore resolves its
// repository from its own environment. Two or more in one client resolve the
// SAME repository and contend on its cross-process ingest lock.
//
// The struct carries the entry's NAME and whether setup manages it, and nothing
// else. In particular it carries no candidate repository: a contending entry
// states no repository anywhere (that is precisely what makes it contend), so
// there is nothing in the config for repair to read. The root comes from the
// user (see PinRoots' callers) and from nowhere else.
type ContendingEntry struct {
	Name    string // the server key, e.g. "graphi-myrepo"
	Managed bool   // true for the setup-managed entry (Name == ManagedServerName)
}

// ContendingEntries returns this client's contending zero-config graphi entries,
// sorted by name, flagging the setup-managed one. It is a thin reading over
// Client.ContendingGraphiServers — the same list `graphi doctor` warns about —
// so repair and detection can never drift apart. A missing config yields an
// empty list; fewer than two entries means the client has no contention.
func (c Client) ContendingEntries() ([]ContendingEntry, error) {
	names, err := c.ContendingGraphiServers()
	if err != nil {
		return nil, err
	}
	out := make([]ContendingEntry, 0, len(names))
	for _, name := range names {
		out = append(out, ContendingEntry{Name: name, Managed: name == ManagedServerName})
	}
	return out, nil
}

// WithConfigPath returns a copy of c whose config file is path. It exists so
// `--config <path>` can pin a single file without the caller having to
// re-derive the client's servers key or duplicate its adapter.
func (c Client) WithConfigPath(path string) Client {
	c.pathFn = func() (string, error) { return path, nil }
	return c
}

// ValidateRoot resolves root to an absolute path and applies exactly the
// conditions `graphi mcp -root` enforces at bind time
// (cmd/internal/runtime.pinExplicitRoot): the path must exist and must be a
// directory. No upward walk, no home-directory guard — an explicit root is
// deliberate intent, and main's `-root` treats it as such.
//
// It exists so a repair can never turn a CONTENDING entry into a DEAD one. A
// bad `-root` does not fail when it is written; it fails much later, opaquely,
// as the MCP server's retryable -32002, and the config on disk looks repaired.
// Validating here is what makes the write honest.
func ValidateRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("empty repository path")
	}
	abs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", root, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("root %q does not exist", abs)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("root %q is not a directory", abs)
	}
	return abs, nil
}

// PinRoots makes each named server entry say which repository it serves, by
// APPENDING `-root <path>` to its args. roots maps server name → repository
// path, supplied by the user. dryRun computes the Action and diff but writes NO
// file.
//
// It is deliberately additive and fail-closed:
//   - It never deletes an entry, never removes or reorders an existing
//     argument, and never touches an entry that was not named.
//   - Every root is validated (ValidateRoot: exists, is a directory) BEFORE any
//     mutation, so a repair cannot replace contention with an entry that cannot
//     bind.
//   - An entry that is missing, is not a JSON object, or whose command is not a
//     graphi binary (isGraphiCommand) is an ERROR, not a skip — and the error is
//     returned BEFORE any write, so the config stays byte-identical.
//   - An entry that already carries a pinning flag is left exactly as it is.
//   - The write itself goes through writeAtomicWithBackup, so it has the same
//     atomic temp+rename, fail-closed backup, post-write verify+restore and
//     preservation of unrelated keys that applyKey already has.
func (c Client) PinRoots(roots map[string]string, dryRun bool) (Result, error) {
	path, err := c.pathFn()
	if err != nil {
		return Result{}, err
	}
	return pinRootsKey(path, c.ServersKey, roots, dryRun)
}

// pinRootsKey is PinRoots generalized over the top-level servers key, mirroring
// the applyKey/Apply split. It runs a complete validation pass before it mutates
// anything, so a refusal anywhere leaves the whole document untouched.
func pinRootsKey(path, serversKey string, roots map[string]string, dryRun bool) (Result, error) {
	doc, err := Load(path)
	if err != nil {
		return Result{}, err
	}
	servers, _ := doc[serversKey].(map[string]any)

	names := make([]string, 0, len(roots))
	for name := range roots {
		names = append(names, name)
	}
	sort.Strings(names)

	// Pass 1 — validate everything, mutate nothing.
	type pin struct {
		name  string
		root  string
		entry map[string]any
	}
	plan := make([]pin, 0, len(names))
	for _, name := range names {
		raw, ok := servers[name]
		if !ok {
			return Result{}, fmt.Errorf("mcpconfig: pin %q: no such server entry in %s", name, path)
		}
		entry, ok := raw.(map[string]any)
		if !ok {
			return Result{}, fmt.Errorf("mcpconfig: pin %q: entry in %s is not a JSON object", name, path)
		}
		command, _ := entry["command"].(string)
		if !isGraphiCommand(command) {
			return Result{}, fmt.Errorf("mcpconfig: pin %q: command %q is not a graphi binary", name, command)
		}
		root, err := ValidateRoot(roots[name])
		if err != nil {
			return Result{}, fmt.Errorf("mcpconfig: pin %q: %w", name, err)
		}
		if graphiEntryIsPinned(entry) {
			continue // already names what it serves
		}
		plan = append(plan, pin{name: name, root: root, entry: entry})
	}

	act := ActionUnchanged
	if len(plan) > 0 {
		act = ActionUpdated
	}
	var b strings.Builder
	fmt.Fprintf(&b, "config: %s\naction: %s\n", path, act)
	for _, p := range plan {
		fmt.Fprintf(&b, "pin: %s args += -root %s\n", p.name, p.root)
	}
	diff := b.String()

	if dryRun || act == ActionUnchanged {
		return Result{Action: act, Diff: diff}, nil
	}

	// Pass 2 — mutate, then write atomically through the shared path.
	for _, p := range plan {
		args, _ := p.entry["args"].([]any)
		p.entry["args"] = append(append([]any{}, args...), "-root", p.root)
	}
	doc[serversKey] = servers

	backupPath, err := writeAtomicWithBackup(path, doc)
	if err != nil {
		return Result{}, err
	}
	return Result{Action: act, Diff: diff, BackupPath: backupPath}, nil
}
