package githistory

import (
	"context"
	"testing"
	"time"
)

// refTime is a fixed reference time for deterministic tests.
var refTime = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

// helper to build a commit quickly.
func commit(sha, author string, daysAgo int, files ...string) Commit {
	return Commit{
		SHA:          sha,
		Author:       author,
		Timestamp:    refTime.AddDate(0, 0, -daysAgo),
		FilesChanged: files,
	}
}

// --- Test 1: churn basic ---------------------------------------------------

func TestChurnBasic(t *testing.T) {
	provider := &InMemoryProvider{
		Commits: []Commit{
			commit("aaa", "alice", 1, "main.go", "util.go"),
			commit("bbb", "bob", 2, "main.go"),
			commit("ccc", "alice", 3, "util.go"),
		},
	}
	a := New(provider, Config{Now: refTime})
	result, err := a.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// main.go: 2 commits (aaa, bbb); util.go: 2 commits (aaa, ccc).
	if len(result.ChurnScores) != 2 {
		t.Fatalf("want 2 churn scores, got %d", len(result.ChurnScores))
	}

	byPath := make(map[string]ChurnScore)
	for _, cs := range result.ChurnScores {
		byPath[cs.Path] = cs
	}

	main := byPath["main.go"]
	if main.Commits != 2 {
		t.Errorf("main.go commits: want 2, got %d", main.Commits)
	}
	if main.LastCommitSHA != "aaa" {
		t.Errorf("main.go last SHA: want aaa, got %s", main.LastCommitSHA)
	}
	if main.LastAuthor != "alice" {
		t.Errorf("main.go last author: want alice, got %s", main.LastAuthor)
	}

	util := byPath["util.go"]
	if util.Commits != 2 {
		t.Errorf("util.go commits: want 2, got %d", util.Commits)
	}
}

// --- Test 2: bus-factor single author (risky) ------------------------------

func TestBusFactorSingleAuthor(t *testing.T) {
	provider := &InMemoryProvider{
		Commits: []Commit{
			commit("aaa", "alice", 1, "solo.go"),
			commit("bbb", "alice", 2, "solo.go"),
			commit("ccc", "alice", 3, "solo.go"),
		},
	}
	a := New(provider, Config{Now: refTime})
	result, err := a.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(result.BusFactors) != 1 {
		t.Fatalf("want 1 bus-factor entry, got %d", len(result.BusFactors))
	}
	bf := result.BusFactors[0]
	if bf.UniqueAuthors != 1 {
		t.Errorf("unique authors: want 1, got %d", bf.UniqueAuthors)
	}
	if !bf.Risky {
		t.Error("single-author file should be risky")
	}
}

// --- Test 3: bus-factor multi author (safe) --------------------------------

func TestBusFactorMultiAuthor(t *testing.T) {
	provider := &InMemoryProvider{
		Commits: []Commit{
			commit("aaa", "alice", 1, "shared.go"),
			commit("bbb", "bob", 2, "shared.go"),
			commit("ccc", "carol", 3, "shared.go"),
		},
	}
	a := New(provider, Config{Now: refTime})
	result, err := a.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(result.BusFactors) != 1 {
		t.Fatalf("want 1 bus-factor entry, got %d", len(result.BusFactors))
	}
	bf := result.BusFactors[0]
	if bf.UniqueAuthors != 3 {
		t.Errorf("unique authors: want 3, got %d", bf.UniqueAuthors)
	}
	if bf.Risky {
		t.Error("3-author file should NOT be risky with default threshold 1")
	}
	if len(bf.Authors) != 3 {
		t.Errorf("authors list: want 3, got %d", len(bf.Authors))
	}
	// Authors should be sorted.
	if bf.Authors[0] != "alice" || bf.Authors[1] != "bob" || bf.Authors[2] != "carol" {
		t.Errorf("authors should be sorted: got %v", bf.Authors)
	}
}

// --- Test 4: co-change detection -------------------------------------------

