// Package testenv isolates tests from real user state and client profiles.
package testenv

import (
	"os"
	"path/filepath"
	"strings"
)

// TB is the subset of testing.TB needed by Isolate.
type TB interface {
	Helper()
	TempDir() string
	Setenv(string, string)
	Fatalf(string, ...any)
}

// Paths names the synthetic home, state, config, and client directories used
// by one test. Every path is absolute and contained under Root.
type Paths struct {
	Root             string
	Home             string
	HomePath         string
	StateHome        string
	ConfigHome       string
	AppData          string
	LocalAppData     string
	ClaudeConfigDir  string
	ClaudeConfigPath string
	CodexHome        string
}

// Isolate redirects the standard Unix and Windows user-directory variables,
// plus the documented client config homes Graphi supports, into one temporary
// tree. Tests using it cannot fall back to a real user profile.
func Isolate(t TB) Paths {
	t.Helper()
	root, err := filepath.Abs(t.TempDir())
	if err != nil {
		t.Fatalf("resolve isolated test root: %v", err)
	}
	home := filepath.Join(root, "home")
	paths := Paths{
		Root:             root,
		Home:             home,
		HomePath:         strings.TrimPrefix(home, filepath.VolumeName(home)),
		StateHome:        filepath.Join(root, "state"),
		ConfigHome:       filepath.Join(root, "config"),
		AppData:          filepath.Join(root, "appdata"),
		LocalAppData:     filepath.Join(root, "local-appdata"),
		ClaudeConfigDir:  filepath.Join(home, ".claude"),
		ClaudeConfigPath: filepath.Join(home, ".claude.json"),
		CodexHome:        filepath.Join(home, ".codex"),
	}
	for _, dir := range []string{
		paths.Home, paths.StateHome, paths.ConfigHome, paths.AppData,
		paths.LocalAppData, paths.ClaudeConfigDir, paths.CodexHome,
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create isolated test directory %q: %v", dir, err)
		}
	}

	for key, value := range map[string]string{
		"HOME":               paths.Home,
		"USERPROFILE":        paths.Home,
		"HOMEDRIVE":          filepath.VolumeName(paths.Home),
		"HOMEPATH":           paths.HomePath,
		"XDG_STATE_HOME":     paths.StateHome,
		"XDG_CONFIG_HOME":    paths.ConfigHome,
		"APPDATA":            paths.AppData,
		"LOCALAPPDATA":       paths.LocalAppData,
		"CLAUDE_CONFIG_DIR":  paths.ClaudeConfigDir,
		"CLAUDE_CONFIG_PATH": paths.ClaudeConfigPath,
		"CODEX_HOME":         paths.CodexHome,
	} {
		t.Setenv(key, value)
	}
	return paths
}
