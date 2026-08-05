package main

// Adversarial pins for the query-strict Labs wrapper (PRD §28), written to
// PROVE a lie or leak. Two attacks landed and are fixed in query_strict.go;
// the rest are held as regression pins:
//
//   - Trailing positional arguments: the stdlib flag package stops parsing at
//     the first non-flag token, so `callers -symbol X stray -policy review`
//     silently DROPPED -policy (no preflight) and `... stray -min-tier
//     confirmed` silently dropped the filter (everything admitted). Fixed:
//     leftover positionals are an input error (exit 2), never a silent drop.
//   - Preflight/store split: with an explicit -db the trust preflight ran
//     against the auto-managed store of the cwd repository while the query ran
//     against the -db store — a PASS minted on one store certified a query
//     over another. Fixed: the preflight receives the same -db/-meta the
//     query session uses.
//
// Held attacks (fail-closed behavior confirmed): tier rewriting (provenance is
// verbatim), exclusion arithmetic at neighborhood depth 2, not-found/empty
// outcome passthrough, unknown/empty -policy, -min-tier garbage.

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/query"
)

// TestQueryStrict_TrailingArgsAreInputErrors — the silent-drop attack: a stray
// positional token makes the stdlib flag parser ignore every following flag.
// Both drops below would change the trust posture of the run (no preflight /
// no filter), so leftover positionals must be an input error with no output.
func TestQueryStrict_TrailingArgsAreInputErrors(t *testing.T) {
	repo, dbPath, helper := strictFixture(t)

	cases := [][]string{
		// Would silently drop -min-tier confirmed: the result would admit
		// every tier while the user asked for confirmed-only.
		{"callers", "-db", dbPath, "-symbol", helper, "stray", "-min-tier", "confirmed"},
		// Would silently drop -policy review: the query would run with no
		// preflight although the user asked for one.
		{"callers", "-db", dbPath, "-symbol", helper, "stray", "-policy", "review-v1"},
		// The explicit flag terminator is the same trick spelled differently.
		{"callers", "-db", dbPath, "-symbol", helper, "--", "-policy", "review-v1"},
	}
	for _, args := range cases {
		var out bytes.Buffer
		if code := runQueryStrictAt(repo, args, &out); code != 2 {
			t.Errorf("%v exit = %d, want 2 (a dropped flag is a silent trust downgrade)", args, code)
		}
		if out.Len() != 0 {
			t.Errorf("%v wrote output on input error:\n%s", args, out.String())
		}
	}
}

// TestQueryStrict_PreflightFollowsExplicitDB — the preflight-laundering
// attack: the cwd repository's auto-managed store is healthy (the exploratory
// preflight passes and the query runs, exit 0), but with -db pointing at a
// store no full pass ever certified the SAME preflight must judge THAT store
// — UNKNOWN, query blocked (exit 4) — never certify store A and query store B.
func TestQueryStrict_PreflightFollowsExplicitDB(t *testing.T) {
	repo, _, helper := strictFixture(t)

	// Baseline: preflight over the auto-managed store passes, query runs.
	code, env, raw := runStrict(t, repo, []string{"callers", "-symbol", helper, "-policy", "exploratory-v1"})
	if code != 0 {
		t.Fatalf("baseline preflight exit = %d, want 0 (fixture store is healthy)\n%s", code, raw)
	}
	if env.Trust.PreflightVerdict == "" {
		t.Fatalf("baseline envelope carries no preflight verdict:\n%s", raw)
	}

	// Attack: same repo, same policy, but the query is pointed at a fresh
	// store that was never certified by any pass.
	uncertified := filepath.Join(t.TempDir(), "uncertified.db")
	store, err := graphstore.OpenSQLite(uncertified)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code = runQueryStrictAt(repo, []string{"callers", "-db", uncertified, "-symbol", helper, "-policy", "exploratory-v1"}, &out)
	if code != 2 {
		t.Fatalf("exit = %d, want 2: the preflight must judge the -db store the query runs against, not the auto-managed one\n%s", code, out.String())
	}
	if out.Len() != 0 {
		t.Fatalf("blocked preflight still wrote a result:\n%s", out.String())
	}
}

// TestQueryStrict_PolicyValueErrors pins the two policy-flag error paths: an
// explicitly empty -policy must not silently mean "no preflight", and a name
// outside the built-in registry is an input error — both exit 2, no output.
func TestQueryStrict_PolicyValueErrors(t *testing.T) {
	repo, dbPath, helper := strictFixture(t)
	cases := [][]string{
		{"callers", "-db", dbPath, "-symbol", helper, "-policy", ""},
		{"callers", "-db", dbPath, "-symbol", helper, "-policy", "certainly-not-a-policy"},
	}
	for _, args := range cases {
		var out bytes.Buffer
		if code := runQueryStrictAt(repo, args, &out); code != 2 {
			t.Errorf("%v exit = %d, want 2", args, code)
		}
		if out.Len() != 0 {
			t.Errorf("%v wrote output on input error:\n%s", args, out.String())
		}
	}
}

