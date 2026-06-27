// Package forge is the read-only PR-enumeration boundary for the EP-018 multi-PR
// triage suite (SW-105). It is a SURFACE-layer ingestion client: it lists open
// pull requests and fetches their lightweight metadata from the configured forge
// (the GitHub REST API) so the surfaces can hand the already-enumerated PR set to
// the zero-egress engine `triage-prs` analyzer.
//
// This package is the suite's ONLY outbound path. It is deliberately distinct
// from the comment-posting engine/review.GitHubHost: it performs discovery and
// metadata fetch ONLY — no scoring, no comment posting, no mutation. Keeping the
// enumeration egress at the surface boundary is what lets the engine stay
// strictly local-first/zero-outbound while still scoring real PRs.
//
// The Enumerator seam is injectable so tests drive the suite against an in-memory
// fixture and perform NO real network I/O. The real GitHubForge is the only type
// that dials, and it is explicitly allowlisted in internal/canary as the
// documented, read-only forge boundary (mirroring engine/review's single egress).
package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/samibel/graphi/core/model"
)

// PR is the read-only, lightweight metadata for one open pull request. It carries
// exactly what `list_prs` returns and what the engine `triage-prs` analyzer needs
// to resolve the touched-node set — never a diff body, never review prose.
type PR struct {
	Number       int      `json:"number"`
	Title        string   `json:"title"`
	Author       string   `json:"author"`
	BaseRef      string   `json:"base_ref"`
	HeadRef      string   `json:"head_ref"`
	HeadSHA      string   `json:"head_sha"`
	ChangedFiles []string `json:"changed_files"`
	Additions    int      `json:"additions"`
	Deletions    int      `json:"deletions"`
	Mergeable    string   `json:"mergeable"`
}

// Enumerator is the injectable, mockable PR-enumeration seam. The real
// GitHubForge dials the forge; tests inject an in-memory implementation so the
// suite does zero network I/O. ListOpenPRs returns the open PR set with metadata;
// it performs NO graph scoring and posts NO comments.
type Enumerator interface {
	ListOpenPRs(ctx context.Context) ([]PR, error)
}

// PRListSchemaVersion versions the `list_prs` JSON envelope shape.
const PRListSchemaVersion = 1

// PRList is the versioned, byte-stable `list_prs` envelope. PRs are emitted in
// canonical PR-number order so identical forge state yields byte-identical output.
type PRList struct {
	SchemaVersion int    `json:"schema_version"`
	Outcome       string `json:"outcome"` // found | empty
	PRs           []PR   `json:"prs"`
}

// normalize returns a defensively-copied PR with its changed-file paths
// normalized (via model.NormalizePath) and sorted, so the metadata is
// machine-independent and byte-stable regardless of the forge's response order.
func normalize(p PR) PR {
	files := make([]string, 0, len(p.ChangedFiles))
	for _, f := range p.ChangedFiles {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		files = append(files, model.NormalizePath(f))
	}
	sort.Strings(files)
	p.ChangedFiles = dedupeStrings(files)
	return p
}

func dedupeStrings(s []string) []string {
	if len(s) == 0 {
		return []string{}
	}
	out := s[:1]
	for i := 1; i < len(s); i++ {
		if s[i] != out[len(out)-1] {
			out = append(out, s[i])
		}
	}
	return out
}

