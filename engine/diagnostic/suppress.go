package diagnostic

import (
	"path"
	"strings"

	"github.com/samibel/graphi/core/model"
)

// SuppressionConfig carries caller-supplied pattern sets for the configurable
// classifiers. The zero value uses conservative built-in defaults.
type SuppressionConfig struct {
	// GeneratedPathPatterns match files considered generated or vendored.
	GeneratedPathPatterns []string
	// TestPathPatterns match test files and testdata.
	TestPathPatterns []string
	// ConfiguredPathPatterns match user-supplied paths.
	ConfiguredPathPatterns []string
	// FrameworkSignatures match well-known framework entry points by name or path.
	FrameworkSignatures []string
	// GeneratedMarkerDetector, when set, reports whether the file at the given
	// repo-relative path carries an in-content generated-code marker ("Code
	// generated … DO NOT EDIT", "@generated"). The engine performs no I/O of
	// its own, so the detector is injected by the surface layer (see
	// surfaces/client.GeneratedMarkerDetector). Nil disables content sniffing.
	GeneratedMarkerDetector func(file string) bool
}

// DefaultSuppressionConfig returns the built-in conservative pattern sets.
func DefaultSuppressionConfig() SuppressionConfig {
	return SuppressionConfig{
		GeneratedPathPatterns: []string{
			"*.gen.go", "*_pb.go", "*.generated.*", "generated", "vendor/", "node_modules/",
		},
		TestPathPatterns: []string{
			"*_test.go", "*.test.*", "/testdata/",
		},
		ConfiguredPathPatterns: []string{},
		FrameworkSignatures: []string{
			"http.Handler", "http.HandleFunc", "ServeHTTP", "main.main", "HandlerFunc",
			// Lifecycle hooks and plugin-registration surfaces the graph cannot
			// prove calls into (reflection/annotation-driven).
			"TestMain", "OnStart", "OnStop", "OnStartup", "OnShutdown",
			"RegisterPlugin", "PluginRegister", "Lifecycle",
		},
	}
}

// suppressResult carries the survivors and the suppressed findings produced by
// the suppression stage.
type suppressResult struct {
	Survivors  []Diagnostic
	Suppressed []Diagnostic
}

// suppressionStage runs all classifiers over the input diagnostics and returns
// survivors plus suppressed findings. It mutates the tally so counts feed the
// final Summary.
func suppressionStage(cfg SuppressionConfig, isExternalImport func(Diagnostic) bool) func([]Diagnostic, *tally) []Diagnostic {
	return func(diags []Diagnostic, t *tally) []Diagnostic {
		out := make([]Diagnostic, 0, len(diags))
		var pendingExternal []Diagnostic

		for _, d := range diags {
			cat := classify(d, cfg)
			if cat != "" {
				d.Suppression = cat
				t.SuppressedByCategory[string(cat)]++
				out = append(out, d) // suppressed findings stay in --all output
				continue
			}
			if isExternalImport(d) {
				pendingExternal = append(pendingExternal, d)
				continue
			}
			out = append(out, d)
		}

		// Aggregate repeated unresolved external imports.
		aggregated := aggregateExternalImports(pendingExternal, t)
		out = append(out, aggregated...)

		return out
	}
}

// classify returns the suppression category for a diagnostic, or empty if it
// should remain shown by default. Framework-entrypoint and public-API
// suppression apply only to dead-symbol findings (per the signal-quality PRD);
// test/generated/configured-path suppression applies to every code.
func classify(d Diagnostic, cfg SuppressionConfig) SuppressionCategory {
	file := d.File
	name := nodeNameFromMessage(d.Message)

	if matchAnyPattern(file, cfg.TestPathPatterns) || strings.HasSuffix(file, "_test.go") || strings.Contains(file, "/testdata/") {
		return SuppressionTestCode
	}
	if matchAnyPattern(file, cfg.GeneratedPathPatterns) {
		return SuppressionGenerated
	}
	if cfg.GeneratedMarkerDetector != nil && cfg.GeneratedMarkerDetector(file) {
		return SuppressionGenerated
	}
	if matchAnyPattern(file, cfg.ConfiguredPathPatterns) {
		return SuppressionConfiguredPath
	}
	if d.Code == "dead_symbol" && matchAnySignature(name, d.File, cfg.FrameworkSignatures) {
		return SuppressionFrameworkEntrypoint
	}
	if d.Code == "dead_symbol" && looksExported(name) {
		return SuppressionPublicAPINoEvidence
	}
	return ""
}

