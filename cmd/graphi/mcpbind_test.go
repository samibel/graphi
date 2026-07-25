package main

import (
	"strings"
	"testing"
	"time"

	rtime "github.com/samibel/graphi/cmd/internal/runtime"
	"github.com/samibel/graphi/engine/ingest"
)

// TestMCPBindStatus_RenderStrings pins the exact client-visible detail lines
// the MCP server splices into its retryable -32002 errors, per mode and phase.
func TestMCPBindStatus_RenderStrings(t *testing.T) {
	base := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		prepare func(b *mcpBindStatus, clock *time.Time)
		want    string
	}{
		{
			name:    "idle renders empty (static fallback)",
			prepare: func(b *mcpBindStatus, clock *time.Time) {},
			want:    "",
		},
		{
			name: "binding before root resolution",
			prepare: func(b *mcpBindStatus, clock *time.Time) {
				b.BeginAttempt()
				*clock = base.Add(2 * time.Second)
			},
			want: "resolving repository root (2s elapsed)",
		},
		{
			name: "binding with resolved root",
			prepare: func(b *mcpBindStatus, clock *time.Time) {
				b.BeginAttempt()
				b.HandleEvent(rtime.BindEvent{Kind: rtime.BindRootResolved, Root: "/work/mono"})
				*clock = base.Add(2 * time.Second)
			},
			want: "preparing /work/mono (2s elapsed)",
		},
		{
			name: "waiting for another process",
			prepare: func(b *mcpBindStatus, clock *time.Time) {
				b.BeginAttempt()
				b.HandleEvent(rtime.BindEvent{Kind: rtime.BindRootResolved, Root: "/work/mono"})
				b.HandleEvent(rtime.BindEvent{Kind: rtime.BindLockWaiting, Root: "/work/mono"})
				*clock = base.Add(2*time.Minute + 5*time.Second)
			},
			want: "waiting for another graphi process indexing /work/mono (waited 2m5s)",
		},
		{
			name: "lock acquired returns to binding",
			prepare: func(b *mcpBindStatus, clock *time.Time) {
				b.BeginAttempt()
				b.HandleEvent(rtime.BindEvent{Kind: rtime.BindRootResolved, Root: "/work/mono"})
				b.HandleEvent(rtime.BindEvent{Kind: rtime.BindLockWaiting, Root: "/work/mono"})
				b.HandleEvent(rtime.BindEvent{Kind: rtime.BindLockAcquired, Root: "/work/mono"})
				*clock = base.Add(3 * time.Second)
			},
			want: "preparing /work/mono (3s elapsed)",
		},
		{
			name: "walk phase with count",
			prepare: func(b *mcpBindStatus, clock *time.Time) {
				b.BeginAttempt()
				b.HandleEvent(rtime.BindEvent{Kind: rtime.BindRootResolved, Root: "/work/mono"})
				b.HandleProgress(ingest.ProgressEvent{Phase: ingest.PhaseWalk, Done: 1234})
				*clock = base.Add(12 * time.Second)
			},
			want: "indexing /work/mono: scanning repository, 1234 files found (12s elapsed)",
		},
		{
			name: "walk phase without count",
			prepare: func(b *mcpBindStatus, clock *time.Time) {
				b.BeginAttempt()
				b.HandleEvent(rtime.BindEvent{Kind: rtime.BindRootResolved, Root: "/work/mono"})
				b.HandleProgress(ingest.ProgressEvent{Phase: ingest.PhaseWalk})
				*clock = base.Add(1 * time.Second)
			},
			want: "indexing /work/mono: scanning repository (1s elapsed)",
		},
		{
			name: "drift phase with count",
			prepare: func(b *mcpBindStatus, clock *time.Time) {
				b.BeginAttempt()
				b.HandleEvent(rtime.BindEvent{Kind: rtime.BindRootResolved, Root: "/work/mono"})
				b.HandleProgress(ingest.ProgressEvent{Phase: ingest.PhaseDrift, Done: 1234})
				*clock = base.Add(5 * time.Second)
			},
			want: "checking /work/mono for changes, 1234 files checked (5s elapsed)",
		},
		{
			name: "parse phase",
			prepare: func(b *mcpBindStatus, clock *time.Time) {
				b.BeginAttempt()
				b.HandleEvent(rtime.BindEvent{Kind: rtime.BindRootResolved, Root: "/work/mono"})
				b.HandleProgress(ingest.ProgressEvent{Phase: ingest.PhaseParse, Done: 1234, Total: 5678})
				*clock = base.Add(3*time.Minute + 10*time.Second)
			},
			want: "indexing /work/mono: parse 1234/5678 files (3m10s elapsed)",
		},
		{
			name: "link phase",
			prepare: func(b *mcpBindStatus, clock *time.Time) {
				b.BeginAttempt()
				b.HandleEvent(rtime.BindEvent{Kind: rtime.BindRootResolved, Root: "/work/mono"})
				b.HandleProgress(ingest.ProgressEvent{Phase: ingest.PhaseLink})
				*clock = base.Add(4 * time.Minute)
			},
			want: "indexing /work/mono: linking cross-file references (4m0s elapsed)",
		},
		{
			name: "resolve phase",
			prepare: func(b *mcpBindStatus, clock *time.Time) {
				b.BeginAttempt()
				b.HandleEvent(rtime.BindEvent{Kind: rtime.BindRootResolved, Root: "/work/mono"})
				b.HandleProgress(ingest.ProgressEvent{Phase: ingest.PhaseResolve})
				*clock = base.Add(4*time.Minute + 12*time.Second)
			},
			want: "indexing /work/mono: resolving types (4m12s elapsed)",
		},
		{
			name: "write/fts/checkpoint phases render as finishing up",
			prepare: func(b *mcpBindStatus, clock *time.Time) {
				b.BeginAttempt()
				b.HandleEvent(rtime.BindEvent{Kind: rtime.BindRootResolved, Root: "/work/mono"})
				b.HandleProgress(ingest.ProgressEvent{Phase: ingest.PhaseCheckpoint})
				*clock = base.Add(4*time.Minute + 40*time.Second)
			},
			want: "indexing /work/mono: finishing up (4m40s elapsed)",
		},
		{
			name: "clear returns to the static fallback",
			prepare: func(b *mcpBindStatus, clock *time.Time) {
				b.BeginAttempt()
				b.HandleProgress(ingest.ProgressEvent{Phase: ingest.PhaseParse, Done: 1, Total: 2})
				b.Clear()
			},
			want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := base
			b := newMCPBindStatus()
			b.now = func() time.Time { return clock }
			tc.prepare(b, &clock)
			if got := b.Render(); got != tc.want {
				t.Fatalf("Render() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAnnounceBindEvent_ThrottlesWaitingLines pins the stderr cadence: one
// line on the first lock-busy observation, then at most one refresh per
// lockWaitingAnnounceGap, and single lines for root resolution / acquisition.
func TestAnnounceBindEvent_ThrottlesWaitingLines(t *testing.T) {
	clock := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	b := newMCPBindStatus()
	b.now = func() time.Time { return clock }
	b.BeginAttempt()

	var out strings.Builder
	announce := func(ev rtime.BindEvent) {
		b.HandleEvent(ev)
		b.announceBindEvent(&out, ev)
	}

	announce(rtime.BindEvent{Kind: rtime.BindRootResolved, Root: "/work/mono"})
	waiting := rtime.BindEvent{Kind: rtime.BindLockWaiting, Root: "/work/mono"}
	announce(waiting) // first observation: announced
	for range 5 {     // within the gap: throttled
		clock = clock.Add(5 * time.Second)
		announce(waiting)
	}
	clock = clock.Add(lockWaitingAnnounceGap) // past the gap: one refresh
	announce(waiting)
	announce(rtime.BindEvent{Kind: rtime.BindLockAcquired, Root: "/work/mono"})

	want := "graphi: mcp: binding repository root /work/mono\n" +
		"graphi: mcp: waiting for another graphi process indexing /work/mono\n" +
		"graphi: mcp: still waiting for the ingest lock on /work/mono (1m25s elapsed)\n" +
		"graphi: mcp: ingest lock acquired — continuing\n"
	if out.String() != want {
		t.Fatalf("announced lines:\n%q\nwant:\n%q", out.String(), want)
	}
}
