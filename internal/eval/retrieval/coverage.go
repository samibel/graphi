package retrieval

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CheckSpanCoverage is AC-9 as a function: every judged span in d must
// resolve to a regular file under root whose line count reaches end_line and
// whose [start_line, end_line] text contains the judgement's anchor. Every
// failing span is reported, naming its query, so a stale dataset is fixed in
// one pass rather than one span per test run.
func CheckSpanCoverage(root string, d *Dataset) error {
	var problems []string
	files := map[string][]string{}
	for _, q := range d.Queries {
		for i, j := range q.Judgements {
			if err := resolveSpan(root, files, j); err != nil {
				problems = append(problems, fmt.Sprintf("query %s judgement %d (%s:%d-%d): %v", q.ID, i, j.Path, j.StartLine, j.EndLine, err))
			}
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("retrieval: %d judged span(s) do not resolve under %s:\n  %s", len(problems), root, strings.Join(problems, "\n  "))
}

func resolveSpan(root string, files map[string][]string, j Judgement) error {
	lines, err := fileLines(root, files, j.Path)
	if err != nil {
		return err
	}
	if j.EndLine > len(lines) {
		return fmt.Errorf("file has %d lines, span ends at %d", len(lines), j.EndLine)
	}
	text := strings.Join(lines[j.StartLine-1:j.EndLine], "\n")
	if !strings.Contains(text, j.Anchor) {
		return fmt.Errorf("anchor %q not found inside the span", j.Anchor)
	}
	return nil
}

// fileLines reads and caches a repo-relative file split into lines. A
// trailing newline does not produce an extra empty line, so the count is what
// an editor shows.
func fileLines(root string, cache map[string][]string, rel string) ([]string, error) {
	if lines, ok := cache[rel]; ok {
		return lines, nil
	}
	p := filepath.Join(root, filepath.FromSlash(rel))
	fi, err := os.Stat(p)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file", rel)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	cache[rel] = lines
	return lines, nil
}

// CheckoutHEAD returns the HEAD commit of the Git checkout at root. It runs
// the local git binary only; no network.
func CheckoutHEAD(ctx context.Context, root string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("retrieval: git rev-parse HEAD in %s: %w", root, err)
	}
	return strings.TrimSpace(string(out)), nil
}
