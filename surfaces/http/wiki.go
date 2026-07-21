package http

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/samibel/graphi/engine/wiki"
	"github.com/samibel/graphi/surfaces/http/webui"
)

// --- Wiki (SW-041) ---------------------------------------------------------

// wikiDoc lazily generates and caches the self-generated wiki from the attached
// store. On a nil store it returns a zero-value Wiki with wikiErr set so the
// HTTP handlers can 404 consistently.
func (s *Server) wikiDoc() (wiki.Wiki, error) {
	s.wikiOnce.Do(func() {
		if s.store == nil {
			s.wikiErr = errors.New("wiki disabled (no store attached)")
			return
		}
		s.wikiGenerated, s.wikiErr = wiki.Generate(context.Background(), s.store)
	})
	return s.wikiGenerated, s.wikiErr
}

// serveWikiSPA reports whether the request is a browser DOCUMENT navigation
// (Accept: text/html) while the embedded UI is available; if so it serves the
// SPA shell and returns true. This mirrors the vite dev-server /wiki bypass:
// a deep link / page reload of /wiki* must land in the app (which then fetches
// the data with Accept: text/markdown), not show raw markdown bytes.
func serveWikiSPA(w http.ResponseWriter, r *http.Request) bool {
	if !webui.Enabled() || !strings.Contains(r.Header.Get("Accept"), "text/html") {
		return false
	}
	http.ServeFileFS(w, r, webui.FS, "index.html")
	return true
}

// handleWikiIndex serves the wiki index page as Markdown (text/markdown).
func (s *Server) handleWikiIndex(w http.ResponseWriter, r *http.Request) {
	if serveWikiSPA(w, r) {
		return
	}
	doc, err := s.wikiDoc()
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "wiki unavailable")
		return
	}
	writeMarkdown(w, doc.Index.Body)
}

// handleWikiPage serves one community page as Markdown. Unknown id → 404.
func (s *Server) handleWikiPage(w http.ResponseWriter, r *http.Request) {
	if serveWikiSPA(w, r) {
		return
	}
	doc, err := s.wikiDoc()
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "wiki unavailable")
		return
	}
	p, ok := doc.PageByID(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "unknown community")
		return
	}
	writeMarkdown(w, p.Body)
}
