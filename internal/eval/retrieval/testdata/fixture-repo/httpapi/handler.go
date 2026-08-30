// Package httpapi is the HTTP surface of the fixture. Every request is
// authenticated by auth.ValidateToken before it touches the store.
package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"example.com/fixture/auth"
	"example.com/fixture/store"
)

// Handler serves GET /items/{key} and PUT /items/{key} over a Store.
type Handler struct {
	Store  store.Store
	Secret string
	Now    func() time.Time
}

// NewHandler constructs a Handler over s with the token-signing secret.
func NewHandler(s store.Store, secret string) *Handler {
	return &Handler{Store: s, Secret: secret, Now: time.Now}
}

// ServeHTTP implements http.Handler. The request flow is: bearer token →
// auth.ValidateToken → route on method → Store.Get / Store.Put.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, err := h.authenticate(r); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/items/")
	switch r.Method {
	case http.MethodGet:
		h.get(w, key)
	case http.MethodPut:
		h.put(w, r, key)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// authenticate extracts the bearer token and validates it.
func (h *Handler) authenticate(r *http.Request) (auth.Claims, error) {
	raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if raw == "" {
		return auth.Claims{}, errors.New("missing bearer token")
	}
	return auth.ValidateToken(raw, h.Secret, h.Now())
}

// get writes the stored value or a 404.
func (h *Handler) get(w http.ResponseWriter, key string) {
	v, err := h.Store.Get(key)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(v)
}

// put stores the request body under key.
func (h *Handler) put(w http.ResponseWriter, r *http.Request, key string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.Store.Put(key, body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
