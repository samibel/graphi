package embed

import (
	"fmt"
	"strings"
	"sync"
)

// EnvSelector is the environment variable that opts a process into an embedder.
// Empty/unset ⇒ no embedder ⇒ graceful-skip (semantic search OFF). The value is a
// selector string such as "ollama:nomic-embed-text" or "ollama" — never a host or
// a secret. Parsing happens in Constructor.
const EnvSelector = "GRAPHI_EMBEDDER"

// RegisterDefaults registers graphi's built-in DEFAULT embedders onto r and
// returns r for chaining. Mirroring core/parse.RegisterDefaults' open/closed
// style, registration is one line per entry — but the DEFAULT build deliberately
// registers NOTHING: semantic search is OFF by default and the default binary
// ships no embedder (SW-059 / OQ6). Opt-in embedders (Ollama loopback; ONNX under
// `//go:build embed_onnx`) plug in through Constructor / the build-tag seam, NOT
// here, so the zero-value graceful-skip invariant is preserved by construction.
//
// Keeping this function present-but-empty gives the registration-level no-CGO
// guard (AssertNoCgoEmbedder) and the cgoconformance import-graph scan a single,
// stable entry point to assert against.
func RegisterDefaults(r *Registry) *Registry {
	// (intentionally empty — the default tier registers no embedder)
	return r
}

// NewDefaultRegistry returns a Registry pre-loaded with the default embedders,
// which for the default build is NONE (graceful-skip). It is the constructor
// cmd/graphi and tests use to obtain the default, semantic-search-OFF registry.
func NewDefaultRegistry() *Registry {
	return RegisterDefaults(NewRegistry())
}

// Constructor resolves a config selector (e.g. the GRAPHI_EMBEDDER value) into an
// Embedder. It is the SINGLE config-driven selection seam:
//
//   - empty selector ⇒ (nil, nil): graceful-skip, no embedder, no error, no
//     network, nothing constructed (the default path);
//   - unknown selector ⇒ (nil, nil): graceful-skip as well — an unrecognized
//     embedder must degrade gracefully, never error out or block lexical search;
//   - a recognized selector ⇒ the constructed embedder (opt-in only).
//
// Network embedders (Ollama) are constructed here ONLY for an explicit selector
// and validate loopback fail-closed at construction. Returning (nil, nil) leaves
// the caller's Registry in the zero/graceful-skip state.
//
// ctor is the per-scheme constructor table; pass DefaultConstructors() for the
// default build (which knows the loopback Ollama scheme). The CGO ONNX scheme is
// added to the table only under `//go:build embed_onnx`.
func Constructor(selector string, ctor map[string]SchemeConstructor) (Embedder, error) {
	scheme, arg := splitSelector(selector)
	if scheme == "" {
		return nil, nil // graceful-skip: nothing configured
	}
	make, ok := ctor[scheme]
	if !ok || make == nil {
		return nil, nil // graceful-skip: unknown embedder, degrade cleanly
	}
	return make(arg)
}

// SchemeConstructor builds an embedder for a selector's scheme from its argument
// (the part after the first ':'). It MAY return an error to fail closed (e.g. the
// Ollama constructor rejects a non-loopback host). A nil-error nil-Embedder is
// treated as graceful-skip by Constructor.
type SchemeConstructor func(arg string) (Embedder, error)

// DefaultConstructors returns the per-scheme constructor table currently
// registered (via RegisterScheme). In the DEFAULT (CGo-free) build this is the
// loopback-only Ollama scheme when engine/embed/ollama is imported (opt-in, never
// reached on the default path because the selector is empty by default). The CGO
// ONNX scheme self-registers via engine/embed/onnx's init ONLY under the
// `embed_onnx` build tag (that package does not compile otherwise), so the
// default table can never construct a CGO embedder.
func DefaultConstructors() map[string]SchemeConstructor {
	m := map[string]SchemeConstructor{}
	baseMu.Lock()
	for scheme, make := range baseConstructors {
		m[scheme] = make
	}
	baseMu.Unlock()
	return m
}

