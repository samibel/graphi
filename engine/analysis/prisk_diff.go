package analysis

import (
	"fmt"
	"strconv"
	"strings"
)

// parseDiff parses a unified-diff string into the changed references the scorer
// resolves to graph node ids. It is a PURE, bounded, string-only parser:
//
//   - no shell-out, no git-exec, no filesystem access on the hot path (the diff
//     is supplied as a string; a local-path caller reads it once, outside this);
//   - size-bounded (MaxDiffBytes) so a hostile/huge diff cannot OOM the scorer;
//   - path-sanitized (model.NormalizePath, applied at resolution time) so no
//     path-traversal can leak a host-absolute path into identity.
//
// Recognized inputs (local-first contract; NO remote fetch / base..head):
//
//  1. A unified diff: `+++ b/<path>` file headers and `@@ -a,b +c,d @@` hunk
//     headers locate changed files and line ranges. An optional trailing symbol
//     hint on the hunk header (the function context git emits after `@@`) is
//     captured as the qualified-name hint.
//  2. A simple line-oriented form for callers that pre-resolve: lines of the
//     form `path:name` or `path#Lline` or a bare 16-char node id, one per line.
//     This keeps the contract usable from a CLI flag without a full diff.
//
// An empty or whitespace-only input yields zero refs (an empty diff completes
// with an empty report, never an error).
func parseDiff(diff string) ([]changedRef, error) {
	if len(diff) > MaxDiffBytes {
		return nil, fmt.Errorf("analysis: diff exceeds %d-byte bound (untrusted input)", MaxDiffBytes)
	}
	if strings.TrimSpace(diff) == "" {
		return nil, nil
	}

	// Heuristic: a real unified diff contains a `+++ ` or `@@ ` marker. Otherwise
	// treat the input as the simple line-oriented form.
	if strings.Contains(diff, "\n+++ ") || strings.HasPrefix(diff, "+++ ") ||
		strings.Contains(diff, "@@") {
		return parseUnifiedDiff(diff), nil
	}
	return parseSimpleRefs(diff), nil
}

// parseUnifiedDiff extracts changed file + line/symbol hints from a unified diff.
func parseUnifiedDiff(diff string) []changedRef {
	var refs []changedRef
	seen := map[string]struct{}{}
	add := func(ref changedRef) {
		key := ref.file + "|" + ref.name + "|" + strconv.Itoa(ref.line)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		refs = append(refs, ref)
	}

	var curFile string
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ "):
			curFile = cleanDiffPath(strings.TrimPrefix(line, "+++ "))
		case strings.HasPrefix(line, "@@"):
			if curFile == "" {
				continue
			}
			newLine, sym := parseHunkHeader(line)
			add(changedRef{
				raw:  diffRaw(curFile, sym, newLine),
				file: curFile,
				name: sym,
				line: newLine,
			})
		}
	}
	// If the diff named files but no hunks resolved a symbol/line, still emit a
	// file-level ref per changed file so the region is scored (or degraded).
	if len(refs) == 0 && curFile != "" {
		refs = append(refs, changedRef{raw: curFile, file: curFile})
	}
	return refs
}

// parseHunkHeader extracts the new-file starting line and an optional trailing
// function-context symbol from a `@@ -a,b +c,d @@ context` hunk header.
func parseHunkHeader(line string) (int, string) {
	// `@@ -a,b +c,d @@ optional context`
	rest := strings.TrimPrefix(line, "@@")
	end := strings.Index(rest, "@@")
	var context string
	header := rest
	if end >= 0 {
		header = rest[:end]
		context = strings.TrimSpace(rest[end+2:])
	}
	newLine := 0
	for _, tok := range strings.Fields(header) {
		if strings.HasPrefix(tok, "+") {
			numPart := strings.TrimPrefix(tok, "+")
			if comma := strings.Index(numPart, ","); comma >= 0 {
				numPart = numPart[:comma]
			}
			if n, err := strconv.Atoi(numPart); err == nil {
				newLine = n
			}
		}
	}
	return newLine, symbolFromContext(context)
}

// symbolFromContext extracts a bare identifier from a git hunk function-context
// string (e.g. "func (a Foo) Bar(" -> "Bar"). Best-effort, pure string.
func symbolFromContext(context string) string {
	if context == "" {
		return ""
	}
	// Take the token before the first '(' if present, else the last word.
	if paren := strings.Index(context, "("); paren >= 0 {
		head := strings.TrimSpace(context[:paren])
		fields := strings.Fields(head)
		if len(fields) > 0 {
			return lastIdent(fields[len(fields)-1])
		}
	}
	fields := strings.Fields(context)
	if len(fields) > 0 {
		return lastIdent(fields[len(fields)-1])
	}
	return ""
}

// lastIdent returns the trailing identifier component of a possibly-qualified
// token (e.g. "Foo.Bar" -> "Bar"). Pure string.
func lastIdent(s string) string {
	if dot := strings.LastIndex(s, "."); dot >= 0 {
		return s[dot+1:]
	}
	return s
}

// parseSimpleRefs parses the line-oriented form: one ref per line, where each is
// `path:name`, `path#Lline`, or a bare 16-char node id.
func parseSimpleRefs(input string) []changedRef {
	var refs []changedRef
	seen := map[string]struct{}{}
	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") && !strings.Contains(line, "#L") {
			continue
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}

		if looksLikeNodeID(line) {
			refs = append(refs, changedRef{raw: line})
			continue
		}
		ref := changedRef{raw: line}
		// `path#Lline`
		if hash := strings.Index(line, "#L"); hash >= 0 {
			ref.file = cleanDiffPath(line[:hash])
			if n, err := strconv.Atoi(line[hash+2:]); err == nil {
				ref.line = n
			}
			refs = append(refs, ref)
			continue
		}
		// `path:name`
		if colon := strings.LastIndex(line, ":"); colon >= 0 {
			ref.file = cleanDiffPath(line[:colon])
			ref.name = strings.TrimSpace(line[colon+1:])
			refs = append(refs, ref)
			continue
		}
		// bare path
		ref.file = cleanDiffPath(line)
		refs = append(refs, ref)
	}
	return refs
}

// cleanDiffPath strips a git `a/`/`b/` prefix and trailing tab-metadata from a
// diff path token. Pure string; the repo-relative normalization (and traversal
// defense) is applied later via model.NormalizePath at resolution time.
func cleanDiffPath(p string) string {
	p = strings.TrimSpace(p)
	// Drop trailing tab + timestamp some diff tools append.
	if tab := strings.IndexByte(p, '\t'); tab >= 0 {
		p = p[:tab]
	}
	p = strings.TrimSpace(p)
	if p == "/dev/null" {
		return ""
	}
	for _, prefix := range []string{"a/", "b/", "./"} {
		if strings.HasPrefix(p, prefix) {
			p = p[len(prefix):]
			break
		}
	}
	return p
}

// diffRaw builds a stable verbatim reference string for degraded reporting.
func diffRaw(file, name string, line int) string {
	switch {
	case name != "":
		return file + ":" + name
	case line > 0:
		return file + "#L" + strconv.Itoa(line)
	default:
		return file
	}
}
