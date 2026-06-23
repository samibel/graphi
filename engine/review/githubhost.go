package review

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// GitHubHost is the REAL CommentHost (SW-043): the single outbound boundary that
// posts/updates the sticky PR comment through the GitHub REST issue-comments API.
//
// It is the ONLY net/http user in the PR-review vertical. Everything upstream of
// the upsert (pr-risk / pr-signals / pr-questions analysis, render, gate) is
// zero-outbound; this type confines all network I/O to the three CommentHost
// methods so the local-first contract is provable by inspection:
//
//   - the engine analyzers import no network packages, and
//   - the gate/render logic imports no network packages, and
//   - this file's net/http use is reachable ONLY when Publish is requested AND a
//     token is present (see HostFromEnv).
//
// The token is held in the struct (sourced from the environment by HostFromEnv —
// NEVER from argv) and sent only in the Authorization header; it is never logged.
type GitHubHost struct {
	// apiBase is the GitHub REST API base (e.g. https://api.github.com). It is the
	// single host this type ever dials. Configurable so GitHub Enterprise and the
	// test transport can redirect it; defaults to public GitHub.
	apiBase string
	// owner/repo/issue identify the PR's issue-comments collection. A PR is an
	// "issue" for the comments API, so issue == PR number.
	owner string
	repo  string
	issue int
	// token authorizes the comment writes (GITHUB_TOKEN). Required.
	token string
	// http is the client used for every call. Injected in tests; a bounded-timeout
	// default in production.
	http *http.Client
}

// DefaultGitHubAPIBase is the public GitHub REST base. GitHub Enterprise Server
// overrides it via GITHUB_API_URL (HostFromEnv reads it).
const DefaultGitHubAPIBase = "https://api.github.com"

// GitHubHostConfig is the explicit constructor input for a GitHubHost. It keeps
// the token out of any positional/argv path and makes every field auditable.
type GitHubHostConfig struct {
	APIBase string       // GitHub REST base; empty => DefaultGitHubAPIBase
	Owner   string       // repository owner (org/user)
	Repo    string       // repository name
	Issue   int          // PR/issue number whose comments collection is upserted
	Token   string       // GITHUB_TOKEN (required); sent only in the Authorization header
	HTTP    *http.Client // optional; a 30s-timeout client is used when nil
}

// NewGitHubHost constructs the real CommentHost. It validates the required fields
// so a misconfigured Action fails fast (before any network call) rather than
// posting to the wrong place. The token is required and never defaulted.
func NewGitHubHost(cfg GitHubHostConfig) (*GitHubHost, error) {
	if cfg.Token == "" {
		return nil, fmt.Errorf("review: github host: token is required")
	}
	if cfg.Owner == "" || cfg.Repo == "" {
		return nil, fmt.Errorf("review: github host: owner and repo are required")
	}
	if cfg.Issue <= 0 {
		return nil, fmt.Errorf("review: github host: a positive issue/PR number is required")
	}
	base := cfg.APIBase
	if base == "" {
		base = DefaultGitHubAPIBase
	}
	base = strings.TrimRight(base, "/")
	hc := cfg.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &GitHubHost{
		apiBase: base,
		owner:   cfg.Owner,
		repo:    cfg.Repo,
		issue:   cfg.Issue,
		token:   cfg.Token,
		http:    hc,
	}, nil
}

// HostFromEnv resolves a real GitHubHost from the standard GitHub Actions
// environment, returning (nil, nil) when the environment does not request a real
// publish (no token) so the caller can fall back to the offline MockHost. This is
// the SINGLE place the token is read from the environment (NEVER from argv): the
// Action passes GITHUB_TOKEN through env, satisfying the security contract that
// the secret never appears on a world-readable command line.
//
//   - GITHUB_TOKEN          — the secret (required for a real host; absent => nil)
//   - GITHUB_REPOSITORY     — "owner/repo" (Actions sets this automatically)
//   - GITHUB_API_URL        — REST base (Actions sets this; empty => public GitHub)
//
// issue is the PR number (resolved by the entrypoint from the event payload).
func HostFromEnv(issue int) (*GitHubHost, error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		// No token => caller uses the offline MockHost (dry-run / local).
		return nil, nil
	}
	owner, repo, err := splitRepo(os.Getenv("GITHUB_REPOSITORY"))
	if err != nil {
		return nil, err
	}
	if issue <= 0 {
		// Allow the PR number to come from GITHUB_PR_NUMBER as a fallback.
		if n, perr := strconv.Atoi(strings.TrimSpace(os.Getenv("GITHUB_PR_NUMBER"))); perr == nil {
			issue = n
		}
	}
	return NewGitHubHost(GitHubHostConfig{
		APIBase: os.Getenv("GITHUB_API_URL"),
		Owner:   owner,
		Repo:    repo,
		Issue:   issue,
		Token:   token,
	})
}