// baseConstructors is the build-tag-independent scheme table. Ollama lives here
// (loopback-only, stdlib net/http, no CGO). It is populated by the
// engine/embed/ollama package via RegisterScheme in its init, so the embed leaf
// never imports the ollama subpackage (no import cycle).
var (
	baseMu           sync.Mutex
	baseConstructors = map[string]SchemeConstructor{}
)

// RegisterScheme adds a constructor for a selector scheme to the build-tag-
// independent table. It is the extension seam used by opt-in embedder
// subpackages (e.g. engine/embed/ollama) to register their scheme from an init
// WITHOUT the embed leaf importing them — preserving the open/closed layering.
// Registering an empty scheme or a nil constructor is a no-op.
func RegisterScheme(scheme string, make SchemeConstructor) {
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	if scheme == "" || make == nil {
		return
	}
	baseMu.Lock()
	baseConstructors[scheme] = make
	baseMu.Unlock()
}

// splitSelector splits "scheme:arg" into its lowercase scheme and verbatim arg.
// A bare "scheme" yields ("scheme", ""). Empty input yields ("", "").
func splitSelector(selector string) (scheme, arg string) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return "", ""
	}
	if i := strings.IndexByte(selector, ':'); i >= 0 {
		return strings.ToLower(strings.TrimSpace(selector[:i])), strings.TrimSpace(selector[i+1:])
	}
	return strings.ToLower(selector), ""
}

// CgoEmbedder is the marker interface a CGO-backed embedder (e.g. the ONNX
// embedder under `//go:build embed_onnx`) implements so the registration-level
// no-CGO guard can detect it WITHOUT importing it. A pure-Go embedder does NOT
// implement this; the default build therefore contains no type that satisfies it.
type CgoEmbedder interface {
	Embedder
	// IsCgoEmbedder is a no-op marker method present only on CGO embedders.
	IsCgoEmbedder()
}

// ImpureEmbedder names a single registered embedder that is CGO-backed — one that
// must never reach the default build. It is the structured offender record the
// no-CGO guard returns so a failure can name exactly which embedder regressed.
type ImpureEmbedder struct {
	ID string
}

func (i ImpureEmbedder) String() string { return fmt.Sprintf("%s (cgo embedder)", i.ID) }

// AssertNoCgoEmbedder is the registration-level no-CGO guard for the embed
// boundary (SW-059), analogous to core/parse.AssertPureGoDefaults. It enumerates
// every embedder registered in r and returns the ones that are CGO-backed (i.e.
// implement CgoEmbedder). A nil/empty result means the registry is provably free
// of any CGO embedder; a non-empty result is a CGo-free regression that names the
// offending embedder(s).
//
// It is the registration-layer complement to internal/cgoconformance's
// import-graph scan: two-layer defense-in-depth. It is tag-independent (it
// inspects in-process registration state), so it holds identically under the
// default `go test` and under an `embed_onnx` build.
func AssertNoCgoEmbedder(r *Registry) []ImpureEmbedder {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	embs := make([]Embedder, 0, len(r.byID))
	for _, e := range r.byID {
		embs = append(embs, e)
	}
	r.mu.RUnlock()

	var offenders []ImpureEmbedder
	for _, e := range embs {
		if _, isCgo := e.(CgoEmbedder); isCgo {
			offenders = append(offenders, ImpureEmbedder{ID: e.ID()})
		}
	}
	return offenders
}

// FormatCgoEmbedderFailure renders a clear, release-blocking message naming the
// offending CGO embedders for a non-empty AssertNoCgoEmbedder result.
func FormatCgoEmbedderFailure(offenders []ImpureEmbedder) string {
	if len(offenders) == 0 {
		return ""
	}
	names := make([]string, 0, len(offenders))
	for _, o := range offenders {
		names = append(names, o.String())
	}
	return fmt.Sprintf(
		"no-CGO embed guard: %d CGO embedder(s) reachable from the default registry: %v — the default build MUST register no CGO embedder; the ONNX embedder belongs only behind //go:build embed_onnx",
		len(offenders), names,
	)
}
