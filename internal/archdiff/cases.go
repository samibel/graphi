package archdiff

import (
	"context"

	"github.com/samibel/graphi/surfaces/client"
)

// UseCase is one invocation the harness records and later compares.
//
// Invoke receives the Client rather than any concrete type on purpose: that is
// the seam both the compatibility facade and the future application-backed
// client satisfy, so the same table serves the whole migration.
type UseCase struct {
	// ID is the stable key in the recorded baseline. Never reuse an ID for a
	// different invocation — the baseline is keyed by it.
	ID string
	// Context is the bounded context that will own this use case.
	Context string
	// Invoke performs the call. Returning the error unchanged matters: a typed
	// refusal is a recorded contract, not a failure of the harness.
	Invoke func(ctx context.Context, c client.Client, env *Env) ([]byte, error)
}

// Bounded contexts, matching the migration matrix.
const (
	ContextGraphRead  = "graphread"
	ContextCodeChange = "codechange"
	ContextReview     = "review"
	ContextKnowledge  = "knowledge"
	ContextOperations = "operations"
)

// Cases is the recorded use-case table.
//
// Coverage is deliberately broad rather than deep: every bounded context and
// every refusal path, one representative invocation each. Depth already exists —
// the characterization suite pins the exact bytes of the twelve stable ops. What
// no existing suite covers is the long tail (edit previews, review analysis,
// knowledge, the fail-closed paths), and that tail is precisely what a migration
// silently breaks.
//
// Mutating operations are represented by their preview / dry-run form. An
// applied mutation would change the store mid-run and make every later case in
// the same environment depend on ordering, which would trade a reproducible
// baseline for one extra case.
func Cases() []UseCase {
	return []UseCase{
		// ── Graph Read ──────────────────────────────────────────────────────
		{ID: "graphread/query.definition", Context: ContextGraphRead,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.Query(ctx, "definition", env.HelloID, 0)
			}},
		{ID: "graphread/query.callers", Context: ContextGraphRead,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.Query(ctx, "callers", env.HelloID, 0)
			}},
		{ID: "graphread/query.callees", Context: ContextGraphRead,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.Query(ctx, "callees", env.ChainAID, 0)
			}},
		{ID: "graphread/query.references", Context: ContextGraphRead,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.Query(ctx, "references", env.HelloID, 0)
			}},
		{ID: "graphread/query.neighborhood", Context: ContextGraphRead,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.Query(ctx, "neighborhood", env.ChainAID, 2)
			}},
		{ID: "graphread/query.unknown-symbol", Context: ContextGraphRead,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				// The miss path is a contract too: a query for a symbol that does
				// not exist must keep answering the same way, not start erroring.
				return c.Query(ctx, "definition", "does-not-exist", 0)
			}},
		{ID: "graphread/search", Context: ContextGraphRead,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.Search(ctx, "Hello", 20)
			}},
		{ID: "graphread/semantic-search.unconfigured", Context: ContextGraphRead,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				// With no embedder this must stay a TYPED graceful skip with no
				// error and zero network — the exact contract a refactor is most
				// likely to collapse into a plain sentinel.
				return c.SemanticSearch(ctx, "greeting", 10)
			}},
		{ID: "graphread/search-ast", Context: ContextGraphRead,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.SearchAST(ctx, `{"kind":"function"}`, 20)
			}},
		{ID: "graphread/find-clones", Context: ContextGraphRead,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.FindClones(ctx, "")
			}},
		{ID: "graphread/compound", Context: ContextGraphRead,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.Compound(ctx, "SEED "+env.ChainAID+"\nHOP out calls\n")
			}},
		{ID: "graphread/analyze.impact", Context: ContextGraphRead,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.Analyze(ctx, client.AnalyzeParams{Name: "impact", Symbol: env.HelloID})
			}},
		{ID: "graphread/analyze.unknown", Context: ContextGraphRead,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.Analyze(ctx, client.AnalyzeParams{Name: "no-such-analyzer"})
			}},
		{ID: "graphread/diagnose", Context: ContextGraphRead,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.Diagnose(ctx, nil, client.DiagnoseOptions{All: true, JSON: true, Root: env.Root})
			}},
		{ID: "graphread/explain-symbol", Context: ContextGraphRead,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.ExplainSymbol(ctx, env.HelloID, 20)
			}},
		{ID: "graphread/related-files", Context: ContextGraphRead,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.RelatedFiles(ctx, env.ChainAID, "both", 10)
			}},
		{ID: "graphread/change-risk", Context: ContextGraphRead,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.ChangeRisk(ctx, env.HelloID, "", 20)
			}},
		{ID: "graphread/trust-report", Context: ContextGraphRead,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				payload, _, _, err := c.TrustReport(ctx, client.TrustReportOptions{Root: env.Root})
				return payload, err
			}},

		// ── Code Change (preview / dry-run only) ────────────────────────────
		{ID: "codechange/refactor-preview.rename", Context: ContextCodeChange,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.RefactorPreview(ctx, client.RefactorRequest{
					Kind: "rename", TargetSymbol: env.HelloID, OldName: "Hello", NewName: "Greetings",
				})
			}},
		{ID: "codechange/refactor-preview.unsupported-kind", Context: ContextCodeChange,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				// extract/move fail closed by design (SAFE-01); freezing that
				// refusal keeps a refactor from accidentally implementing it.
				return c.RefactorPreview(ctx, client.RefactorRequest{
					Kind: "extract", TargetSymbol: env.HelloID,
				})
			}},
		{ID: "codechange/inline.dry-run", Context: ContextCodeChange,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.Inline(ctx, client.InlineRequest{TargetSymbol: env.HelloID, DryRun: true})
			}},
		{ID: "codechange/safe-delete.dry-run", Context: ContextCodeChange,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.SafeDelete(ctx, client.SafeDeleteRequest{TargetSymbol: env.HelloID, DryRun: true})
			}},
		{ID: "codechange/undo.unknown-token", Context: ContextCodeChange,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.Undo(ctx, "no-such-undo-token", "archdiff")
			}},

		// ── Review & Forge (offline mock forge, dry-run publish) ────────────
		{ID: "review/list-prs", Context: ContextReview,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.ListPRs(ctx)
			}},
		{ID: "review/triage-prs", Context: ContextReview,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.TriagePRs(ctx)
			}},
		{ID: "review/conflicts-prs", Context: ContextReview,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.ConflictsPRs(ctx)
			}},
		{ID: "review/suggest-reviewers", Context: ContextReview,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.SuggestReviewers(ctx, "sample.go")
			}},
		{ID: "review/critique-review", Context: ContextReview,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.CritiqueReview(ctx, 1, "sample.go", "")
			}},
		{ID: "review/compare-branches.unwired", Context: ContextReview,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				// No branch-state materializer is wired, so this records the
				// fail-closed contract rather than a comparison result.
				return c.CompareBranches(ctx, "main", "feature")
			}},
		{ID: "review/pr-comment.dry-run", Context: ContextReview,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				// Publish=false: rendering and gating only. This case exists to
				// freeze that the dry-run path performs no egress.
				return c.PrComment(ctx, client.PrCommentRequest{
					PR: "1", Diff: "sample.go", Provenance: "summary", Publish: false,
				})
			}},

		// ── Knowledge ───────────────────────────────────────────────────────
		{ID: "knowledge/memory.list-empty", Context: ContextKnowledge,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.Memory(ctx, client.MemoryRequest{Op: "list", Limit: 10})
			}},
		{ID: "knowledge/memory.export-path-rejected", Context: ContextKnowledge,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				// SAFE-01: a server-side export path must stay refused. This is a
				// safety contract, and a refactor that "fixes" it is a regression.
				return c.Memory(ctx, client.MemoryRequest{Op: "export", ExportToPath: "/tmp/exfiltrate.json"})
			}},
		{ID: "knowledge/distill", Context: ContextKnowledge,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.Distill(ctx, client.DistillRequest{
					SessionID: "archdiff-session",
					Turns: []client.Turn{{
						ID: "t1", Prompt: "rename Hello", FilesIn: []string{"sample.go"}, FilesOut: []string{"sample.go"},
					}},
					Decisions: []string{"keep the exported name"},
					Risks:     []string{"callers outside the fixture"},
				})
			}},
		{ID: "knowledge/skillgen", Context: ContextKnowledge,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.SkillGen(ctx, client.SkillGenRequest{
					Name: "archdiff-skill", Trigger: "on demand", Description: "recorded baseline skill",
					Steps: []client.SkillStep{{Name: "look", Action: "query"}},
				})
			}},
		{ID: "knowledge/brief", Context: ContextKnowledge,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				// Brief returns two payloads; the JSON one carries the contract.
				payload, _, err := c.Brief(ctx, "Hello")
				return payload, err
			}},

		// ── Operations ──────────────────────────────────────────────────────
		{ID: "operations/savings", Context: ContextOperations,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.Savings(ctx)
			}},
	}
}

