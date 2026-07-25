package mcpconfig

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	ServersKey string // top-level JSON key holding the server map
	pathFn     func() (string, error)
}

// ConfigPath resolves this client's config file path. It is best-effort and may
// point at a not-yet-created file (detection is parent-dir aware).
func (c Client) ConfigPath() (string, error) { return c.pathFn() }

// Configurable reports whether this client looks installed: its config file or
// its parent directory exists. Pure file-ops, never dials, conservative on error.
func (c Client) Configurable() bool {
	path, err := c.pathFn()
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
	path, err := c.pathFn()
	if err != nil {
		return "", err
	}
	doc, err := Load(path)
	if err != nil {
		return "", err
	}
	return planKey(doc, c.ServersKey, "graphi", GraphiEntry(binary, args))
}

// Apply registers graphi's stdio entry under this client's servers key,
// atomically and non-destructively (see applyKey). dryRun previews without
// writing.
func (c Client) Apply(binary string, args []string, dryRun bool) (Result, error) {
	path, err := c.pathFn()
	if err != nil {
		return Result{}, err
	}
	return applyKey(path, c.ServersKey, "graphi", GraphiEntry(binary, args), dryRun)
}

// ContendingGraphiServers returns the names (sorted) of server entries in this
// client's config that spawn a graphi binary in ZERO-CONFIG mcp mode — no
// `-db`/`-daemon` pin, so each spawned process resolves its repository from
// its own environment. Two or more such entries (e.g. a hand-added
// "graphi-myrepo" next to the setup-managed "graphi") resolve the SAME
// repository and contend on its cross-process ingest lock: one indexes, the
// rest block, and every one of them reports "repository is not bound" until
// the winner finishes. A missing config yields an empty list.
func (c Client) ContendingGraphiServers() ([]string, error) {
	path, err := c.pathFn()
	if err != nil {
		return nil, err
	}
	doc, err := Load(path)
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

// graphiEntryIsPinned reports whether the entry's args attach to an explicit
// store or daemon (`-db`/`-daemon`, single or double dash, with or without
// `=value`) — a pinned server performs no repository detection and therefore
// never contends on an auto-resolved repo's ingest lock.
func graphiEntryIsPinned(entry map[string]any) bool {
	args, _ := entry["args"].([]any)
	for _, a := range args {
		s, _ := a.(string)
		s = strings.TrimPrefix(s, "-")
		s = strings.TrimPrefix(s, "-")
		if s == "db" || s == "daemon" || strings.HasPrefix(s, "db=") || strings.HasPrefix(s, "daemon=") {
			return true
		}
	}
	return false
}

// Clients returns the known local MCP clients, in stable order. Claude Code is
// first so bare `graphi setup` keeps its historical primary target.
func Clients() []Client {
	return []Client{
		{ID: "claude", Display: "Claude Code", ServersKey: "mcpServers", pathFn: ConfigPath},
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
func devinConfigPath() (string, error) { return homeJoin(".config", "devin", "config.json") }

// windsurfConfigPath is Windsurf's (Codeium) global MCP config.
func windsurfConfigPath() (string, error) { return homeJoin(".codeium", "windsurf", "mcp_config.json") }

// claudeDesktopConfigPath is the Claude Desktop app config.
func claudeDesktopConfigPath() (string, error) {
	return configJoin("Claude", "claude_desktop_config.json")
}