// MarshalPRList is the single canonical serializer for the `list_prs` envelope,
// shared by every surface. It normalizes + sorts (PR number ASC), disables HTML
// escaping, and trims the trailing newline — byte-for-byte stable across runs and
// surfaces (mirrors the analysis.Marshal discipline). It performs NO scoring.
func MarshalPRList(prs []PR) ([]byte, error) {
	norm := make([]PR, 0, len(prs))
	for _, p := range prs {
		norm = append(norm, normalize(p))
	}
	sort.SliceStable(norm, func(i, j int) bool { return norm[i].Number < norm[j].Number })

	out := PRList{SchemaVersion: PRListSchemaVersion, Outcome: "empty", PRs: norm}
	if len(norm) > 0 {
		out.Outcome = "found"
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, fmt.Errorf("forge: marshal pr list: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// DefaultGitHubAPIBase is the public GitHub REST base.
const DefaultGitHubAPIBase = "https://api.github.com"

// Config is the explicit constructor input for a GitHubForge (token kept off any
// argv path; sourced from the environment by FromEnv).
type Config struct {
	APIBase string
	Owner   string
	Repo    string
	Token   string
	HTTP    *http.Client
}

// GitHubForge is the real, read-only PR-enumeration client. It is the suite's
// single outbound boundary: it issues GET requests to the GitHub REST pulls API
// and posts/ mutates nothing. The token is held in the struct (sourced from the
// environment) and sent only in the Authorization header; it is never logged.
type GitHubForge struct {
	apiBase string
	owner   string
	repo    string
	token   string
	http    *http.Client
}

// NewGitHubForge constructs the read-only forge client, validating the required
// fields so a misconfigured caller fails fast before any network call.
func NewGitHubForge(cfg Config) (*GitHubForge, error) {
	if cfg.Token == "" {
		return nil, fmt.Errorf("forge: github: token is required")
	}
	if cfg.Owner == "" || cfg.Repo == "" {
		return nil, fmt.Errorf("forge: github: owner and repo are required")
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
	return &GitHubForge{apiBase: base, owner: cfg.Owner, repo: cfg.Repo, token: cfg.Token, http: hc}, nil
}

// FromEnv resolves a GitHubForge from the standard GitHub Actions environment,
// returning (nil, nil) when no token is present so callers can fall back to a
// mock/offline path. The token is read ONLY from the environment (never argv).
func FromEnv() (*GitHubForge, error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return nil, nil
	}
	owner, repo, err := splitRepo(os.Getenv("GITHUB_REPOSITORY"))
	if err != nil {
		return nil, err
	}
	return NewGitHubForge(Config{
		APIBase: os.Getenv("GITHUB_API_URL"),
		Owner:   owner,
		Repo:    repo,
		Token:   token,
	})
}

func splitRepo(s string) (owner, repo string, err error) {
	s = strings.TrimSpace(s)
	i := strings.IndexByte(s, '/')
	if i <= 0 || i == len(s)-1 {
		return "", "", fmt.Errorf("forge: github: GITHUB_REPOSITORY must be owner/repo, got %q", s)
	}
	return s[:i], s[i+1:], nil
}

// ghPull is the subset of the GitHub pulls JSON the enumerator reads.
type ghPull struct {
	Number int                    `json:"number"`
	Title  string                 `json:"title"`
	User   struct{ Login string } `json:"user"`
	Base   struct{ Ref string }   `json:"base"`
	Head   struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Additions int   `json:"additions"`
	Deletions int   `json:"deletions"`
	Mergeable *bool `json:"mergeable"`
}

// ghFile is the subset of the GitHub pull-files JSON the enumerator reads.
type ghFile struct {
	Filename string `json:"filename"`
}

// ListOpenPRs implements Enumerator. It pages the open pull requests and fetches
// each PR's changed-file list, returning normalized metadata. It is read-only:
// every request is a GET; it posts/mutates nothing.
func (g *GitHubForge) ListOpenPRs(ctx context.Context) ([]PR, error) {
	var pulls []ghPull
	page := 1
	for {
		url := fmt.Sprintf("%s/repos/%s/%s/pulls?state=open&per_page=100&page=%d", g.apiBase, g.owner, g.repo, page)
		var got []ghPull
		if err := g.get(ctx, url, &got); err != nil {
			return nil, fmt.Errorf("forge: list open prs: %w", err)
		}
		pulls = append(pulls, got...)
		if len(got) < 100 {
			break
		}
		page++
	}

	out := make([]PR, 0, len(pulls))
	for _, p := range pulls {
		files, err := g.listFiles(ctx, p.Number)
		if err != nil {
			return nil, err
		}
		out = append(out, PR{
			Number:       p.Number,
			Title:        p.Title,
			Author:       p.User.Login,
			BaseRef:      p.Base.Ref,
			HeadRef:      p.Head.Ref,
			HeadSHA:      p.Head.SHA,
			ChangedFiles: files,
			Additions:    p.Additions,
			Deletions:    p.Deletions,
			Mergeable:    mergeableState(p.Mergeable),
		})
	}
	return out, nil
}

func (g *GitHubForge) listFiles(ctx context.Context, number int) ([]string, error) {
	var files []string
	page := 1
	for {
		url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/files?per_page=100&page=%d", g.apiBase, g.owner, g.repo, number, page)
		var got []ghFile
		if err := g.get(ctx, url, &got); err != nil {
			return nil, fmt.Errorf("forge: list pr files: %w", err)
		}
		for _, f := range got {
			files = append(files, f.Filename)
		}
		if len(got) < 100 {
			break
		}
		page++
	}
	return files, nil
}

func mergeableState(m *bool) string {
	if m == nil {
		return "unknown"
	}
	if *m {
		return "mergeable"
	}
	return "conflicting"
}

// get is the single read-only HTTP exec helper: it builds a GET request with the
// standard GitHub headers (Bearer auth — token NEVER logged) and decodes the 2xx
// JSON body into out. Every network call funnels through here.
func (g *GitHubForge) get(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := g.http.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", redactURL(url), err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("GET %s: unexpected status %d: %s", redactURL(url), resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// redactURL strips any query string from a URL for error messages (defensive: the
// token lives only in the Authorization header, never the URL).
func redactURL(u string) string {
	if i := strings.IndexByte(u, '?'); i >= 0 {
		return u[:i]
	}
	return u
}

// MockForge is the in-memory Enumerator used by tests and offline/local runs. It
// performs ZERO network I/O — it returns the fixed PR set it was constructed with.
type MockForge struct {
	PRs []PR
}

// NewMockForge builds an offline enumerator over a fixed PR set.
func NewMockForge(prs []PR) *MockForge { return &MockForge{PRs: prs} }

// ListOpenPRs implements Enumerator with no network I/O.
func (m *MockForge) ListOpenPRs(_ context.Context) ([]PR, error) {
	out := make([]PR, len(m.PRs))
	copy(out, m.PRs)
	return out, nil
}

// static interface checks.
var (
	_ Enumerator = (*GitHubForge)(nil)
	_ Enumerator = (*MockForge)(nil)
)
