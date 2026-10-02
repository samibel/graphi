package testenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsolateRedirectsUserStateAndClientHomes(t *testing.T) {
	paths := Isolate(t)

	wantEnv := map[string]string{
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
	}
	for key, want := range wantEnv {
		if got := os.Getenv(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}

	for name, path := range map[string]string{
		"root": paths.Root, "home": paths.Home, "state": paths.StateHome,
		"config": paths.ConfigHome, "appdata": paths.AppData,
		"local-appdata": paths.LocalAppData, "claude": paths.ClaudeConfigDir,
		"codex": paths.CodexHome,
	} {
		if !filepath.IsAbs(path) {
			t.Errorf("%s path is not absolute: %q", name, path)
		}
		if rel, err := filepath.Rel(paths.Root, path); err != nil || rel == ".." || filepath.IsAbs(rel) {
			t.Errorf("%s path %q escapes isolated root %q (rel=%q, err=%v)", name, path, paths.Root, rel, err)
		}
	}
}