// TestQueryStrict_NotFoundOutcomePassesThrough — an unresolved symbol must
// come through coherently: the canonical not_found outcome verbatim, zero
// exclusions, no fabricated limitation, non-nil empty lists, exit 0.
func TestQueryStrict_NotFoundOutcomePassesThrough(t *testing.T) {
	repo, dbPath, _ := strictFixture(t)
	code, env, raw := runStrict(t, repo, []string{"callers", "-db", dbPath, "-symbol", "does-not-exist", "-min-tier", "confirmed"})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (not-found is an outcome, not an error)\n%s", code, raw)
	}
	if env.Result.Outcome != query.OutcomeNotFound {
		t.Errorf("outcome = %q, want %q passed through verbatim", env.Result.Outcome, query.OutcomeNotFound)
	}
	if env.Filter.ExcludedEdges != 0 {
		t.Errorf("excluded = %d on a not-found result, want 0", env.Filter.ExcludedEdges)
	}
	if len(env.Limitations) != 0 {
		t.Errorf("not-found result fabricated a limitation: %v", env.Limitations)
	}
	if env.Result.Nodes == nil || env.Result.Edges == nil || env.Limitations == nil {
		t.Error("envelope lists must be non-nil even on not-found")
	}
}

// TestQueryStrict_NeighborhoodDepth2ExclusionArithmetic attacks the exclusion
// count on the multi-hop path: over a depth-2 neighborhood with mixed tiers,
// for every min-tier the excluded count must equal the baseline census of
// edges below the bar, the kept set must carry no edge below the bar with its
// tier verbatim (never rewritten), every surviving node must be justified, and
// the limitation must appear exactly when edges were excluded.
func TestQueryStrict_NeighborhoodDepth2ExclusionArithmetic(t *testing.T) {
	repo, dbPath, helper := strictFixture(t)

	code, base, raw := runStrict(t, repo, []string{"neighborhood", "-db", dbPath, "-symbol", helper, "-depth", "2"})
	if code != 0 {
		t.Fatalf("baseline exit = %d\n%s", code, raw)
	}
	if len(base.Result.Edges) == 0 {
		t.Fatalf("fixture produced no neighborhood edges:\n%s", raw)
	}
	baseTier := map[string]int{}
	for _, e := range base.Result.Edges {
		baseTier[string(e.Tier)]++
	}
	if baseTier["derived"] == 0 || baseTier["heuristic"] == 0 {
		t.Fatalf("fixture census %v lacks the tier mix the attack needs", baseTier)
	}

	admits := map[string][]string{
		"confirmed": {"confirmed"},
		"derived":   {"confirmed", "derived"},
		"heuristic": {"confirmed", "derived", "heuristic"},
	}
	for minTier, allowed := range admits {
		code, env, raw := runStrict(t, repo, []string{"neighborhood", "-db", dbPath, "-symbol", helper, "-depth", "2", "-min-tier", minTier})
		if code != 0 {
			t.Fatalf("%s run exit = %d\n%s", minTier, code, raw)
		}
		allowedSet := map[string]bool{}
		wantKept := 0
		for _, tier := range allowed {
			allowedSet[tier] = true
			wantKept += baseTier[tier]
		}
		wantExcluded := len(base.Result.Edges) - wantKept
		if env.Filter.ExcludedEdges != wantExcluded {
			t.Errorf("min-tier %s: excluded = %d, want %d (census %v)", minTier, env.Filter.ExcludedEdges, wantExcluded, baseTier)
		}
		if len(env.Result.Edges) != wantKept {
			t.Errorf("min-tier %s: kept %d edges, want %d", minTier, len(env.Result.Edges), wantKept)
		}
		for _, e := range env.Result.Edges {
			if !allowedSet[string(e.Tier)] {
				t.Errorf("min-tier %s: edge with tier %q survived the filter: %+v", minTier, e.Tier, e)
			}
		}
		used := map[string]bool{string(env.Result.Symbol): true}
		for _, e := range env.Result.Edges {
			used[string(e.From)] = true
			used[string(e.To)] = true
		}
		for _, n := range env.Result.Nodes {
			if !used[string(n.ID)] {
				t.Errorf("min-tier %s: unjustified node survived: %+v", minTier, n)
			}
		}
		if wantExcluded > 0 && len(env.Limitations) == 0 {
			t.Errorf("min-tier %s: %d edges excluded but no limitation present", minTier, wantExcluded)
		}
		if wantExcluded == 0 && len(env.Limitations) != 0 {
			t.Errorf("min-tier %s: nothing excluded but a limitation was fabricated: %v", minTier, env.Limitations)
		}
	}
}