// UnwiredCases records the fail-closed contract of a client with nothing
// optional attached. Every one of these must refuse with its typed sentinel: a
// capability that is not wired must never report success, and the PRD calls out
// "no noop implementation that reports a missing capability as a success" as a
// rule the refactor has to keep.
func UnwiredCases() []UseCase {
	return []UseCase{
		{ID: "unwired/analyze", Context: ContextGraphRead,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.Analyze(ctx, client.AnalyzeParams{Name: "impact", Symbol: env.HelloID})
			}},
		// Diagnose is deliberately absent from this table. ErrDiagnosticUnavailable
		// is a TRANSPORT gap, not an optional-service gap: the in-process client
		// always has a diagnostic reader, so a bare Direct still answers. The
		// remote clients are where that sentinel fires, and this harness drives
		// the in-process seam. Recording a "fail-closed" expectation here would
		// therefore freeze a fact that is not true of this implementation.
		{ID: "unwired/refactor-preview", Context: ContextCodeChange,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.RefactorPreview(ctx, client.RefactorRequest{Kind: "rename", TargetSymbol: env.HelloID})
			}},
		{ID: "unwired/refactor", Context: ContextCodeChange,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.Refactor(ctx, client.RefactorRequest{Kind: "rename", TargetSymbol: env.HelloID}, "archdiff")
			}},
		{ID: "unwired/inline", Context: ContextCodeChange,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.Inline(ctx, client.InlineRequest{TargetSymbol: env.HelloID, DryRun: true})
			}},
		{ID: "unwired/safe-delete", Context: ContextCodeChange,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.SafeDelete(ctx, client.SafeDeleteRequest{TargetSymbol: env.HelloID, DryRun: true})
			}},
		{ID: "unwired/undo", Context: ContextCodeChange,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.Undo(ctx, "token", "archdiff")
			}},
		{ID: "unwired/list-prs", Context: ContextReview,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.ListPRs(ctx)
			}},
		{ID: "unwired/triage-prs", Context: ContextReview,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.TriagePRs(ctx)
			}},
		{ID: "unwired/conflicts-prs", Context: ContextReview,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.ConflictsPRs(ctx)
			}},
		{ID: "unwired/pr-comment", Context: ContextReview,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.PrComment(ctx, client.PrCommentRequest{PR: "1", Diff: "sample.go"})
			}},
		{ID: "unwired/compare-branches", Context: ContextReview,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.CompareBranches(ctx, "main", "feature")
			}},
		{ID: "unwired/memory", Context: ContextKnowledge,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.Memory(ctx, client.MemoryRequest{Op: "list"})
			}},
		{ID: "unwired/distill", Context: ContextKnowledge,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.Distill(ctx, client.DistillRequest{SessionID: "s"})
			}},
		{ID: "unwired/skillgen", Context: ContextKnowledge,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.SkillGen(ctx, client.SkillGenRequest{Name: "s"})
			}},
		{ID: "unwired/savings", Context: ContextOperations,
			Invoke: func(ctx context.Context, c client.Client, env *Env) ([]byte, error) {
				return c.Savings(ctx)
			}},
	}
}