func TestCoChangeDetection(t *testing.T) {
	provider := &InMemoryProvider{
		Commits: []Commit{
			commit("aaa", "alice", 1, "api.go", "handler.go"),
			commit("bbb", "bob", 2, "api.go", "handler.go"),
			commit("ccc", "carol", 3, "api.go", "unrelated.go"),
		},
	}
	a := New(provider, Config{Now: refTime, MinCoCommits: 2})
	result, err := a.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// api.go + handler.go changed together in commits aaa and bbb (count=2).
	// api.go + unrelated.go changed together only in ccc (count=1, below threshold).
	if len(result.CoChangeGroups) != 1 {
		t.Fatalf("want 1 co-change group, got %d", len(result.CoChangeGroups))
	}
	g := result.CoChangeGroups[0]
	if g.CoCommits != 2 {
		t.Errorf("co-commits: want 2, got %d", g.CoCommits)
	}
	if len(g.Files) != 2 || g.Files[0] != "api.go" || g.Files[1] != "handler.go" {
		t.Errorf("files: want [api.go handler.go], got %v", g.Files)
	}
}

// --- Test 5: bounded window (max commits) ----------------------------------

func TestBoundedWindowMaxCommits(t *testing.T) {
	provider := &InMemoryProvider{
		Commits: []Commit{
			commit("aaa", "alice", 1, "a.go"),
			commit("bbb", "bob", 2, "b.go"),
			commit("ccc", "carol", 3, "c.go"),
			commit("ddd", "dave", 4, "d.go"),
		},
	}
	// Limit to 2 commits.
	a := New(provider, Config{Now: refTime, MaxCommits: 2})
	result, err := a.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Only aaa and bbb should be included.
	if len(result.ChurnScores) != 2 {
		t.Fatalf("want 2 churn scores (bounded), got %d", len(result.ChurnScores))
	}
	paths := make(map[string]bool)
	for _, cs := range result.ChurnScores {
		paths[cs.Path] = true
	}
	if !paths["a.go"] || !paths["b.go"] {
		t.Errorf("expected a.go and b.go, got %v", result.ChurnScores)
	}
}

// --- Test 6: bounded window (max age) --------------------------------------

func TestBoundedWindowMaxAge(t *testing.T) {
	provider := &InMemoryProvider{
		Commits: []Commit{
			commit("aaa", "alice", 5, "recent.go"),
			commit("bbb", "bob", 60, "old.go"),
		},
	}
	// Only include commits from the last 30 days.
	a := New(provider, Config{Now: refTime, MaxAge: 30 * 24 * time.Hour})
	result, err := a.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(result.ChurnScores) != 1 {
		t.Fatalf("want 1 churn score (age-bounded), got %d", len(result.ChurnScores))
	}
	if result.ChurnScores[0].Path != "recent.go" {
		t.Errorf("want recent.go, got %s", result.ChurnScores[0].Path)
	}
}

// --- Test 7: empty history -------------------------------------------------

func TestEmptyHistory(t *testing.T) {
	provider := &InMemoryProvider{Commits: nil}
	a := New(provider, Config{Now: refTime})
	result, err := a.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(result.ChurnScores) != 0 {
		t.Errorf("want 0 churn scores, got %d", len(result.ChurnScores))
	}
	if len(result.BusFactors) != 0 {
		t.Errorf("want 0 bus factors, got %d", len(result.BusFactors))
	}
	if len(result.CoChangeGroups) != 0 {
		t.Errorf("want 0 co-change groups, got %d", len(result.CoChangeGroups))
	}
	if len(result.Diagnostics) == 0 {
		t.Error("expected a diagnostic for empty history")
	}
}

// --- Test 8: determinism ---------------------------------------------------

