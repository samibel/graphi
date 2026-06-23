package review

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeGitHub is a minimal in-process stand-in for the GitHub issue-comments API.
// It lets the GitHubHost tests exercise the REAL net/http code path against a
// local httptest server (no outbound network) while asserting the request shape,
// auth header, and idempotent create-then-update behavior.
type fakeGitHub struct {
	t        *testing.T
	comments []ghComment
	nextID   int64
	// sawAuth records the Authorization header of the last request so the test can
	// assert the token is sent as a Bearer header (and is never put in the URL).
	sawAuth string
}

func (f *fakeGitHub) handler() http.Handler {
	mux := http.NewServeMux()
	// List + create: /repos/o/r/issues/{n}/comments
	mux.HandleFunc("/repos/o/r/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
		f.sawAuth = r.Header.Get("Authorization")
		if got := r.Header.Get("X-GitHub-Api-Version"); got != "2022-11-28" {
			f.t.Errorf("missing/incorrect API version header: %q", got)
		}
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(f.comments)
		case http.MethodPost:
			var in struct {
				Body string `json:"body"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			f.nextID++
			c := ghComment{ID: f.nextID, Body: in.Body, HTMLURL: fmt.Sprintf("https://github.com/o/r/pull/42#issuecomment-%d", f.nextID)}
			f.comments = append(f.comments, c)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(c)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	// Update: /repos/o/r/issues/comments/{id}
	mux.HandleFunc("/repos/o/r/issues/comments/", func(w http.ResponseWriter, r *http.Request) {
		f.sawAuth = r.Header.Get("Authorization")
		if r.Method != http.MethodPatch {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var in struct {
			Body string `json:"body"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		idStr := strings.TrimPrefix(r.URL.Path, "/repos/o/r/issues/comments/")
		for i := range f.comments {
			if fmt.Sprintf("%d", f.comments[i].ID) == idStr {
				f.comments[i].Body = in.Body
				_ = json.NewEncoder(w).Encode(f.comments[i])
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	})
	return mux
}

func newTestHost(t *testing.T, srv *httptest.Server) *GitHubHost {
	t.Helper()
	h, err := NewGitHubHost(GitHubHostConfig{
		APIBase: srv.URL,
		Owner:   "o",
		Repo:    "r",
		Issue:   42,
		Token:   "secret-token",
		HTTP:    srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewGitHubHost: %v", err)
	}
	return h
}

// TestGitHubHostUpsertIdempotent proves the real host satisfies the same
// idempotency contract the MockHost does (AC1 sticky semantics): first Upsert
// CREATES exactly one comment carrying the marker; a second Upsert through the
// same host UPDATES that same comment in place (no duplicate).
func TestGitHubHostUpsertIdempotent(t *testing.T) {
	fake := &fakeGitHub{t: t}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	host := newTestHost(t, srv)

	body1 := StickyMarker + "\nfirst body"
	r1, err := Upsert(context.Background(), host, body1)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if r1.Action != ActionCreated {
		t.Fatalf("first upsert action = %q, want %q", r1.Action, ActionCreated)
	}
	if len(fake.comments) != 1 {
		t.Fatalf("after first upsert: %d comments, want 1", len(fake.comments))
	}

	body2 := StickyMarker + "\nsecond body"
	r2, err := Upsert(context.Background(), host, body2)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if r2.Action != ActionUpdated {
		t.Fatalf("second upsert action = %q, want %q", r2.Action, ActionUpdated)
	}
	if r2.CommentID != r1.CommentID {
		t.Fatalf("second upsert id = %q, want same as first %q", r2.CommentID, r1.CommentID)
	}
	if len(fake.comments) != 1 {
		t.Fatalf("after second upsert: %d comments, want 1 (no duplicate)", len(fake.comments))
	}
	if got := fake.comments[0].Body; got != body2 {
		t.Fatalf("comment body not updated in place: %q", got)
	}
}

// TestGitHubHostSendsBearerToken asserts the token travels in the Authorization
// header (S1: never on argv, never in the URL) on every call.
func TestGitHubHostSendsBearerToken(t *testing.T) {
	fake := &fakeGitHub{t: t}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	host := newTestHost(t, srv)

	if _, _, err := host.Find(context.Background(), StickyMarker); err != nil {
		t.Fatalf("find: %v", err)
	}
	if fake.sawAuth != "Bearer secret-token" {
		t.Fatalf("auth header = %q, want %q", fake.sawAuth, "Bearer secret-token")
	}
}

// TestGitHubHostFindMissing returns (false) without error when no comment carries
// the marker — so Upsert branches to Create.
func TestGitHubHostFindMissing(t *testing.T) {
	fake := &fakeGitHub{t: t, comments: []ghComment{{ID: 7, Body: "unrelated chatter"}}}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	host := newTestHost(t, srv)

	_, found, err := host.Find(context.Background(), StickyMarker)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if found {
		t.Fatalf("found = true, want false (no marker present)")
	}
}

// TestNewGitHubHostValidation asserts fail-fast on missing required config (token,
// owner/repo, issue) BEFORE any network call (S1/S2: misconfiguration must not
// post to the wrong place).
func TestNewGitHubHostValidation(t *testing.T) {
	cases := []struct {
		name string
		cfg  GitHubHostConfig
	}{
		{"no token", GitHubHostConfig{Owner: "o", Repo: "r", Issue: 1}},
		{"no owner", GitHubHostConfig{Token: "t", Repo: "r", Issue: 1}},
		{"no repo", GitHubHostConfig{Token: "t", Owner: "o", Issue: 1}},
		{"no issue", GitHubHostConfig{Token: "t", Owner: "o", Repo: "r"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewGitHubHost(tc.cfg); err == nil {
				t.Fatalf("NewGitHubHost(%+v) = nil error, want validation error", tc.cfg)
			}
		})
	}
}

// TestHostFromEnvNoTokenFallsBack proves the env resolver returns (nil, nil) when
// GITHUB_TOKEN is absent so the caller falls back to the offline MockHost (the
// zero-egress local/dry-run path).
func TestHostFromEnvNoTokenFallsBack(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	h, err := HostFromEnv(42)
	if err != nil {
		t.Fatalf("HostFromEnv: %v", err)
	}
	if h != nil {
		t.Fatalf("HostFromEnv with no token = %v, want nil", h)
	}
}

// TestHostFromEnvResolvesFromActionsEnv proves the resolver reads the token from
// the environment (NEVER argv) and parses owner/repo + API base.
func TestHostFromEnvResolvesFromActionsEnv(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "env-token")
	t.Setenv("GITHUB_REPOSITORY", "octo/widget")
	t.Setenv("GITHUB_API_URL", "https://ghe.example.com/api/v3")
	h, err := HostFromEnv(99)
	if err != nil {
		t.Fatalf("HostFromEnv: %v", err)
	}
	if h == nil {
		t.Fatal("HostFromEnv with token = nil, want a host")
	}
	if h.owner != "octo" || h.repo != "widget" {
		t.Fatalf("owner/repo = %q/%q, want octo/widget", h.owner, h.repo)
	}
	if h.apiBase != "https://ghe.example.com/api/v3" {
		t.Fatalf("apiBase = %q", h.apiBase)
	}
	if h.token != "env-token" || h.issue != 99 {
		t.Fatalf("token/issue = %q/%d", h.token, h.issue)
	}
}
