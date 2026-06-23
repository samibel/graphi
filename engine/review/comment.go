package review

import (
	"context"
	"fmt"
	"strings"
)

// Upsert actions — the enumerated outcome of a sticky upsert.
const (
	// ActionCreated: no comment bearing the marker existed, so exactly one was
	// created.
	ActionCreated = "created"
	// ActionUpdated: a comment bearing the marker already existed, so it was
	// updated IN PLACE (no duplicate) — the idempotent re-run path.
	ActionUpdated = "updated"
)

// UpsertResult records WHICH sticky action occurred and the affected comment id,
// so a caller (and the AC1 test) can assert idempotency: a second run on a host
// that already carries the marker yields Action=updated keyed on the existing id,
// with exactly one comment present.
type UpsertResult struct {
	Action    string `json:"action"`     // created | updated
	CommentID string `json:"comment_id"` // host id of the created/updated comment
}

// Upsert posts the rendered body as the sticky PR comment THROUGH the mockable
// CommentHost boundary (the single outbound seam). It is idempotent:
//
//  1. Find the comment carrying the StickyMarker.
//  2. If present -> Update it IN PLACE (keyed on its host id). No duplicate.
//  3. If absent  -> Create exactly one comment.
//
// All network is confined to the injected host; this function itself imports no
// network/exec packages (AC1, AC3). The body MUST contain the StickyMarker (it
// always does — Render writes it first); this is asserted defensively.
func Upsert(ctx context.Context, host CommentHost, body string) (UpsertResult, error) {
	if host == nil {
		return UpsertResult{}, fmt.Errorf("review: nil comment host")
	}
	if !containsMarker(body) {
		return UpsertResult{}, fmt.Errorf("review: rendered body is missing the sticky marker")
	}

	existing, found, err := host.Find(ctx, StickyMarker)
	if err != nil {
		return UpsertResult{}, fmt.Errorf("review: find sticky comment: %w", err)
	}
	if found {
		updated, err := host.Update(ctx, existing.ID, body)
		if err != nil {
			return UpsertResult{}, fmt.Errorf("review: update sticky comment: %w", err)
		}
		return UpsertResult{Action: ActionUpdated, CommentID: updated.ID}, nil
	}

	created, err := host.Create(ctx, body)
	if err != nil {
		return UpsertResult{}, fmt.Errorf("review: create sticky comment: %w", err)
	}
	return UpsertResult{Action: ActionCreated, CommentID: created.ID}, nil
}

// containsMarker reports whether body carries the sticky marker.
func containsMarker(body string) bool {
	return strings.Contains(body, StickyMarker)
}