func TestDeterminism(t *testing.T) {
	provider := &InMemoryProvider{
		Commits: []Commit{
			commit("aaa", "alice", 1, "x.go", "y.go"),
			commit("bbb", "bob", 2, "y.go", "z.go"),
			commit("ccc", "carol", 3, "x.go", "z.go"),
			commit("ddd", "alice", 4, "x.go", "y.go"),
		},
	}

	cfg := Config{Now: refTime, MinCoCommits: 2}
	a := New(provider, cfg)

	first, err := a.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 10; i++ {
		got, err := a.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		// ChurnScores
		if len(got.ChurnScores) != len(first.ChurnScores) {
			t.Fatalf("run %d: churn count differs: %d vs %d", i, len(got.ChurnScores), len(first.ChurnScores))
		}
		for j := range got.ChurnScores {
			if got.ChurnScores[j] != first.ChurnScores[j] {
				t.Errorf("run %d churn[%d] differs: %+v vs %+v", i, j, got.ChurnScores[j], first.ChurnScores[j])
			}
		}
		// BusFactors
		if len(got.BusFactors) != len(first.BusFactors) {
			t.Fatalf("run %d: bus-factor count differs", i)
		}
		for j := range got.BusFactors {
			if got.BusFactors[j].Path != first.BusFactors[j].Path ||
				got.BusFactors[j].UniqueAuthors != first.BusFactors[j].UniqueAuthors ||
				got.BusFactors[j].Risky != first.BusFactors[j].Risky {
				t.Errorf("run %d bus[%d] differs", i, j)
			}
		}
		// CoChangeGroups
		if len(got.CoChangeGroups) != len(first.CoChangeGroups) {
			t.Fatalf("run %d: co-change count differs", i)
		}
		for j := range got.CoChangeGroups {
			if got.CoChangeGroups[j].CoCommits != first.CoChangeGroups[j].CoCommits {
				t.Errorf("run %d cochange[%d] co-commits differ", i, j)
			}
		}
	}
}

// --- Test 9: registry integration ------------------------------------------

func TestRegistryIntegration(t *testing.T) {
	provider := &InMemoryProvider{}
	a := New(provider, Config{})
	if a.Name() != "git-history" {
		t.Errorf("name: want git-history, got %s", a.Name())
	}
}

// --- Test 10: bus-factor custom threshold ----------------------------------

func TestBusFactorCustomThreshold(t *testing.T) {
	provider := &InMemoryProvider{
		Commits: []Commit{
			commit("aaa", "alice", 1, "team.go"),
			commit("bbb", "bob", 2, "team.go"),
		},
	}
	// Threshold=2 means <=2 unique authors is risky.
	a := New(provider, Config{Now: refTime, BusFactorThreshold: 2})
	result, err := a.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(result.BusFactors) != 1 {
		t.Fatalf("want 1 bus-factor entry, got %d", len(result.BusFactors))
	}
	bf := result.BusFactors[0]
	if bf.UniqueAuthors != 2 {
		t.Errorf("unique authors: want 2, got %d", bf.UniqueAuthors)
	}
	if !bf.Risky {
		t.Error("2 authors with threshold=2 should be risky")
	}
}

// --- Test 11: dual-constraint window (both active) -------------------------

func TestDualConstraintWindow(t *testing.T) {
	// 5 commits spanning 10 days. MaxCommits=3, MaxAge=7 days.
	// MaxAge cuts off commits older than 7 days (only 3 survive anyway).
	// But MaxCommits=3 would keep 3 — the tighter constraint matters.
	provider := &InMemoryProvider{
		Commits: []Commit{
			commit("a", "alice", 1, "f1.go"),
			commit("b", "bob", 3, "f2.go"),
			commit("c", "carol", 5, "f3.go"),
			commit("d", "dave", 8, "f4.go"),  // older than 7 days
			commit("e", "eve", 10, "f5.go"),  // older than 7 days
		},
	}
	a := New(provider, Config{
		Now:        refTime,
		MaxCommits: 3,
		MaxAge:     7 * 24 * time.Hour,
	})
	result, err := a.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Age filter removes d,e (>7 days old). That leaves a,b,c = 3 commits.
	// MaxCommits=3 doesn't cut further. Result: 3 files.
	if len(result.ChurnScores) != 3 {
		t.Fatalf("want 3 churn scores, got %d", len(result.ChurnScores))
	}
}