// matchAnyPattern reports whether p matches any of the glob patterns. Patterns
// without a slash match the base name or any path segment; patterns with a
// slash match as a path substring.
func matchAnyPattern(p string, patterns []string) bool {
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

// matchAnySignature reports whether the name or path matches a framework signature.
func matchAnySignature(name, file string, signatures []string) bool {
	for _, sig := range signatures {
		if sig == "" {
			continue
		}
		if strings.Contains(name, sig) || strings.Contains(file, sig) {
			return true
		}
	}
	return false
}

// nodeNameFromMessage extracts the quoted qualified name from a diagnostic message.
// It returns the empty string if no quoted name is found.
func nodeNameFromMessage(msg string) string {
	start := strings.Index(msg, "\"")
	if start == -1 {
		return ""
	}
	end := strings.Index(msg[start+1:], "\"")
	if end == -1 {
		return ""
	}
	return msg[start+1 : start+1+end]
}

// looksExported reports whether the final segment of a qualified name starts with
// an uppercase letter (Go visibility heuristic) and the name has a package prefix.
// It is conservative and flagged as the highest-uncertainty classifier.
func looksExported(name string) bool {
	if name == "" {
		return false
	}
	parts := strings.Split(name, ".")
	if len(parts) < 2 {
		return false
	}
	last := parts[len(parts)-1]
	if last == "" {
		return false
	}
	return last[0] >= 'A' && last[0] <= 'Z'
}

// isExternalImport is the default predicate for unresolved external imports. A
// heuristic unresolved_reference is treated as external for aggregation purposes.
// Callers may override this via the SuppressionConfig in future iterations.
func isExternalImport(d Diagnostic) bool {
	return d.Code == "unresolved_reference" && d.Confidence == ConfidenceHeuristic
}

// aggregateExternalImports groups unresolved external imports by target symbol,
// emits one representative per group with OccurrenceCount = the SUM of the
// members' occurrence counts, and tags the remaining members as suppressed
// aggregated. The tally is updated.
//
// Summing (rather than counting group size) makes this idempotent for input the
// analyzer already aggregated by target: since WP-12 emits one diagnostic per
// target with OccurrenceCount=N, each group here has size 1 and the sum
// preserves N. It still collapses correctly if multiple pre-aggregated
// diagnostics ever share a target.
func aggregateExternalImports(diags []Diagnostic, t *tally) []Diagnostic {
	if len(diags) == 0 {
		return nil
	}
	groups := map[model.NodeId][]Diagnostic{}
	for _, d := range diags {
		key := d.TargetSymbol
		groups[key] = append(groups[key], d)
	}

	out := make([]Diagnostic, 0, len(groups))
	for _, group := range groups {
		// Deterministic representative: canonical-comparator-smallest member.
		sortDiagnostics(group)
		rep := group[0]
		rep.OccurrenceCount = occurrenceOf(rep)
		for _, other := range group[1:] {
			rep.OccurrenceCount += occurrenceOf(other)
			other.Suppression = SuppressionAggregatedExternalImport
			t.SuppressedByCategory[string(SuppressionAggregatedExternalImport)]++
			out = append(out, other)
		}
		out = append(out, rep)
	}
	return out
}

// occurrenceOf returns a diagnostic's occurrence count, treating an unset (<1)
// count as a single occurrence.
func occurrenceOf(d Diagnostic) int {
	if d.OccurrenceCount < 1 {
		return 1
	}
	return d.OccurrenceCount
}
