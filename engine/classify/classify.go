// Package classify centralizes the pure-string source-path classification
// heuristics (test code, generated/vendored code, configuration files,
// language-by-extension) that were previously duplicated across engine
// packages (analysis/triage, edit/safe_delete, diagnostic/suppress).
//
// Two pattern vocabularies coexist here on purpose:
//
//   - TestPathPatterns are lowercase SUBSTRING patterns (triage semantics),
//     matched by IsTestPath. Directory-style entries (leading "/") also match
//     at the start of a repo-relative path ("test/x.go").
//   - GeneratedPathPatterns are GLOB patterns (suppress semantics), matched by
//     MatchAnyPattern / IsGeneratedPath.
//
// Deliberately NOT unified onto this package: the private isTestPath in
// engine/agenttools/risk (its output feeds the frozen-stable change_risk
// summary; widening it would silently change stable behavior) and the
// fail-safe test check inside engine/diagnostic.IsEntryPoint (deliberately
// narrower). See the comments at those sites.
package classify

import (
	"path"
	"strings"

	"github.com/samibel/graphi/core/model"
)

// TestPathPatterns is the documented, deterministic, language-agnostic union
// of the test-path heuristics previously spread across the engine: Go
// _test.go, JS/TS .test./.spec., Python test_/_spec, the common test/spec
// directories, and Go testdata trees. Substring semantics, lowercase.
var TestPathPatterns = []string{
	"_test.",
	".test.",
	".spec.",
	"_spec.",
	"test_",
	"/tests/",
	"/test/",
	"/spec/",
	"/__tests__/",
	"/testdata/",
}

// GeneratedPathPatterns match files considered generated or vendored. Glob
// semantics (see MatchAnyPattern).
var GeneratedPathPatterns = []string{
	"*.gen.go", "*_pb.go", "*.generated.*", "generated", "vendor/", "node_modules/",
}

// configBasenames are exact (lowercased) file names that are configuration
// regardless of extension.
var configBasenames = map[string]bool{
	"go.mod":     true,
	"go.sum":     true,
	"dockerfile": true,
	"makefile":   true,
	".env":       true,
}

// configExtensions are (lowercased) extensions of common configuration and
// manifest formats.
var configExtensions = map[string]bool{
	".yaml":       true,
	".yml":        true,
	".json":       true,
	".toml":       true,
	".ini":        true,
	".cfg":        true,
	".conf":       true,
	".properties": true,
	".lock":       true,
}

// IsTestPath reports whether p looks like test code by the documented
// substring heuristic over TestPathPatterns. Matching is case-insensitive and
// separator-normalized; directory-style patterns also match a leading
// repo-relative segment ("test/x.go").
func IsTestPath(p string) bool {
	if p == "" {
		return false
	}
	lower := strings.ToLower(model.NormalizePath(p))
	slashed := "/" + lower
	for _, pat := range TestPathPatterns {
		if strings.HasPrefix(pat, "/") {
			if strings.Contains(slashed, pat) {
				return true
			}
			continue
		}
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// IsGeneratedPath reports whether p looks generated or vendored per
// GeneratedPathPatterns.
func IsGeneratedPath(p string) bool {
	return MatchAnyPattern(model.NormalizePath(p), GeneratedPathPatterns)
}

// IsConfigPath reports whether p looks like a configuration or manifest file:
// a known config basename, a known config extension, or a CI workflow file.
func IsConfigPath(p string) bool {
	if p == "" {
		return false
	}
	lower := strings.ToLower(model.NormalizePath(p))
	if strings.Contains(lower, ".github/workflows/") {
		return true
	}
	base := path.Base(lower)
	if configBasenames[base] {
		return true
	}
	return configExtensions[path.Ext(base)]
}

// languageByExtension maps (lowercased) file extensions to display language
// names. It intentionally tracks the parser catalog's vocabulary.
var languageByExtension = map[string]string{
	".go":   "Go",
	".ts":   "TypeScript",
	".tsx":  "TypeScript",
	".js":   "JavaScript",
	".jsx":  "JavaScript",
	".mjs":  "JavaScript",
	".py":   "Python",
	".java": "Java",
	".kt":   "Kotlin",
	".kts":  "Kotlin",
	".cs":   "C#",
	".c":    "C",
	".h":    "C",
	".cpp":  "C++",
	".cc":   "C++",
	".cxx":  "C++",
	".hpp":  "C++",
	".rs":   "Rust",
	".rb":   "Ruby",
	".php":  "PHP",
	".lua":  "Lua",
	".sh":   "Bash",
	".bash": "Bash",
	".sql":  "SQL",
	".css":  "CSS",
	".md":   "Markdown",
	".yaml": "YAML",
	".yml":  "YAML",
	".toml": "TOML",
	".json": "JSON",
	".html": "HTML",
	".tf":   "HCL",
	".hcl":  "HCL",
}

// Language returns the display language name for a source path by extension,
// or "" when unknown.
func Language(p string) string {
	if p == "" {
		return ""
	}
	return languageByExtension[strings.ToLower(path.Ext(model.NormalizePath(p)))]
}

// MatchAnyPattern reports whether p matches any of the glob patterns. Patterns
// without a slash match the full path, the base name, or any path segment;
// patterns containing a slash also match as a path substring. This is the
// single shared implementation of the suppress-style matcher.
func MatchAnyPattern(p string, patterns []string) bool {
	for _, pat := range patterns {
		if pat == "" {
			continue
		}
		matched, _ := path.Match(pat, p)
		if matched {
			return true
		}
		matched, _ = path.Match(pat, path.Base(p))
		if matched {
			return true
		}
		// Match against any path segment.
		for _, seg := range strings.Split(p, "/") {
			matched, _ = path.Match(pat, seg)
			if matched {
				return true
			}
		}
		// Path-substring match for directory-style patterns.
		if strings.Contains(pat, "/") && strings.Contains(p, pat) {
			return true
		}
	}
	return false
}
