package review

import (
	"context"
	"sort"
	"strconv"
	"strings"
)

// Comment is one PR-host comment as seen through the CommentHost boundary. It is
// intentionally minimal — only the fields the sticky upsert needs — so the
// interface stays a thin, host-agnostic contract (GitHub, GitLab, …). The ID is
// the host's opaque comment identifier; the upsert keys its UPDATE on it.
type Comment struct {
	ID   string `json:"id"`   // host-opaque comment identifier
	Body string `json:"body"` // full comment body (carries the hidden sticky marker)
}

// CommentHost is the SINGLE outbound boundary of the otherwise zero-outbound
// engine: the PR-host comment API. It is deliberately small and TOTAL so the
// sticky-upsert logic (comment.go) stays host-agnostic and fully offline-testable
// through a mock:
//
//   - Find returns the existing comment that carries the given sticky marker, if
//     any. A missing comment is reported as (Comment{}, false, nil) — NOT an
//     error — so upsert can branch on presence without error-sniffing.
//   - Create posts a new comment with the given body and returns it (with the
//     host-assigned ID).
//   - Update replaces the body of the comment identified by id and returns it.
//
// The real network implementation (which holds the host token and imports
// net/http) lives behind this interface; the renderer and gate never see it. The
// MockHost below is the test double used in every test in this package.
type CommentHost interface {
	Find(ctx context.Context, marker string) (Comment, bool, error)
	Create(ctx context.Context, body string) (Comment, error)
	Update(ctx context.Context, id, body string) (Comment, error)
}

// MockHost is the in-memory CommentHost used by every test (and by the CLI/MCP
// dry-run default). It holds NO token and performs NO network I/O. It records
// every call so a test can assert idempotency (exactly one comment, an UPDATE
// keyed on the marker's comment id on the second run).
//
// MockHost is safe to construct with a zero value; the comment store and counters
// initialize lazily.
type MockHost struct {
	// comments is the host-side comment store keyed by comment ID.
	comments map[string]Comment
	// nextID seeds the deterministic ID assigned to created comments.
	nextID int

	// Recorded call log (assertable by tests).
	Creates    int      // number of Create calls
	Updates    int      // number of Update calls
	Finds      int      // number of Find calls
	UpdatedIDs []string // ids passed to Update, in call order
	CreatedIDs []string // ids assigned by Create, in call order
}

// NewMockHost returns an empty MockHost (no pre-existing comments).
func NewMockHost() *MockHost {
	return &MockHost{comments: map[string]Comment{}}
}

// Seed inserts a pre-existing comment (e.g. a prior sticky comment) and returns
// its assigned ID. It is the test helper that sets up the "comment already
// exists" precondition for the idempotency AC.
func (m *MockHost) Seed(body string) string {
	if m.comments == nil {
		m.comments = map[string]Comment{}
	}
	id := m.allocID()
	m.comments[id] = Comment{ID: id, Body: body}
	return id
}

// allocID returns the next deterministic comment ID.
func (m *MockHost) allocID() string {
	m.nextID++
	return "c" + strconv.Itoa(m.nextID)
}

// Find implements CommentHost. It returns the FIRST comment (in deterministic
// ID order) whose body contains the marker substring. A missing marker is
// (Comment{}, false, nil), never an error.
func (m *MockHost) Find(_ context.Context, marker string) (Comment, bool, error) {
	m.Finds++
	if m.comments == nil || marker == "" {
		return Comment{}, false, nil
	}
	ids := make([]string, 0, len(m.comments))
	for id := range m.comments {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		c := m.comments[id]
		if strings.Contains(c.Body, marker) {
			return c, true, nil
		}
	}
	return Comment{}, false, nil
}

// Create implements CommentHost. It stores a new comment and records the call.
func (m *MockHost) Create(_ context.Context, body string) (Comment, error) {
	if m.comments == nil {
		m.comments = map[string]Comment{}
	}
	id := m.allocID()
	c := Comment{ID: id, Body: body}
	m.comments[id] = c
	m.Creates++
	m.CreatedIDs = append(m.CreatedIDs, id)
	return c, nil
}

// Update implements CommentHost. It replaces the body of the comment identified
// by id and records the call. Updating an unknown id is reported as an error so
// a logic bug in upsert (updating a comment that does not exist) surfaces in
// tests rather than silently creating one.
func (m *MockHost) Update(_ context.Context, id, body string) (Comment, error) {
	if m.comments == nil {
		m.comments = map[string]Comment{}
	}
	if _, ok := m.comments[id]; !ok {
		return Comment{}, &updateUnknownError{id: id}
	}
	c := Comment{ID: id, Body: body}
	m.comments[id] = c
	m.Updates++
	m.UpdatedIDs = append(m.UpdatedIDs, id)
	return c, nil
}

// Count returns the number of comments currently held (the idempotency assertion
// checks this is exactly 1 after any number of upserts).
func (m *MockHost) Count() int { return len(m.comments) }

// Get returns the stored comment for id (test helper).
func (m *MockHost) Get(id string) (Comment, bool) {
	c, ok := m.comments[id]
	return c, ok
}

// updateUnknownError is the typed error returned when Update is called with an
// id the host does not know. Kept private; tests assert on its message.
type updateUnknownError struct{ id string }

func (e *updateUnknownError) Error() string {
	return "review: mock host: update of unknown comment id " + e.id
}