// splitRepo parses "owner/repo" into its parts.
func splitRepo(s string) (owner, repo string, err error) {
	s = strings.TrimSpace(s)
	i := strings.IndexByte(s, '/')
	if i <= 0 || i == len(s)-1 {
		return "", "", fmt.Errorf("review: github host: GITHUB_REPOSITORY must be owner/repo, got %q", s)
	}
	return s[:i], s[i+1:], nil
}

// ghComment is the subset of the GitHub issue-comment JSON the upsert needs.
type ghComment struct {
	ID      int64  `json:"id"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
}

// Find implements CommentHost. It pages the issue's comments and returns the
// FIRST comment whose body carries the sticky marker. A missing marker is
// (Comment{}, false, nil) — never an error — so Upsert branches on presence.
func (h *GitHubHost) Find(ctx context.Context, marker string) (Comment, bool, error) {
	if marker == "" {
		return Comment{}, false, nil
	}
	page := 1
	for {
		url := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments?per_page=100&page=%d",
			h.apiBase, h.owner, h.repo, h.issue, page)
		var got []ghComment
		if err := h.do(ctx, http.MethodGet, url, nil, &got); err != nil {
			return Comment{}, false, fmt.Errorf("review: github host: list comments: %w", err)
		}
		for _, c := range got {
			if strings.Contains(c.Body, marker) {
				return Comment{ID: strconv.FormatInt(c.ID, 10), Body: c.Body}, true, nil
			}
		}
		if len(got) < 100 {
			return Comment{}, false, nil
		}
		page++
	}
}

// Create implements CommentHost. It POSTs a new issue comment and returns it with
// the host-assigned id and html_url (carried back through Comment.ID; the URL is
// surfaced via the upsert result the surfaces serialize).
func (h *GitHubHost) Create(ctx context.Context, body string) (Comment, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments", h.apiBase, h.owner, h.repo, h.issue)
	var out ghComment
	if err := h.do(ctx, http.MethodPost, url, map[string]string{"body": body}, &out); err != nil {
		return Comment{}, fmt.Errorf("review: github host: create comment: %w", err)
	}
	return Comment{ID: strconv.FormatInt(out.ID, 10), Body: out.Body}, nil
}

// Update implements CommentHost. It PATCHes the comment identified by id (the
// stable GitHub comment id) with the new body — the idempotent re-run path.
func (h *GitHubHost) Update(ctx context.Context, id, body string) (Comment, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/issues/comments/%s", h.apiBase, h.owner, h.repo, id)
	var out ghComment
	if err := h.do(ctx, http.MethodPatch, url, map[string]string{"body": body}, &out); err != nil {
		return Comment{}, fmt.Errorf("review: github host: update comment: %w", err)
	}
	return Comment{ID: strconv.FormatInt(out.ID, 10), Body: out.Body}, nil
}

// do is the single HTTP exec helper: it builds the request with the standard
// GitHub headers (Bearer auth — token NEVER logged — and the REST API version),
// sends it through the injected client, and decodes a 2xx JSON body into out.
// Every network call in the vertical funnels through here.
func (h *GitHubHost) do(ctx context.Context, method, url string, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := h.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, redactURL(url), err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s %s: unexpected status %d: %s", method, redactURL(url), resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// redactURL is defensive: error messages echo the URL but never the token (the
// token lives only in the Authorization header, never the URL, so this is purely
// belt-and-suspenders against future refactors that might append query secrets).
func redactURL(u string) string {
	if i := strings.IndexByte(u, '?'); i >= 0 {
		return u[:i]
	}
	return u
}

// Static interface check: GitHubHost satisfies CommentHost.
var _ CommentHost = (*GitHubHost)(nil)
