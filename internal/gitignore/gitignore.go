// Package gitignore is a small, pure matcher for the documented .gitignore
// pattern subset graphi supports: comments and blank lines, `!` negation with
// last-match-wins, anchoring (a pattern containing `/` is root-relative,
// otherwise it matches at any depth), trailing `/` for directory-only
// patterns, and the `*`, `?`, `[...]`, `**` wildcards. Only the repository
// ROOT .gitignore is consulted (nested .gitignore files are out of scope) and
// git's "cannot re-include below an excluded directory" rule holds. No
// filesystem access, no exec, no git dependency — Compile takes lines, Match
// takes repo-relative POSIX paths.
package gitignore

import (
	"regexp"
	"strings"
)

// pattern is one compiled .gitignore line.
type pattern struct {
	re      *regexp.Regexp
	negate  bool
	dirOnly bool
}

// Matcher evaluates a compiled root .gitignore. The zero/nil Matcher ignores
// nothing.
type Matcher struct {
	patterns []pattern
}

// Compile parses .gitignore lines into a Matcher. Malformed glob lines are
// skipped (fail-open per line — an unreadable pattern must not take down the
// walk); a nil Matcher is returned when nothing usable remains.
func Compile(lines []string) *Matcher {
	var pats []pattern
	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		neg := strings.HasPrefix(line, "!")
		if neg {
			line = line[1:]
		}
		dirOnly := strings.HasSuffix(line, "/")
		line = strings.TrimSuffix(line, "/")
		if line == "" {
			continue
		}
		anchored := strings.Contains(line, "/")
		line = strings.TrimPrefix(line, "/")
		re, err := translate(line, anchored)
		if err != nil {
			continue
		}
		pats = append(pats, pattern{re: re, negate: neg, dirOnly: dirOnly})
	}
	if len(pats) == 0 {
		return nil
	}
	return &Matcher{patterns: pats}
}

// Match reports whether the repo-relative POSIX path is ignored. It applies
// git's ancestor rule: a path below an ignored directory is ignored even when
// no pattern names the path itself, and negation cannot re-include below an
// excluded directory. Nil-safe.
func (m *Matcher) Match(relPath string, isDir bool) bool {
	if m == nil || relPath == "" || relPath == "." {
		return false
	}
	// Any excluded ancestor directory excludes the whole subtree.
	segs := strings.Split(relPath, "/")
	for i := 1; i < len(segs); i++ {
		if m.matchExact(strings.Join(segs[:i], "/"), true) {
			return true
		}
	}
	return m.matchExact(relPath, isDir)
}

// matchExact evaluates the pattern list against exactly this path,
// last-match-wins.
func (m *Matcher) matchExact(relPath string, isDir bool) bool {
	ignored := false
	for _, p := range m.patterns {
		if p.dirOnly && !isDir {
			continue
		}
		if p.re.MatchString(relPath) {
			ignored = !p.negate
		}
	}
	return ignored
}

// translate converts one gitignore glob into an anchored Go regexp over the
// whole repo-relative path. Unanchored patterns may match at any depth.
func translate(glob string, anchored bool) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	if !anchored {
		b.WriteString(`(?:.*/)?`)
	}
	i := 0
	for i < len(glob) {
		c := glob[i]
		switch c {
		case '*':
			if strings.HasPrefix(glob[i:], "**/") {
				b.WriteString(`(?:.*/)?`)
				i += 3
				continue
			}
			if glob[i:] == "**" {
				b.WriteString(`.*`)
				i += 2
				continue
			}
			b.WriteString(`[^/]*`)
			i++
		case '?':
			b.WriteString(`[^/]`)
			i++
		case '[':
			end := strings.IndexByte(glob[i+1:], ']')
			if end < 0 {
				b.WriteString(regexp.QuoteMeta(string(c)))
				i++
				continue
			}
			b.WriteString(glob[i : i+end+2])
			i += end + 2
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
			i++
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}
