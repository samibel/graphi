package mcpconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Format selects the parser/writer used for a client's real configuration
// syntax. The zero value remains JSON for existing adapters and tests.
type Format string

const (
	FormatJSON Format = "json"
	FormatTOML Format = "toml"
)

// Client is a local MCP client graphi can register itself into. The mcpconfig
// machinery (atomic write + fail-closed backup + non-destructive merge) is
// client-agnostic; a Client captures only what differs between clients: the
// config file location and the top-level JSON key under which stdio servers are
// listed ("mcpServers" for Claude Code / Cursor / Windsurf / Claude Desktop,
// "servers" for VS Code). The server entry shape (ServerEntry) is shared.
//
// Every adapter targets the GLOBAL/user-level config that a locally-running
// stdio server can be reached from. Purely cloud-sandboxed agents (the GitHub
// Copilot coding agent's remote runner) cannot reach a local stdio graphi and
// are deliberately NOT clients here — but locally-installed agent CLIs that
// spawn stdio servers themselves (Devin CLI) are.
type Client struct {
	ID         string // stable identifier, e.g. "claude", "cursor"
	Display    string // human label, e.g. "Claude Code"
	ServersKey string // top-level JSON/TOML key holding the server map
	Format     Format // JSON by default; Codex uses TOML
	pathFn     func() (string, error)
}

// EntryState is a read-only semantic plan for one named server entry.
type EntryState struct {
	Action             Action
	Current            any
	Target             any
	BeforeConfigDigest string
	TargetConfigDigest string
}

// RemovalState is the read-only before/after plan for deleting one entry.
// Current and the complete-config digests let a caller authorize the exact
// observed state and require the writer to compare it again under its lock.
type RemovalState struct {
	Action             Action
	Current            any
	BeforeConfigDigest string
	TargetConfigDigest string
}

// ConfigPath resolves this client's config file path to a stable absolute
// identity. It may point at a not-yet-created file (detection is parent-dir
// aware), but it never remains dependent on a later working directory.
func (c Client) ConfigPath() (string, error) {
	path, err := c.pathFn()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(path) == "" {
		return "", errors.New("mcpconfig: empty config path")
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("mcpconfig: resolve config path: %w", err)
	}
	return abs, nil
}

// WithConfigPath overrides only the target path. The adapter's selected parser
// and format remain unchanged, so a Codex override is still TOML regardless of
// its filename extension.
func (c Client) WithConfigPath(path string) Client {
	c.pathFn = func() (string, error) { return path, nil }
	return c
}

// Configurable reports whether this client looks installed: its config file or
// its parent directory exists. Pure file-ops, never dials, conservative on error.
func (c Client) Configurable() bool {
	path, err := c.ConfigPath()
	if err != nil {
		return false
	}
	if _, err := os.Stat(path); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Dir(path)); err == nil {
		return true
	}
	return false
}

// Plan reports the Action that registering graphi (with the given binary/args)
// would take against this client's current config, without writing.
func (c Client) Plan(binary string, args []string) (Action, error) {
	return c.PlanEntry("graphi", GraphiEntry(binary, args))
}

// PlanEntry reports the action for a fully specified named server entry.
func (c Client) PlanEntry(name string, entry ServerEntry) (Action, error) {
	path, err := c.ConfigPath()
	if err != nil {
		return "", err
	}
	if c.Format == FormatTOML {
		return planCodex(path, name, entry)
	}
	doc, err := Load(path)
	if err != nil {
		return "", err
	}
	return planKey(doc, c.ServersKey, name, entry)
}

// Apply registers graphi's stdio entry under this client's servers key,
// atomically and non-destructively (see applyKey). dryRun previews without
// writing.
func (c Client) Apply(binary string, args []string, dryRun bool) (Result, error) {
	return c.ApplyEntry("graphi", GraphiEntry(binary, args), dryRun)
}

// ApplyEntry registers a fully specified named stdio entry through the same
// non-destructive writer used by the legacy graphi wrapper.
func (c Client) ApplyEntry(name string, entry ServerEntry, dryRun bool) (Result, error) {
	return c.ApplyEntryObserved(name, entry, "", dryRun)
}

