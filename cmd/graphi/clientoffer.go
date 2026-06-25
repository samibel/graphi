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

// detectClients returns the local MCP clients that look installed
// (config file or parent dir present) AND do not already have graphi wired with
// the exact entry we would write. It is best-effort and purely file-ops: it
// never dials and never panics, skipping any client that errors.
func detectClients() []mcpconfig.Client {
	self, _ := os.Executable()
	var out []mcpconfig.Client
	for _, c := range mcpconfig.Clients() {
		if !c.Configurable() {
			continue
		}
		action, err := c.Plan(self, []string{"mcp"})
		if err != nil || action == mcpconfig.ActionUnchanged {
			continue // unreadable, or already connected → nothing to offer
		}
		out = append(out, c)
	}
	return out
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

// clientsMemoPath is the state-dir marker that records we have already offered to
// connect MCP clients, so we do not nag on subsequent runs.
func clientsMemoPath() string {
	return filepath.Join(state.StateDir(), "clients-offered")
}

// displayList renders the client display names as "A", "A and B", or
// "A, B and C".
func displayList(cs []mcpconfig.Client) string {
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = c.Display
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}

// maybeOfferClients offers, on the first interactive zero-config run, to connect
// graphi to every detected-but-unconnected local MCP client in ONE consent
// prompt. Consent is mandatory: when there is no TTY it prints a one-line hint
// and writes NOTHING (no setup, no memo). Writing (setup and/or the one-time
// memo) happens ONLY after an interactive prompt. The memo is written for both
// yes and no so the offer is one-time, but never in the non-TTY branch. All
// behavior is offline file-ops.
func maybeOfferClients(in io.Reader, out io.Writer, isTTY bool, memoPath string, detect func() []mcpconfig.Client, doSetup func([]mcpconfig.Client) error) {
	clients := detect()
	if len(clients) == 0 {
		return
	}
	names := displayList(clients)
	if !isTTY {
		fmt.Fprintf(out, "%s detected — run 'graphi setup' to connect graphi.\n", names)
		return
	}
	if _, err := os.Stat(memoPath); err == nil {
		return // already offered; don't nag
	}

	fmt.Fprintf(out, "%s found — connect graphi to them? [Y/n] ", names)
	line, _ := bufio.NewReader(in).ReadString('\n')
	ans := strings.ToLower(strings.TrimSpace(line))
	if ans == "" || ans == "y" || ans == "yes" {
		_ = doSetup(clients)
		fmt.Fprintln(out, `Connected. Try in your agent: "Use graphi to show the callers of <symbol>"`)
	}

	// Record the one-time memo for BOTH yes and no so we don't nag again.
	_ = os.MkdirAll(filepath.Dir(memoPath), 0o700)
	_ = os.WriteFile(memoPath, []byte("-"), 0o644)
}
