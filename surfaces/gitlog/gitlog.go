// Package gitlog is the production githistory.GitProvider: a bounded, local
// `git log` reader living at the SURFACE BOUNDARY (the forge precedent — the
// engine itself never executes anything and never touches the network; it
// consumes the parsed commits through the provider seam that
// engine/analysis/githistory defines).
//
// Privacy posture: the only side effect is executing the local `git` binary
// against the local repository — no network egress, no writes, no environment
// mutation. A missing git binary or a non-repository degrades to (nil, error)
// and every consumer of the seam already handles "no provider / no commits"
// gracefully.
package gitlog

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis/githistory"
)

// recordSep and unitSep are ASCII control separators that cannot appear in
// commit metadata, making the parse unambiguous without escaping.
const (
	recordSep = "\x1e" // between commits
	unitSep   = "\x1f" // between fields
)

// Provider reads bounded history from the repository at Root via `git log`.
type Provider struct {
	// Root is the repository root the log is read from.
	Root string
}

// New returns a provider rooted at root.
func New(root string) *Provider { return &Provider{Root: root} }

// Compile-time proof the surface provider satisfies the engine seam.
var _ githistory.GitProvider = (*Provider)(nil)

// Log implements githistory.GitProvider: commits newest-first, bounded by
// maxCommits and since (whichever is tighter), each with the touched file
// paths (normalized repo-relative).
func (p *Provider) Log(ctx context.Context, maxCommits int, since time.Time) ([]githistory.Commit, error) {
	args := []string{
		"-C", p.Root,
		"log",
		"--no-color",
		"--name-only",
		"--no-merges",
		"--date=unix",
		"--pretty=format:" + recordSep + "%H" + unitSep + "%an" + unitSep + "%ad",
	}
	if maxCommits > 0 {
		args = append(args, "-n", strconv.Itoa(maxCommits))
	}
	if !since.IsZero() {
		args = append(args, "--since="+strconv.FormatInt(since.Unix(), 10))
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("gitlog: git log failed: %s", msg)
	}
	return parseLog(out.String())
}

// parseLog turns the separator-framed `git log --name-only` output into
// commits. Exported logic is kept pure and separately tested.
func parseLog(raw string) ([]githistory.Commit, error) {
	var commits []githistory.Commit
	for _, record := range strings.Split(raw, recordSep) {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}
		lines := strings.Split(record, "\n")
		fields := strings.Split(lines[0], unitSep)
		if len(fields) != 3 {
			return nil, fmt.Errorf("gitlog: malformed commit header %q", lines[0])
		}
		epoch, err := strconv.ParseInt(strings.TrimSpace(fields[2]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("gitlog: malformed commit date %q", fields[2])
		}
		c := githistory.Commit{
			SHA:       strings.TrimSpace(fields[0]),
			Author:    strings.TrimSpace(fields[1]),
			Timestamp: time.Unix(epoch, 0).UTC(),
		}
		for _, line := range lines[1:] {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			c.FilesChanged = append(c.FilesChanged, model.NormalizePath(line))
		}
		commits = append(commits, c)
	}
	return commits, nil
}