// ApplyEntryObserved applies only if the complete config still has the digest
// authorized by the caller. The comparison happens after both in-process and
// cross-process writer locks are held.
func (c Client) ApplyEntryObserved(name string, entry ServerEntry, beforeConfigDigest string, dryRun bool) (Result, error) {
	path, err := c.ConfigPath()
	if err != nil {
		return Result{}, err
	}
	if c.Format == FormatTOML {
		return applyCodexObserved(path, name, entry, beforeConfigDigest, dryRun)
	}
	return applyKeyObserved(path, c.ServersKey, name, entry, beforeConfigDigest, dryRun)
}

// Entries returns the selected client's named server map without modifying it.
func (c Client) Entries() (map[string]any, error) {
	path, err := c.ConfigPath()
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if c.Format == FormatTOML {
		raw, readErr := os.ReadFile(path)
		if os.IsNotExist(readErr) {
			return map[string]any{}, nil
		}
		if readErr != nil {
			return nil, readErr
		}
		doc, err = decodeTOML(raw)
		if err != nil {
			return nil, err
		}
		return tomlObject(doc, c.ServersKey)
	}
	doc, err = Load(path)
	if err != nil {
		return nil, err
	}
	return serverMap(doc, c.ServersKey)
}

// PlanEntryState returns the current and post-merge entry values used for
// ownership and pending-change digests.
func (c Client) PlanEntryState(name string, desired ServerEntry) (EntryState, error) {
	if c.Format == FormatTOML {
		path, err := c.ConfigPath()
		if err != nil {
			return EntryState{}, err
		}
		return planCodexEntryState(path, name, desired)
	}
	path, err := c.ConfigPath()
	if err != nil {
		return EntryState{}, err
	}
	snapshot, err := loadSnapshot(path)
	if err != nil {
		return EntryState{}, err
	}
	servers, err := serverMap(snapshot.doc, c.ServersKey)
	if err != nil {
		return EntryState{}, err
	}
	current, exists := servers[name]
	target := any(desired)
	action := ActionCreated
	if exists {
		target, err = mergeServerEntry(current, desired)
		if err != nil {
			return EntryState{}, err
		}
		action = ActionUpdated
		if equalJSON(current, target) {
			action = ActionUnchanged
		}
	}
	beforeDigest := configDigest(snapshot.raw)
	targetDigest := beforeDigest
	if action != ActionUnchanged {
		docCopy := make(map[string]any, len(snapshot.doc)+1)
		for key, value := range snapshot.doc {
			docCopy[key] = value
		}
		serversCopy := make(map[string]any, len(servers)+1)
		for key, value := range servers {
			serversCopy[key] = value
		}
		serversCopy[name] = target
		docCopy[c.ServersKey] = serversCopy
		raw, marshalErr := json.MarshalIndent(docCopy, "", "  ")
		if marshalErr != nil {
			return EntryState{}, marshalErr
		}
		targetDigest = configDigest(raw)
	}
	return EntryState{
		Action: action, Current: current, Target: target,
		BeforeConfigDigest: beforeDigest, TargetConfigDigest: targetDigest,
	}, nil
}

// PlanRemoveEntryState returns the exact current entry and the complete config
// digests before and after its removal without modifying the file.
func (c Client) PlanRemoveEntryState(name string) (RemovalState, error) {
	path, err := c.ConfigPath()
	if err != nil {
		return RemovalState{}, err
	}
	if c.Format == FormatTOML {
		return planCodexRemovalState(path, name)
	}
	snapshot, err := loadSnapshot(path)
	if err != nil {
		return RemovalState{}, err
	}
	servers, err := serverMap(snapshot.doc, c.ServersKey)
	if err != nil {
		return RemovalState{}, err
	}
	current, exists := servers[name]
	beforeDigest := configDigest(snapshot.raw)
	if !exists {
		return RemovalState{Action: ActionUnchanged, BeforeConfigDigest: beforeDigest, TargetConfigDigest: beforeDigest}, nil
	}
	docCopy := make(map[string]any, len(snapshot.doc)+1)
	for key, value := range snapshot.doc {
		docCopy[key] = value
	}
	serversCopy := make(map[string]any, len(servers))
	for key, value := range servers {
		if key != name {
			serversCopy[key] = value
		}
	}
	docCopy[c.ServersKey] = serversCopy
	raw, err := json.MarshalIndent(docCopy, "", "  ")
	if err != nil {
		return RemovalState{}, err
	}
	return RemovalState{Action: ActionRemoved, Current: current, BeforeConfigDigest: beforeDigest, TargetConfigDigest: configDigest(raw)}, nil
}

func configDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// RemoveEntry removes one named entry through the client's real format writer.
func (c Client) RemoveEntry(name string, dryRun bool) (Result, error) {
	return c.RemoveEntryObserved(name, nil, "", dryRun)
}

// RemoveEntryObserved removes only the entry/config state the caller already
// authorized. Both comparisons are repeated under the writer locks.
func (c Client) RemoveEntryObserved(name string, expectedCurrent any, beforeConfigDigest string, dryRun bool) (Result, error) {
	path, err := c.ConfigPath()
	if err != nil {
		return Result{}, err
	}
	if c.Format == FormatTOML {
		return removeCodexObserved(path, name, expectedCurrent, beforeConfigDigest, dryRun)
	}
	return removeKeyObserved(path, c.ServersKey, name, expectedCurrent, beforeConfigDigest, dryRun)
}

// ContendingGraphiServers returns the names (sorted) of server entries in this
// client's config that spawn a graphi binary in ZERO-CONFIG mcp mode — no
// `-db`/`-daemon`/`-root` pin, so each spawned process resolves its repository
// from its own environment. Two or more such entries (e.g. a hand-added
// "graphi-myrepo" next to the setup-managed "graphi") resolve the SAME
// repository and contend on its cross-process ingest lock: one indexes, the
// rest block, and every one of them reports "repository is not bound" until
// the winner finishes. A missing config yields an empty list.
func (c Client) ContendingGraphiServers() ([]string, error) {
	path, err := c.ConfigPath()
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if c.Format == FormatTOML {
		raw, readErr := os.ReadFile(path)
		if os.IsNotExist(readErr) {
			return nil, nil
		}
		if readErr != nil {
			return nil, readErr
		}
		doc, err = decodeTOML(raw)
	} else {
		doc, err = Load(path)
	}
	if err != nil {
		return nil, err
	}
	servers, _ := doc[c.ServersKey].(map[string]any)
	var names []string
	for name, raw := range servers {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		command, _ := entry["command"].(string)
		if !isGraphiCommand(command) {
			continue
		}
		if graphiEntryIsPinned(entry) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// isGraphiCommand reports whether a config entry's command launches a graphi
// binary. The basename split accepts both path separators regardless of host
// OS — a config file records the path style of the machine it was written on,
// and being lenient here can only add a WARNING, never change behavior.
func isGraphiCommand(command string) bool {
	name := strings.ToLower(command)
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	return strings.TrimSuffix(name, ".exe") == "graphi"
}

// graphiEntryIsPinned reports whether the entry's args bind the session
// outright — to an explicit store or daemon (`-db`/`-daemon`) or to an explicit
// repository root (`-root`), single or double dash, with or without `=value`.
// A pinned server performs no repository detection and therefore never contends
// on an auto-resolved repo's ingest lock.
//
// `-root` differs from the other two in one way that matters here: it pins only
// when it actually names a repository. `-db` keeps its historical value-agnostic
// leniency (a bare `-db` still reads as pinned), but a bare `-root` is REJECTED
// by `graphi mcp` itself as an unknown argument (cmd/graphi/serve.go
// extractMCPFlags), and an empty value falls back to detection — neither pins
// anything, so neither may silence the contention warning. The value of
// `-root <path>` is the FOLLOWING argument, so the scan is index-aware.
//
// Only args are considered. GRAPHI_ROOT in the entry's Env map also pins a
// session at runtime, but whether an env-pinned entry counts as pinned for
// contention purposes is a separate judgement (SW-163 scope note).
func graphiEntryIsPinned(entry map[string]any) bool {
	raw, _ := entry["args"].([]any)
	args := make([]string, len(raw))
	for i, a := range raw {
		args[i], _ = a.(string)
	}
	for i, arg := range args {
		s := strings.TrimPrefix(arg, "-")
		s = strings.TrimPrefix(s, "-")
		switch {
		case s == "db" || s == "daemon" || strings.HasPrefix(s, "db=") || strings.HasPrefix(s, "daemon="):
			return true
		case strings.HasPrefix(s, "root="):
			if strings.TrimPrefix(s, "root=") != "" {
				return true
			}
		case s == "root":
			if i+1 < len(args) && args[i+1] != "" {
				return true
			}
		}
	}
	return false
}

// Clients returns the known local MCP clients, in stable order. Claude Code is
// first so bare `graphi setup` keeps its historical primary target.
func Clients() []Client {
	return []Client{
		{ID: "claude", Display: "Claude Code", ServersKey: "mcpServers", pathFn: ConfigPath},
		{ID: "codex", Display: "Codex", ServersKey: "mcp_servers", Format: FormatTOML, pathFn: codexConfigPath},
		{ID: "copilot", Display: "GitHub Copilot (VS Code)", ServersKey: "servers", pathFn: vscodeConfigPath},
		{ID: "cursor", Display: "Cursor", ServersKey: "mcpServers", pathFn: cursorConfigPath},
		{ID: "devin", Display: "Devin CLI", ServersKey: "mcpServers", pathFn: devinConfigPath},
		{ID: "windsurf", Display: "Windsurf", ServersKey: "mcpServers", pathFn: windsurfConfigPath},
		{ID: "claude-desktop", Display: "Claude Desktop", ServersKey: "mcpServers", pathFn: claudeDesktopConfigPath},
	}
}

// ClientByID returns the registered client with the given id.
func ClientByID(id string) (Client, bool) {
	for _, c := range Clients() {
		if c.ID == id {
			return c, true
		}
	}
	return Client{}, false
}

// ClientIDs returns the registered client ids in stable order (for help text).
func ClientIDs() []string {
	cs := Clients()
	ids := make([]string, len(cs))
	for i, c := range cs {
		ids[i] = c.ID
	}
	return ids
}

// --- per-client path resolvers -------------------------------------------------
//
// os.UserConfigDir() already encodes the per-OS base (macOS:
// ~/Library/Application Support, Linux: ~/.config, Windows: %AppData%), which is
// exactly where VS Code and Claude Desktop keep their user config — so those two
// need no GOOS switch. Cursor and Windsurf use fixed home-relative dotdirs.

func homeJoin(parts ...string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{home}, parts...)...), nil
}

