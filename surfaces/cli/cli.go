// Package cli is the command-line surface over the shared surface client.
//
// Layering: cli is a surface. It imports surfaces/client and holds NO query,
// traversal, ordering, or serialization logic of its own. It parses arguments,
// calls client.Client.Query/Search, and prints the canonical serialized bytes.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/samibel/graphi/engine/ledger"
	"github.com/samibel/graphi/engine/price"
	"github.com/samibel/graphi/surfaces/client"
)

// Run executes one CLI structural-query invocation against the shared client
// and writes the canonical serialized result to out. args are the arguments
// AFTER the surface selector (i.e. the subcommand and its flags).
//
// Usage:
//
//	<op> -symbol <id> [-depth N]
//
// where <op> is one of callers|callees|references|definition|neighborhood.
func Run(ctx context.Context, c client.Client, args []string, out, errOut io.Writer) error {
	if len(args) < 1 {
		fmt.Fprintf(errOut, "usage: <operation> -symbol <id> [-depth N]\n")
		return fmt.Errorf("cli: missing operation")
	}
	op := args[0]

	fs := flag.NewFlagSet(op, flag.ContinueOnError)
	fs.SetOutput(errOut)
	symbol := fs.String("symbol", "", "symbol (node) id to query")
	depth := fs.Int("depth", 1, "neighborhood hop depth (clamped to the documented max; ignored by other operations)")
	if err := fs.Parse(args[1:]); err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if *symbol == "" {
		fmt.Fprintln(errOut, "cli: -symbol is required")
		return fmt.Errorf("cli: -symbol is required")
	}

	b, err := c.Query(ctx, op, *symbol, *depth)
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

// cliActor is the default actor recorded for edits initiated via the CLI surface
// when the caller supplies none (Scope decision 6: actor is per-surface, recorded,
// excluded from the AC-4 parity comparable subset).
const cliActor = "cli"

// refactorFlags parses the shared refactor flags into a RefactorRequest. Both the
// preview and apply subcommands use the SAME parsing so the two surfaces (and the
// two CLI verbs) construct identical RefactorOps (parity by construction).
func refactorFlags(name string, args []string, errOut io.Writer) (client.RefactorRequest, string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(errOut)
	kind := fs.String("kind", "", "refactor kind: rename|signature_change (extract|move are accepted but currently perform the same OldName→NewName rewrite as rename; -destination-file is not yet honored)")
	target := fs.String("target", "", "resolved node id of the symbol to refactor")
	oldName := fs.String("old-name", "", "current spelling of the symbol")
	newName := fs.String("new-name", "", "replacement spelling")
	dest := fs.String("destination-file", "", "destination file (move only)")
	actor := fs.String("actor", cliActor, "request identity recorded on the change record")
	if err := fs.Parse(args); err != nil {
		return client.RefactorRequest{}, "", fmt.Errorf("cli: %w", err)
	}
	if *kind == "" || *target == "" || *oldName == "" || *newName == "" {
		fmt.Fprintln(errOut, "cli: -kind, -target, -old-name and -new-name are required")
		return client.RefactorRequest{}, "", fmt.Errorf("cli: missing required refactor flags")
	}
	return client.RefactorRequest{
		Kind:            *kind,
		TargetSymbol:    *target,
		OldName:         *oldName,
		NewName:         *newName,
		DestinationFile: *dest,
	}, *actor, nil
}

// RunRefactorPreview executes a refactor PREVIEW against the shared client and
// writes the canonical EP-004 impact set (blast radius + planned ops) WITHOUT
// mutating (AC-1). The CLI holds no engine logic.
//
// Usage:
//
//	refactor-preview -kind rename -target <id> -old-name X -new-name Y
func RunRefactorPreview(ctx context.Context, c client.Client, args []string, out, errOut io.Writer) error {
	req, _, err := refactorFlags("refactor-preview", args, errOut)
	if err != nil {
		return err
	}
	b, err := c.RefactorPreview(ctx, req)
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

// RunRefactor commits a refactor through the shared client and writes the
// canonical auditable change record. The actor defaults to "cli".
//
// Usage:
//
//	refactor -kind rename -target <id> -old-name X -new-name Y [-actor who]
func RunRefactor(ctx context.Context, c client.Client, args []string, out, errOut io.Writer) error {
	req, actor, err := refactorFlags("refactor", args, errOut)
	if err != nil {
		return err
	}
	b, err := c.Refactor(ctx, req, actor)
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

// RunUndo reverses an applied edit by its undo token and writes the canonical
// reversal change record.
//
// Usage:
//
//	undo -token <undo-token> [-actor who]
func RunUndo(ctx context.Context, c client.Client, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("undo", flag.ContinueOnError)
	fs.SetOutput(errOut)
	token := fs.String("token", "", "the undo token returned by a prior refactor")
	actor := fs.String("actor", cliActor, "request identity recorded on the reversal record")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if *token == "" {
		fmt.Fprintln(errOut, "cli: -token is required")
		return fmt.Errorf("cli: -token is required")
	}
	b, err := c.Undo(ctx, *token, *actor)
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

// RunSearch executes one CLI search invocation against the shared client and
// writes the canonical serialized result to out.
//
// Usage:
//
//	graphi search [-limit N] [-semantic] <query>
//
// With -semantic it runs the OPTIONAL semantic search (SW-059); when no embedder
// is configured the engine returns the typed graceful-skip "unavailable" response
// (no error, no network). Without it, the always-available lexical search runs.
func RunSearch(ctx context.Context, c client.Client, args []string, out, errOut io.Writer) error {
	limit := 100
	semantic := false
	var queryArgs []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-limit" && i+1 < len(args):
			// Use fmt.Sscanf to avoid strconv import.
			var v int
			if _, err := fmt.Sscanf(args[i+1], "%d", &v); err != nil {
				fmt.Fprintf(errOut, "cli: invalid -limit value %q\n", args[i+1])
				return fmt.Errorf("cli: invalid -limit")
			}
			limit = v
			i++
		case args[i] == "-semantic":
			semantic = true
		default:
			queryArgs = append(queryArgs, args[i])
		}
	}
	if len(queryArgs) == 0 {
		fmt.Fprintln(errOut, "usage: search [-limit N] [-semantic] <query>")
		return fmt.Errorf("cli: missing query")
	}
	query := queryArgs[0]

	var b []byte
	var err error
	if semantic {
		b, err = c.SemanticSearch(ctx, query, limit)
	} else {
		b, err = c.Search(ctx, query, limit)
	}
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

// RunSearchAST runs the structural AST pattern query (SW-082 / SW-085). It mirrors
// RunSearch's flag conventions — an optional `-limit N` plus the pattern as the
// positional argument — and writes the canonical query.Result bytes from the shared
// client (byte-identical to the MCP and HTTP surfaces). The pattern is a JSON
// AstPattern, e.g. graphi search-ast '{"kind":"function","name":{"regex":"^handle_"}}'.
func RunSearchAST(ctx context.Context, c client.Client, args []string, out, errOut io.Writer) error {
	limit := 100
	var rest []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-limit" && i+1 < len(args):
			var v int
			if _, err := fmt.Sscanf(args[i+1], "%d", &v); err != nil {
				fmt.Fprintf(errOut, "cli: invalid -limit value %q\n", args[i+1])
				return fmt.Errorf("cli: invalid -limit")
			}
			limit = v
			i++
		default:
			rest = append(rest, args[i])
		}
	}
	if len(rest) == 0 {
		fmt.Fprintln(errOut, "usage: search-ast [-limit N] <json-pattern>")
		return fmt.Errorf("cli: missing pattern")
	}
	b, err := c.SearchAST(ctx, rest[0], limit)
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

// RunFindClones runs the clone-detection query (SW-083 / SW-085). The optional
// positional argument is a JSON CloneConfig (empty ⇒ engine defaults); it writes
// the canonical query.CloneResult bytes from the shared client (byte-identical to
// the MCP and HTTP surfaces). E.g. graphi find-clones '{"threshold":0.9}'.
func RunFindClones(ctx context.Context, c client.Client, args []string, out, errOut io.Writer) error {
	var config string
	for i := 0; i < len(args); i++ {
		if args[i] != "" {
			config = args[i]
			break
		}
	}
	b, err := c.FindClones(ctx, config)
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

// RunDiagnose runs the engine diagnostics (SW-091 / SW-094) and writes the
// canonical diagnostic.Result bytes from the shared client — byte-identical to
// the MCP/HTTP/daemon surfaces. Positional args select analyzer kinds; none ⇒ all
// built-ins. E.g. graphi diagnose            (all) / graphi diagnose dead_symbol.
func RunDiagnose(ctx context.Context, c client.Client, args []string, out, errOut io.Writer) error {
	var kinds []string
	for _, a := range args {
		if a != "" {
			kinds = append(kinds, a)
		}
	}
	b, err := c.Diagnose(ctx, kinds)
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

// RunInline runs the inline refactor (SW-092 / SW-094): `graphi inline [-dry-run]
// <target-symbol>`. It writes the canonical InlineResult bytes from the shared
// client (byte-identical across surfaces). A blocked/unavailable outcome is a
// typed result, not an error.
func RunInline(ctx context.Context, c client.Client, args []string, out, errOut io.Writer) error {
	req, err := mutatingFlags("inline", args, errOut)
	if err != nil {
		return err
	}
	b, err := c.Inline(ctx, client.InlineRequest{TargetSymbol: req.target, DryRun: req.dryRun})
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

// RunSafeDelete runs the safe-delete refactor (SW-093 / SW-094): `graphi
// safe-delete [-dry-run] <target-symbol>`. Writes the canonical SafeDeleteResult
// bytes from the shared client; a blocked report is a typed result, not an error.
func RunSafeDelete(ctx context.Context, c client.Client, args []string, out, errOut io.Writer) error {
	req, err := mutatingFlags("safe-delete", args, errOut)
	if err != nil {
		return err
	}
	b, err := c.SafeDelete(ctx, client.SafeDeleteRequest{TargetSymbol: req.target, DryRun: req.dryRun})
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

// mutatingReq is the decoded form of the shared inline/safe-delete flag set.
type mutatingReq struct {
	target string
	dryRun bool
}

// mutatingFlags decodes `[-dry-run] <target-symbol>` for the inline and
// safe-delete commands — identical decoding for both so their surface behavior
// can never diverge. The structured decode error wording is stable for parity.
func mutatingFlags(cmd string, args []string, errOut io.Writer) (mutatingReq, error) {
	var r mutatingReq
	var rest []string
	for _, a := range args {
		switch a {
		case "-dry-run", "--dry-run":
			r.dryRun = true
		default:
			if a != "" {
				rest = append(rest, a)
			}
		}
	}
	if len(rest) == 0 {
		fmt.Fprintf(errOut, "usage: %s [-dry-run] <target-symbol>\n", cmd)
		return r, fmt.Errorf("cli: %s: missing target symbol", cmd)
	}
	r.target = rest[0]
	return r, nil
}

// RunSetupEmbedder is the opt-in `graphi setup-embedder` command (SW-059). It
// prints the explicit, copy-pasteable config a user sets to enable the OPTIONAL
// semantic search — there is NO hidden default: semantic search stays OFF until
// the user exports GRAPHI_EMBEDDER. It is OFFLINE (prints instructions only;
// constructs/dials nothing) so it never violates the zero-egress default path.
//
// Usage:
//
//	graphi setup-embedder [<selector>]
//
// where <selector> is e.g. "ollama" or "ollama:127.0.0.1:11434" (loopback-only,
// opt-in) or "onnx:<model>" (requires the embed_onnx build).
func RunSetupEmbedder(ctx context.Context, args []string, out, errOut io.Writer) error {
	_ = ctx
	selector := ""
	if len(args) > 0 {
		selector = args[0]
	}
	if selector == "" {
		fmt.Fprintln(out, "graphi semantic search is OPTIONAL and OFF by default.")
		fmt.Fprintln(out, "To enable it, choose an embedder and export GRAPHI_EMBEDDER:")
		fmt.Fprintln(out, "  Ollama (loopback, opt-in):  export GRAPHI_EMBEDDER=ollama")
		fmt.Fprintln(out, "  Ollama (explicit host):     export GRAPHI_EMBEDDER=ollama:127.0.0.1:11434")
		fmt.Fprintln(out, "  ONNX (build with -tags embed_onnx): export GRAPHI_EMBEDDER=onnx:/path/to/model.onnx")
		fmt.Fprintln(out, "Then re-index with embeddings:  graphi index --semantic")
		fmt.Fprintln(out, "Run a semantic query:           graphi search -semantic \"<query>\"")
		return nil
	}
	fmt.Fprintf(out, "To enable graphi semantic search with %q, export:\n", selector)
	fmt.Fprintf(out, "  export GRAPHI_EMBEDDER=%s\n", selector)
	fmt.Fprintln(out, "Then re-index with embeddings:  graphi index --semantic")
	fmt.Fprintln(out, "Note: network embedders (Ollama) are loopback-only and validated fail-closed at construction.")
	return nil
}

// RunListPRs runs the SW-105 read-only forge PR-enumeration through the shared
// client and writes the canonical serialized forge.PRList bytes (byte-identical
// to the MCP/HTTP surfaces). Like every CLI surface it holds NO engine logic — it
// returns forge-sourced metadata only, performs no scoring.
//
// Usage:
//
//	list-prs
func RunListPRs(ctx context.Context, c client.Client, out, errOut io.Writer) error {
	b, err := c.ListPRs(ctx)
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

// RunTriagePRs runs the SW-105 single-pass graph-derived PR triage ranking through
// the shared client and writes the canonical serialized TriageReport bytes
// (byte-identical across surfaces). The forge enumeration is the only egress; the
// ranking is a zero-egress pass over the local graph.
//
// Usage:
//
//	triage-prs
func RunTriagePRs(ctx context.Context, c client.Client, out, errOut io.Writer) error {
	b, err := c.TriagePRs(ctx)
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

// RunConflictsPRs runs the SW-106 inter-PR conflict detection through the shared
// client and writes the canonical serialized ConflictReport bytes (byte-identical
// across surfaces). The forge enumeration is the only egress; the conflict
// detection is a zero-egress pass over the local graph.
//
// Usage:
//
//	conflicts-prs
func RunConflictsPRs(ctx context.Context, c client.Client, out, errOut io.Writer) error {
	b, err := c.ConflictsPRs(ctx)
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

// RunSuggestReviewers runs the SW-107 reviewer recommender through the shared
// client and writes the canonical serialized ReviewerReport bytes (byte-identical
// across surfaces). diff is the local-first PR diff / line-oriented ref string; the
// ranking is a zero-egress pass over the local graph + git history.
//
// Usage:
//
//	suggest-reviewers [-diff <unified-diff|refs>]
func RunSuggestReviewers(ctx context.Context, c client.Client, diff string, out, errOut io.Writer) error {
	b, err := c.SuggestReviewers(ctx, diff)
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

// RunCompareBranches runs the SW-107 graph-level branch comparator through the
// shared client and writes the canonical serialized BranchDiffReport bytes
// (byte-identical across surfaces). The base/head graph states are materialized
// above the surface boundary; the engine diff is a zero-egress pass keyed by
// canonical NodeId.
//
// Usage:
//
//	compare-branches -base <ref> -head <ref>
func RunCompareBranches(ctx context.Context, c client.Client, baseRef, headRef string, out, errOut io.Writer) error {
	b, err := c.CompareBranches(ctx, baseRef, headRef)
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

// RunCritiqueReview runs the SW-108 review critique through the shared client and
// writes the canonical serialized CritiqueReport bytes (byte-identical across
// surfaces). The EXISTING review is supplied inline via -review / -review-path (read
// once at the surface; no engine file I/O) OR fetched from the forge for -pr; the
// touched set comes from -diff / -diff-path. The critique itself is a zero-egress
// pass over the local graph; the only permitted egress is the surface review fetch.
//
// Usage:
//
//	critique-review -diff <unified-diff|refs> [-pr N] [-review <json>|-review-path <file>]
func RunCritiqueReview(ctx context.Context, c client.Client, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("critique-review", flag.ContinueOnError)
	fs.SetOutput(errOut)
	pr := fs.Int("pr", 0, "PR number to fetch the existing review for (when no inline review is supplied)")
	diff := fs.String("diff", "", "the PR's touched set: inline unified-diff or simple ref string")
	diffPath := fs.String("diff-path", "", "path to a LOCAL diff file for the touched set (read once; no remote fetch)")
	review := fs.String("review", "", "inline existing-review JSON ({verdict, comments:[{id,path,line,symbol,claim_targets}]})")
	reviewPath := fs.String("review-path", "", "path to a LOCAL existing-review JSON file (read once; no remote fetch)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("cli: %w", err)
	}

	diffPayload := *diff
	if *diffPath != "" {
		b, rerr := os.ReadFile(*diffPath)
		if rerr != nil {
			fmt.Fprintf(errOut, "cli: read diff file: %v\n", rerr)
			return fmt.Errorf("cli: read diff file: %w", rerr)
		}
		diffPayload = string(b)
	}
	reviewPayload := *review
	if *reviewPath != "" {
		b, rerr := os.ReadFile(*reviewPath)
		if rerr != nil {
			fmt.Fprintf(errOut, "cli: read review file: %v\n", rerr)
			return fmt.Errorf("cli: read review file: %w", rerr)
		}
		reviewPayload = string(b)
	}

	b, err := c.CritiqueReview(ctx, *pr, diffPayload, reviewPayload)
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

// RunSavings prints the savings-ledger readout (SW-020): the headline
// "Saved $X this session" line plus per-call and cumulative USD figures,
// followed by the canonical structured readout JSON (identical to the MCP tool
// result for the same ledger state, preserving MCP<->CLI parity).
//
// Usage:
//
//	savings
func RunSavings(ctx context.Context, c client.Client, out, errOut io.Writer) error {
	b, err := c.Savings(ctx)
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	var r ledger.Readout
	if err := json.Unmarshal(b, &r); err != nil {
		return fmt.Errorf("cli: decode readout: %w", err)
	}
	// Headline + figures (formatted via the shared micro-USD formatter).
	fmt.Fprintf(out, "Saved %s this session\n", price.FormatUSD(r.SessionMicroUSD))
	fmt.Fprintf(out, "per-call: %s\n", price.FormatUSD(r.LastCallMicroUSD))
	fmt.Fprintf(out, "cumulative: %s\n", price.FormatUSD(r.CumulativeMicroUSD))
	if r.SessionCapped || r.LastCallCapped {
		fmt.Fprintln(out, "note: anti-gaming cap applied to one or more contributions")
	}
	// Canonical structured readout (parity with the MCP tool result).
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

// RunMemory executes a memory operation against the shared client and writes the
// canonical serialized MemoryResponse.
//
// Usage:
//
//	memory store -scope <scope> -notebook <nb> -payload <text> [-tags a,b]
//	memory recall -scope <scope> [-notebook <nb>]
//	memory forget -id <id>
func RunMemory(ctx context.Context, c client.Client, args []string, out, errOut io.Writer) error {
	if len(args) < 1 {
		fmt.Fprintln(errOut, "usage: memory store|recall|forget ...")
		return fmt.Errorf("cli: missing memory subcommand")
	}
	op := args[0]
	fs := flag.NewFlagSet("memory "+op, flag.ContinueOnError)
	fs.SetOutput(errOut)
	scope := fs.String("scope", "", "memory scope")
	notebook := fs.String("notebook", "", "memory notebook")
	tags := fs.String("tags", "", "comma-separated tags (store only)")
	payload := fs.String("payload", "", "memory payload (store only)")
	id := fs.String("id", "", "memory id (forget only)")
	if err := fs.Parse(args[1:]); err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	var tagList []string
	if *tags != "" {
		tagList = strings.Split(*tags, ",")
	}
	b, err := c.Memory(ctx, client.MemoryRequest{
		Op:       op,
		Scope:    *scope,
		Notebook: *notebook,
		Tags:     tagList,
		Payload:  *payload,
		ID:       *id,
	})
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

// RunDistill runs session distillation against the shared client and writes the
// canonical serialized DistillResponse.
//
// Usage:
//
//	distill -session <id> -decisions "d1,d2" -risks "r1" -questions "q1" -files "a.go,b.go"
func RunDistill(ctx context.Context, c client.Client, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("distill", flag.ContinueOnError)
	fs.SetOutput(errOut)
	session := fs.String("session", "", "session id")
	decisions := fs.String("decisions", "", "comma-separated decisions")
	risks := fs.String("risks", "", "comma-separated risks")
	questions := fs.String("questions", "", "comma-separated open questions")
	files := fs.String("files", "", "comma-separated file references")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if *session == "" {
		fmt.Fprintln(errOut, "cli: -session is required")
		return fmt.Errorf("cli: -session is required")
	}
	b, err := c.Distill(ctx, client.DistillRequest{
		SessionID:      *session,
		Decisions:      splitCSV(*decisions),
		Risks:          splitCSV(*risks),
		OpenQuestions:  splitCSV(*questions),
		FileReferences: splitCSV(*files),
	})
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

// RunSkillGen runs deterministic skill generation against the shared client and
// writes the canonical serialized SkillGenResponse.
//
// Usage:
//
//	skillgen -name <name> -trigger <trigger> -description <desc>
func RunSkillGen(ctx context.Context, c client.Client, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("skillgen", flag.ContinueOnError)
	fs.SetOutput(errOut)
	name := fs.String("name", "", "skill name")
	trigger := fs.String("trigger", "", "skill trigger")
	desc := fs.String("description", "", "skill description")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if *name == "" || *trigger == "" {
		fmt.Fprintln(errOut, "cli: -name and -trigger are required")
		return fmt.Errorf("cli: -name and -trigger are required")
	}
	b, err := c.SkillGen(ctx, client.SkillGenRequest{
		Name:        *name,
		Trigger:     *trigger,
		Description: *desc,
	})
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// RunAnalysis executes one CLI analyzer invocation against the shared client and
// writes the canonical serialized result to out (SW-022). The CLI holds no
// analysis logic of its own — it parses arguments, builds AnalyzeParams, calls
// client.Client.Analyze, and prints the canonical bytes (MCP<->CLI parity).
//
// Usage:
//
//	<name> -symbol <id> [-direction forward|reverse] [-max-nodes N]
//
// where <name> is a registered analyzer (e.g. impact).
func RunAnalysis(ctx context.Context, c client.Client, args []string, out, errOut io.Writer) error {
	if len(args) < 1 {
		fmt.Fprintf(errOut, "usage: <analyzer> -symbol <id> [-direction forward|reverse] [-max-nodes N]\n")
		return fmt.Errorf("cli: missing analyzer name")
	}
	name := args[0]
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(errOut)
	symbol := fs.String("symbol", "", "symbol (node) id to analyze")
	target := fs.String("target", "", "target symbol (node) id (call-chain endpoint)")
	concept := fs.String("concept", "", "concept term (concept resolver)")
	direction := fs.String("direction", "forward", "traversal direction for directional analyzers (forward|reverse)")
	maxNodes := fs.Int("max-nodes", 0, "output budget on reached nodes (0 = analyzer default)")
	// SW-039 pr-risk: local-first diff input. -diff is an inline unified-diff /
	// ref string; -diff-path reads a LOCAL file once at the surface (no engine
	// file I/O on the hot path, no remote fetch).
	diff := fs.String("diff", "", "pr-risk: inline unified-diff or simple ref string")
	diffPath := fs.String("diff-path", "", "pr-risk: path to a LOCAL diff file (read once; no remote fetch)")
	provenance := fs.String("provenance", "", "pr-risk: evidence redaction level (full|summary)")
	if err := fs.Parse(args[1:]); err != nil {
		return fmt.Errorf("cli: %w", err)
	}

	diffPayload := *diff
	if *diffPath != "" {
		b, rerr := os.ReadFile(*diffPath)
		if rerr != nil {
			fmt.Fprintf(errOut, "cli: read diff file: %v\n", rerr)
			return fmt.Errorf("cli: read diff file: %w", rerr)
		}
		diffPayload = string(b)
	}

	// The pr-risk scorer, the pr-signals detector, and the pr-questions generator
	// are diff-driven; the SW-104 EP-017 operations (communities, watcher-status,
	// notebook-ingest, taint-query) are whole-graph / status operations needing no
	// symbol; every other analyzer is symbol-driven.
	switch {
	case name == "pr-risk" || name == "pr-signals" || name == "pr-questions":
		if diffPayload == "" {
			fmt.Fprintf(errOut, "cli: -diff or -diff-path is required for %s\n", name)
			return fmt.Errorf("cli: -diff or -diff-path is required for %s", name)
		}
	case client.AnalyzerSymbolOptional(name):
		// no required positional argument
	case *symbol == "":
		fmt.Fprintln(errOut, "cli: -symbol is required")
		return fmt.Errorf("cli: -symbol is required")
	}
	b, err := c.Analyze(ctx, client.AnalyzeParams{
		Name:       name,
		Symbol:     *symbol,
		Target:     *target,
		Concept:    *concept,
		Direction:  *direction,
		MaxNodes:   *maxNodes,
		Diff:       diffPayload,
		Provenance: *provenance,
	})
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

// RunPrComment executes the SW-042 sticky PR-comment writer + optional
// risk-threshold merge gate through the shared client and writes the canonical
// serialized engine/review.PublishResult. Like every CLI surface it holds NO
// engine logic — it parses flags, reads a LOCAL diff once (no remote fetch), and
// calls client.Client.PrComment (MCP<->CLI parity).
//
// Usage:
//
//	pr-comment -diff <unified-diff> | -diff-path <file> [-pr ref]
//	  [-provenance summary|full] [-gate] [-gate-threshold N] [-publish]
//
// The default is an OFFLINE dry-run: it renders the deterministic body and
// evaluates the gate WITHOUT contacting any PR host. -publish upserts the sticky
// comment through the (currently mock) host boundary; the real host is wired by
// SW-043.
func RunPrComment(ctx context.Context, c client.Client, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("pr-comment", flag.ContinueOnError)
	fs.SetOutput(errOut)
	pr := fs.String("pr", "", "PR reference rendered in the comment header (e.g. owner/repo#42)")
	diff := fs.String("diff", "", "inline unified-diff or simple ref string")
	diffPath := fs.String("diff-path", "", "path to a LOCAL diff file (read once; no remote fetch)")
	provenance := fs.String("provenance", "summary", "evidence redaction level (summary|full); summary recommended for public comments")
	gate := fs.Bool("gate", false, "enable the optional risk-threshold merge gate")
	gateThreshold := fs.Int("gate-threshold", 700, "risk threshold in fixed-point units (1/1000) the worst region must EXCEED to BLOCK")
	publish := fs.Bool("publish", false, "upsert the sticky comment through the host (default: offline dry-run)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("cli: %w", err)
	}

	diffPayload := *diff
	if *diffPath != "" {
		b, rerr := os.ReadFile(*diffPath)
		if rerr != nil {
			fmt.Fprintf(errOut, "cli: read diff file: %v\n", rerr)
			return fmt.Errorf("cli: read diff file: %w", rerr)
		}
		diffPayload = string(b)
	}
	if diffPayload == "" {
		fmt.Fprintln(errOut, "cli: -diff or -diff-path is required for pr-comment")
		return fmt.Errorf("cli: -diff or -diff-path is required for pr-comment")
	}

	b, err := c.PrComment(ctx, client.PrCommentRequest{
		PR:            *pr,
		Diff:          diffPayload,
		Provenance:    *provenance,
		GateEnabled:   *gate,
		GateThreshold: *gateThreshold,
		Publish:       *publish,
	})
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}
