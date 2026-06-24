package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/samibel/graphi/internal/mcpconfig"
	"github.com/samibel/graphi/internal/state"
)

// detectClaude reports whether a Claude Code MCP client config is configurable
// (the config file or its parent directory exists) and whether graphi is
// already connected (its MCP server entry is present and matches exactly). It is
// best-effort and purely file-ops: it never dials and never panics, returning
// conservative values (connected=false) on any error.
func detectClaude() (configurable, alreadyConnected bool) {
	path, err := mcpconfig.ConfigPath()
	if err != nil {
		return false, false
	}
	if _, err := os.Stat(path); err == nil {
		configurable = true
	} else if _, err := os.Stat(filepath.Dir(path)); err == nil {
		configurable = true
	}
	doc, _ := mcpconfig.Load(path)
	self, _ := os.Executable()
	action, err := mcpconfig.Plan(doc, "graphi", mcpconfig.GraphiEntry(self, []string{"mcp"}))
	alreadyConnected = err == nil && action == mcpconfig.ActionUnchanged
	return configurable, alreadyConnected
}

// isStdinTTY reports whether stdin is an interactive character device (a TTY),
// which gates whether we are allowed to prompt and consume input. The null
// device (/dev/null) is itself a character device but is NOT interactive, so it
// is explicitly excluded: redirecting stdin from /dev/null must be treated as
// non-interactive so the consent gate writes nothing.
func isStdinTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	return !sameFile(os.Stdin, os.DevNull)
}

// sameFile reports whether the open file f refers to the same underlying device
// as the file at path (e.g. os.DevNull). Best-effort: any stat error → false.
func sameFile(f *os.File, path string) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	pi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return os.SameFile(fi, pi)
}

// claudeMemoPath is the state-dir marker that records we have already offered to
// connect Claude Code, so we do not nag on subsequent runs.
func claudeMemoPath() string {
	return filepath.Join(state.StateDir(), "claude-offered")
}

// maybeOfferClaude offers, on the first interactive zero-config run, to connect
// graphi to a detected Claude Code MCP client. Consent is mandatory: when there
// is no TTY it prints a one-line hint and writes NOTHING (no setup, no memo).
// Writing (setup and/or the one-time memo) happens ONLY after an interactive
// prompt. The memo is written for both yes and no so the offer is one-time, but
// never in the non-TTY branch. All behavior is offline file-ops.
func maybeOfferClaude(in io.Reader, out io.Writer, isTTY bool, memoPath string, det func() (bool, bool), doSetup func() error) {
	configurable, connected := det()
	if !configurable || connected {
		return
	}
	if !isTTY {
		fmt.Fprintln(out, "Claude Code detected — run 'graphi claude' to connect graphi.")
		return
	}
	if _, err := os.Stat(memoPath); err == nil {
		return // already offered; don't nag
	}

	fmt.Fprint(out, "Claude Code found — connect graphi? [Y/n] ")
	line, _ := bufio.NewReader(in).ReadString('\n')
	ans := strings.ToLower(strings.TrimSpace(line))
	if ans == "" || ans == "y" || ans == "yes" {
		_ = doSetup()
		fmt.Fprintln(out, "Connected. Try in Claude Code: \"Use graphi to show the callers of <symbol>\"")
	}

	// Record the one-time memo for BOTH yes and no so we don't nag again.
	_ = os.MkdirAll(filepath.Dir(memoPath), 0o700)
	_ = os.WriteFile(memoPath, []byte("-"), 0o644)
}