func configJoin(parts ...string) (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{base}, parts...)...), nil
}

// vscodeConfigPath is the VS Code user-level MCP config (Copilot agent mode).
func vscodeConfigPath() (string, error) { return configJoin("Code", "User", "mcp.json") }

// cursorConfigPath is Cursor's global MCP config.
func cursorConfigPath() (string, error) { return homeJoin(".cursor", "mcp.json") }

// devinConfigPath is the Devin CLI's config. Devin uses an XDG-style fixed
// ~/.config dotdir on every platform (NOT os.UserConfigDir, which would map to
// ~/Library/Application Support on macOS).
func devinConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return resolveDevinConfigPath(runtime.GOOS, home, os.Getenv("APPDATA"))
}

func resolveDevinConfigPath(goos, home, appData string) (string, error) {
	base := filepath.Join(home, ".config", "devin")
	if goos == "windows" {
		if strings.TrimSpace(appData) == "" {
			return "", fmt.Errorf("mcpconfig: APPDATA is required for Devin on Windows")
		}
		base = filepath.Join(appData, "devin")
	}
	dedicated := filepath.Join(base, "mcp_config.json")
	legacy := filepath.Join(base, "config.json")
	dedicatedExists := pathExists(dedicated)
	legacyExists := pathExists(legacy)
	if dedicatedExists && legacyExists {
		return "", fmt.Errorf("mcpconfig: ambiguous Devin MCP configs at %s and %s; use an explicit config path", dedicated, legacy)
	}
	if legacyExists {
		return legacy, nil
	}
	return dedicated, nil
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// windsurfConfigPath is Windsurf's (Codeium) global MCP config.
func windsurfConfigPath() (string, error) { return homeJoin(".codeium", "windsurf", "mcp_config.json") }

// claudeDesktopConfigPath is the Claude Desktop app config.
func claudeDesktopConfigPath() (string, error) {
	return configJoin("Claude", "claude_desktop_config.json")
}
