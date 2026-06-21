// Package bench implements the budget-gated benchmark suite for graphi (story
// SW-010). It measures four metrics at the owning-layer boundaries, compares
// each against a pinned, version-stamped budget in bench-budget.yml, and emits a
// machine-readable report. The suite is hermetic: it uses only loopback/local
// resources (temp files + the pure-Go modernc SQLite backend), runs under
// CGO_ENABLED=0, and performs no network I/O (reusing the SW-008 posture).
//
// This file holds a CONSTRAINED YAML reader for the bench-budget.yml schema. It
// is intentionally NOT a general YAML parser: it supports block mappings with
// 2-space indentation, scalar leaves (ints, quoted strings, bare strings), blank
// lines, and '#' comments. It exists so the suite reads its manifest without
// adding a dependency, keeping the default module hermetic for SW-013 packaging.
// Any unsupported construct returns an error so a malformed manifest fails
// loudly rather than silently.
package bench

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// tinyNode is a parsed YAML node: either a scalar (string/int/bool) or a nested
// mapping (map[string]any). Lists/anchors/etc. are unsupported by design.
type tinyNode = any

// parseTinyYAML parses the constrained YAML subset described above into a nested
// map[string]any. Indentation defines nesting; the parser tracks a stack of
// (indent, node) frames.
func parseTinyYAML(data []byte) (map[string]any, error) {
	root := map[string]any{}
	type frame struct {
		indent int
		node   map[string]any
	}
	stack := []frame{{0, root}}

	lines := bytes.Split(data, []byte("\n"))
	for i, raw := range lines {
		line := stripComment(raw)
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		indent := countIndent(line)
		content := strings.TrimSpace(string(line))
		if indent%2 != 0 {
			return nil, fmt.Errorf("bench-budget: line %d: indentation must be a multiple of 2 spaces", i+1)
		}
		// Pop frames whose indent is >= this line's indent to find the parent.
		for len(stack) > 1 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		parent := stack[len(stack)-1].node

		key, val, hasVal, err := splitKV(content)
		if err != nil {
			return nil, fmt.Errorf("bench-budget: line %d: %w", i+1, err)
		}
		if hasVal {
			v, err := parseScalar(val)
			if err != nil {
				return nil, fmt.Errorf("bench-budget: line %d: %w", i+1, err)
			}
			parent[key] = v
		} else {
			child := map[string]any{}
			parent[key] = child
			stack = append(stack, frame{indent, child})
		}
	}
	return root, nil
}

// stripComment removes a '#' comment from raw, ignoring '#' inside single or
// double quotes. A '#' only starts a comment at line start or when preceded by
// whitespace.
func stripComment(raw []byte) []byte {
	inSingle, inDouble := false, false
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == '#' && !inSingle && !inDouble:
			if i == 0 || raw[i-1] == ' ' || raw[i-1] == '\t' {
				break // rest is comment
			}
		}
		if c == '#' && !inSingle && !inDouble && (i == 0 || raw[i-1] == ' ' || raw[i-1] == '\t') {
			break
		}
		out = append(out, c)
	}
	return out
}

func countIndent(line []byte) int {
	n := 0
	for _, c := range line {
		if c == ' ' {
			n++
		} else {
			break
		}
	}
	return n
}

// splitKV splits "key: value" or "key:" into key and (optional) value. A ':'
// inside a value is tolerated because we split on the FIRST ':' followed by a
// space or end-of-line.
func splitKV(content string) (key, val string, hasVal bool, err error) {
	idx := -1
	inSingle, inDouble := false, false
	for i := 0; i < len(content); i++ {
		c := content[i]
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == ':' && !inSingle && !inDouble && (i+1 == len(content) || content[i+1] == ' '):
			idx = i
		}
		if idx != -1 {
			break
		}
	}
	if idx == -1 {
		return "", "", false, fmt.Errorf("expected 'key:' or 'key: value', got %q", content)
	}
	key = strings.TrimSpace(content[:idx])
	if key == "" {
		return "", "", false, fmt.Errorf("empty key in %q", content)
	}
	rest := strings.TrimSpace(content[idx+1:])
	if rest == "" {
		return key, "", false, nil
	}
	return key, rest, true, nil
}

// parseScalar interprets a value as int, bool, quoted string, or bare string.
func parseScalar(val string) (any, error) {
	if len(val) >= 2 && (val[0] == '"' && val[len(val)-1] == '"' || val[0] == '\'' && val[len(val)-1] == '\'') {
		return val[1 : len(val)-1], nil
	}
	if i, err := strconv.ParseInt(val, 10, 64); err == nil {
		return i, nil
	}
	switch val {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null", "~":
		return nil, nil
	}
	return val, nil
}
