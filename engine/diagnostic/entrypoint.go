package diagnostic

import (
	"strings"

	"github.com/samibel/graphi/core/model"
)

// entryPointAnnotations is the built-in set of source annotation names that mark
// a symbol as a live, framework-invoked entry point. Such a symbol legitimately
// has no in-graph inbound reference (the framework reaches it by reflection /
// annotation scanning, which the static graph cannot see), so it must NOT be
// flagged as a dead symbol and must NOT be safe-deleted. The set is the single
// source of truth reused by the dead_symbol analyzer AND the safe-delete gate.
//
// Names are the bare annotation identifier (the token after '@'), matching what
// the extractors record in NodeMeta.Annotations.
var entryPointAnnotations = map[string]bool{
	// Spring stereotypes / DI + lifecycle.
	"Bean":           true,
	"Configuration":  true,
	"Component":      true,
	"Service":        true,
	"Repository":     true,
	"Controller":     true,
	"RestController": true,
	"PostConstruct":  true,
	"PreDestroy":     true,
	"EventListener":  true,
	"Scheduled":      true,
	// JUnit test entry points and lifecycle hooks.
	"Test":       true,
	"BeforeEach": true,
	"AfterEach":  true,
	"BeforeAll":  true,
	"AfterAll":   true,
	// Overrides are called through the base type / interface, which the graph may
	// not connect to this concrete member.
	"Override": true,
}

// EntryPointAnnotationSet returns a copy of the built-in entry-point annotation
// names (sorted-insensitive map form is not exposed) for callers that want to
// introspect or extend the policy. It is defensive: mutating the result does not
// affect the built-in set.
func EntryPointAnnotationSet() map[string]bool {
	out := make(map[string]bool, len(entryPointAnnotations))
	for k, v := range entryPointAnnotations {
		out[k] = v
	}
	return out
}

// IsEntryPoint reports whether n is a live entry point that should be excluded
// from dead-symbol flagging and protected from safe-delete. A node qualifies
// when ANY of these hold:
//
//   - it carries an entry-point annotation (Meta().Annotations ∩ the built-in set);
//   - it is a program entry: Meta().Flags contains "main", or its qualified name
//     is a Go `main.main` / `*.main` free function;
//   - it lives on a test path: Meta().Flags contains "test_path", or its source
//     path matches a test convention (`*_test.go`, `src/test/`, `test_*.py`);
//   - it OVERRIDES a supertype member: Meta().Flags contains "override" (Kotlin /
//     C# / TS `override` keyword). Like Java's `@Override` annotation, an
//     overriding member is invoked polymorphically through its base type /
//     interface, an edge the static call graph often resolves to the DECLARED
//     type rather than this concrete member — so it legitimately has no direct
//     inbound reference and must not be dead-flagged;
//   - it is DECORATED: Meta().Flags contains "decorated" (a TypeScript class /
//     method decorator, e.g. Angular `@Component` / `@Injectable`, NestJS
//     `@Controller` / `@Get`). A decorated symbol is instantiated or invoked by
//     the framework via the decorator's registration, which the static graph
//     cannot see, so it legitimately has no in-graph inbound reference.
//
// This is intentionally permissive toward NOT flagging/deleting: a false
// "entry point" only suppresses a dead-symbol warning or blocks a delete, both
// of which fail safe.
func IsEntryPoint(n model.Node) bool {
	meta := n.Meta()
	for _, a := range meta.Annotations {
		if entryPointAnnotations[a] {
			return true
		}
	}
	for _, f := range meta.Flags {
		if f == "main" || f == "test_path" || f == "override" || f == "decorated" {
			return true
		}
	}
	if isMainSignatureQN(n.Kind(), n.QualifiedName()) {
		return true
	}
	if isEntryPointTestPath(n.SourcePath()) {
		return true
	}
	return false
}

// isMainSignatureQN reports whether the qualified name is a Go-style program
// entry point (`main.main`, or any `*.main` free function/method) so Go's main
// is excluded even without annotation metadata.
func isMainSignatureQN(kind, qn string) bool {
	if kind != "function" && kind != "method" {
		return false
	}
	return qn == "main.main" || strings.HasSuffix(qn, ".main")
}

// isEntryPointTestPath reports whether p matches a language-agnostic test-path
// convention, so Go/Python test symbols are excluded even when the extractor
// attached no test_path flag.
func isEntryPointTestPath(p string) bool {
	if p == "" {
		return false
	}
	if strings.HasSuffix(p, "_test.go") {
		return true
	}
	if strings.Contains(p, "src/test/") {
		return true
	}
	base := p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		base = p[i+1:]
	}
	if strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py") {
		return true
	}
	return false
}
