package parse

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
)

// Registry is a concurrency-safe mapping from file extension / language to a
// Parser. It is the single selection point for the parse boundary.
//
// Open/closed: callers extend coverage purely by calling Register with a new
// Parser; no existing parser code is edited. Lookups (ParserFor / ParserForLang)
// and Register are all safe for concurrent use by multiple goroutines.
type Registry struct {
	mu     sync.RWMutex
	byExt  map[string]Parser // lowercase ext (".go") -> parser
	byLang map[string]Parser // lowercase language ("go") -> parser
}

// NewRegistry returns an empty, ready-to-use Registry.
func NewRegistry() *Registry {
	return &Registry{
		byExt:  make(map[string]Parser),
		byLang: make(map[string]Parser),
	}
}

// Register adds p to the registry, indexing it by its declared language and by
// each of its declared extensions (case-insensitively). A later registration for
// the same extension/language overrides the earlier one, allowing opt-in backends
// (e.g. a CGO grammar) to supersede a stdlib default. Registering nil, or a
// parser with no language and no extensions, is a no-op.
//
// Register never panics and leaves the registry consistent: it either applies the
// full registration or (for a nil/empty parser) makes no change.
func (r *Registry) Register(p Parser) {
	if p == nil {
		return
	}
	lang := strings.ToLower(strings.TrimSpace(p.Language()))
	exts := p.Extensions()
	if lang == "" && len(exts) == 0 {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if lang != "" {
		r.byLang[lang] = p
	}
	for _, e := range exts {
		if ext := normalizeExt(e); ext != "" {
			r.byExt[ext] = p
		}
	}
}

// ParserFor selects the parser for the given filename by its extension
// (case-insensitive). It returns ErrNoParser when no parser is registered for the
// file's type. The miss path mutates no shared state and is idempotent.
func (r *Registry) ParserFor(filename string) (Parser, error) {
	ext := normalizeExt(filepath.Ext(filename))
	if ext == "" {
		return nil, ErrNoParser
	}
	r.mu.RLock()
	p, ok := r.byExt[ext]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrNoParser
	}
	return p, nil
}

// ParserForLang selects the parser for a canonical language identifier
// (case-insensitive), returning ErrNoParser on a miss.
func (r *Registry) ParserForLang(lang string) (Parser, error) {
	key := strings.ToLower(strings.TrimSpace(lang))
	if key == "" {
		return nil, ErrNoParser
	}
	r.mu.RLock()
	p, ok := r.byLang[key]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrNoParser
	}
	return p, nil
}

// Parse is a convenience that selects a parser by filename and parses src. On a
// selection miss it returns ErrNoParser with a nil result and performs no parse.
func (r *Registry) Parse(ctx context.Context, filename string, src []byte) (*ParseResult, error) {
	p, err := r.ParserFor(filename)
	if err != nil {
		return nil, err
	}
	return p.Parse(ctx, filename, src)
}

// Languages returns the sorted set of registered canonical languages. Useful for
// diagnostics and tests; the returned slice is a copy.
func (r *Registry) Languages() []string {
	r.mu.RLock()
	out := make([]string, 0, len(r.byLang))
	for l := range r.byLang {
		out = append(out, l)
	}
	r.mu.RUnlock()
	sortStrings(out)
	return out
}

// normalizeExt lowercases an extension and guarantees a single leading dot.
// "" / "." normalize to "".
func normalizeExt(e string) string {
	e = strings.ToLower(strings.TrimSpace(e))
	if e == "" || e == "." {
		return ""
	}
	if !strings.HasPrefix(e, ".") {
		e = "." + e
	}
	return e
}

// sortStrings is a tiny insertion sort to avoid importing "sort" for one call;
// keeps the leaf dependency surface minimal. n is always small (language count).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
