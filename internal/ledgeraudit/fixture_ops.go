package ledgeraudit

// FixtureBaselineVersion is the version-stamped baseline the audit pins AGAINST
// the code constant BaselineMethodVersion. It is a literal (not a reference to
// the code constant) on purpose: if the recompute method changes and the code
// constant is bumped without re-pinning this fixture, AssertBaselineVersion
// fails the build (AC: baseline version-stamp enforcement).
const FixtureBaselineVersion = "2026-06-20-v1"

// FrozenOps returns the frozen, deterministic workload the audit runs against in
// CI. It mixes under-cap and over-cap (per-op) operations across two sessions so
// the recompute-agreement, cap, and restart-integrity checks all exercise real
// paths. Bumping this workload is an intentional, reviewed change.
func FrozenOps() []Op {
	return []Op{
		{Model: "default", SessionID: "s1", Seq: 1, InputTokens: 100, OutputTokens: 50, BaselineInputTokens: 5000, BaselineOutputTokens: 2000},
		{Model: "default", SessionID: "s1", Seq: 2, InputTokens: 40, OutputTokens: 20, BaselineInputTokens: 400, BaselineOutputTokens: 100},
		{Model: "premium", SessionID: "s1", Seq: 3, InputTokens: 1000, OutputTokens: 500, BaselineInputTokens: 60000, BaselineOutputTokens: 30000},
		{Model: "default", SessionID: "s2", Seq: 1, InputTokens: 30, OutputTokens: 10, BaselineInputTokens: 300, BaselineOutputTokens: 80},
	}
}
