package mcpconfig

import (
	"os"
	"path/filepath"
)

// Client is a local MCP client graphi can register itself into. The mcpconfig
// machinery (atomic write + fail-closed backup + non-destructive merge) is
// client-agnostic; a Client captures only what differs between clients: the
// config file location and the top-level JSON key under which stdio servers are
// listed ("mcpServers" for Claude Code / Cursor / Windsurf / Claude Desktop,
// "servers" for VS Code). The server entry shape (ServerEntry) is shared.
//
// Every adapter targets the GLOBAL/user-level config that a locally-running
// stdio server can be reached from. Cloud agents (Devin, the GitHub Copilot
// coding agent) run in a remote sandbox and cannot reach a local stdio graphi,
// so they are deliberately NOT clients here.
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

// Clients returns the known local MCP clients, in stable order. Claude Code is
// first so bare `graphi setup` keeps its historical primary target.
func Clients() []Client {
	return []Client{
		{ID: "claude", Display: "Claude Code", ServersKey: "mcpServers", pathFn: ConfigPath},
		{ID: "copilot", Display: "GitHub Copilot (VS Code)", ServersKey: "servers", pathFn: vscodeConfigPath},
		{ID: "cursor", Display: "Cursor", ServersKey: "mcpServers", pathFn: cursorConfigPath},
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

// windsurfConfigPath is Windsurf's (Codeium) global MCP config.
func windsurfConfigPath() (string, error) { return homeJoin(".codeium", "windsurf", "mcp_config.json") }

// claudeDesktopConfigPath is the Claude Desktop app config.
func claudeDesktopConfigPath() (string, error) {
	return configJoin("Claude", "claude_desktop_config.json")
}
