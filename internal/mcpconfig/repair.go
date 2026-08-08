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

// pinnableArgs reads the args list repair is about to APPEND to, and refuses
// every shape it cannot append to honestly. It returns the existing args
// unchanged on success.
//
// It exists because appending through a discarded type assertion
// (`args, _ := entry["args"].([]any)`) is not an append at all: for any args
// that is not a JSON array the assertion yields nil, and the "append" silently
// REPLACES the user's value with just `-root <path>`. That destroys an existing
// argument (AC7) and leaves an entry invoking the bare binary with no
// subcommand, which cannot bind (AC5's invariant) — while the command still
// reports success. The population `--repair` targets is precisely the people who
// hand-edited their client config, so `"args": "mcp"` written as a string rather
// than an array is a live shape, not a hypothetical one.
//
// The three refusals, all fail-closed and all before any mutation:
//   - args absent, or not a JSON array — there is nothing to append to, and
//     replacing it is exactly the destruction AC7 forbids.
//   - an element that is not a string — repair cannot read the argument list it
//     claims to be preserving, so it may not claim to have pinned it.
//   - args[0] != "mcp" — `graphi` dispatches on its FIRST argument
//     (cmd/graphi/main.go: os.Args[1]), so an entry that does not lead with the
//     `mcp` subcommand is not an MCP server and adding `-root` cannot make it
//     one. Pinning it would produce a confident report about an entry that still
//     cannot bind.
//
// Repair reports these rather than repairing them: reshaping a hand-written
// value into what graphi guesses it meant is the same class of inference AC3
// rules out, and rewriting `"mcp -labs"` into `["mcp", "-labs"]` would be
// graphi reconstructing an intent the user alone owns.
func pinnableArgs(entry map[string]any) ([]any, error) {
	raw, present := entry["args"]
	if !present {
		return nil, fmt.Errorf(`entry has no "args": a graphi MCP entry must invoke the "mcp" subcommand, and adding -root to nothing would not make it bindable`)
	}
	args, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf(`entry's "args" is %T, not a JSON array: refusing to overwrite it (fix it by hand, then re-run)`, raw)
	}
	for i, a := range args {
		if _, ok := a.(string); !ok {
			return nil, fmt.Errorf(`entry's "args"[%d] is %T, not a string: refusing to pin an argument list it cannot read`, i, a)
		}
	}
	if len(args) == 0 || args[0].(string) != "mcp" {
		return nil, fmt.Errorf(`entry's "args" does not start with the "mcp" subcommand: it does not run a graphi MCP server, and adding -root would not make it bindable`)
	}
	return args, nil
}

// UnpinnableReasons reports, per server name, why repair cannot pin that entry
// — a name is present in the result ONLY when it cannot be pinned, so an empty
// result means every name is fine. It performs one config read and no writes.
//
// It is the read-side twin of the refusals pinRootsKey enforces at the write
// boundary, and exists so the command layer can report a bad entry, skip it,
// and still repair its healthy neighbours — the same per-entry granularity
// ValidateRoot already gets. The write boundary keeps its own copy of every
// check: this one is for reporting, that one is the guard.
//
// An entry that is already pinned yields no reason. It needs no repair, so
// there is nothing to refuse.
func (c Client) UnpinnableReasons(names []string) (map[string]string, error) {
	path, err := c.pathFn()
	if err != nil {
		return nil, err
	}
	doc, err := Load(path)
	if err != nil {
		return nil, err
	}
	servers, _ := doc[c.ServersKey].(map[string]any)
	out := map[string]string{}
	for _, name := range names {
		raw, ok := servers[name]
		if !ok {
			out[name] = "no such server entry"
			continue
		}
		entry, ok := raw.(map[string]any)
		if !ok {
			out[name] = "entry is not a JSON object"
			continue
		}
		command, _ := entry["command"].(string)
		if !isGraphiCommand(command) {
			out[name] = fmt.Sprintf("command %q is not a graphi binary", command)
			continue
		}
		if graphiEntryIsPinned(entry) {
			continue
		}
		if _, err := pinnableArgs(entry); err != nil {
			out[name] = err.Error()
		}
	}
	return out, nil
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
//   - An entry that is missing, is not a JSON object, whose command is not a
//     graphi binary (isGraphiCommand), or whose args repair cannot append to
//     honestly (pinnableArgs) is an ERROR, not a skip — and the error is
//     returned BEFORE any write, so the config stays byte-identical. One
//     refusal aborts the whole batch; nothing half-repaired reaches disk.
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
		args  []any
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
		args, err := pinnableArgs(entry)
		if err != nil {
			return Result{}, fmt.Errorf("mcpconfig: pin %q in %s: %w", name, path, err)
		}
		plan = append(plan, pin{name: name, root: root, args: args, entry: entry})
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
		// p.args came back from pinnableArgs, so this is an append onto a list
		// that was READ successfully — never onto the zero value of a discarded
		// type assertion, which would silently overwrite whatever was there.
		p.entry["args"] = append(append([]any{}, p.args...), "-root", p.root)
	}
	doc[serversKey] = servers

	backupPath, err := writeAtomicWithBackup(path, doc)
	if err != nil {
		return Result{}, err
	}
	return Result{Action: act, Diff: diff, BackupPath: backupPath}, nil
}
