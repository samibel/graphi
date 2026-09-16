package retrieval

// The candidate MCP capture.
//
// docs/eval/retrieval/methodology.md records "Candidate MCP capture remains
// UNENFORCED until the release-composition slice owns the actual transport
// call". This file is that instrument. It drives the REAL MCP stdio surface —
// surfaces/mcp.Server.Serve, the same JSON-RPC read/dispatch/write loop the
// shipped server runs — over one `tools/call task_context` request per query,
// and preserves the exact response bytes the loop's encoder wrote, terminating
// newline included.
//
// What makes the bytes trustworthy is what is NOT here: no re-marshaling, no
// pretty printer, no envelope reconstruction and no extraction of the inner
// text. The writer's buffer is the artifact. ValidateCandidateBundle then
// refuses the three substitute shapes AC-2 names, and each refusal has a test
// that breaks a real capture to prove it bites.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"reflect"
	"strings"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/taskctx"
	taskcompact "github.com/samibel/graphi/engine/agenttools/taskctx/compact"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/query"
	engineretrieval "github.com/samibel/graphi/engine/retrieval"
	"github.com/samibel/graphi/engine/search"
	cltokenizer "github.com/samibel/graphi/internal/eval/tokenizer"
	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/mcp"
)

// CandidateCaptureVersion identifies the capture instrument. It travels into
// the run directory so a later change to how bytes are captured cannot be
// mistaken for the same measurement.
const CandidateCaptureVersion = "sw280-candidate-mcp-capture/4"

// candidateJSONRPCPrefix is the exact opening the stdio encoder produces for a
// response: encoding/json writes struct fields in declaration order, and
// surfaces/mcp declares jsonrpc, then id, then result.
//
// Requiring it is a cheap, decisive refusal of a re-marshaled envelope: a
// bundle round-tripped through map[string]any comes back with alphabetically
// ordered keys and cannot start this way.
const candidateJSONRPCPrefix = `{"jsonrpc":"2.0","id":`

// candidateRequestID is the id of the one request the capture sends, as it
// appears in the encoded JSON of both the request and the response.
const candidateRequestID = "1"

// CapturedCandidateBundle is one query's preserved task_context/2 response,
// together with the request that produced it. The request is recorded for
// provenance only; it is outside the payload boundary and enters no count.
type CapturedCandidateBundle struct {
	QueryID      string            `json:"query_id"`
	RequestBytes []byte            `json:"request_bytes"`
	Payload      PreservedPayload  `json:"payload"`
	FollowupRead *PreservedPayload `json:"followup_read,omitempty"`
	// RetrievalStrategy and RetrievalState are the observed engine facts that
	// prove this was the ready task_context/2 path and not the /1 fallback.
	RetrievalStrategy        string                    `json:"retrieval_strategy"`
	RetrievalState           string                    `json:"retrieval_state"`
	BundleSummary            string                    `json:"bundle_summary"`
	Qualification            *QualificationObservation `json:"qualification,omitempty"`
	QualificationQueryVector []float32                 `json:"qualification_query_vector,omitempty"`
	// OracleControls are constructed from the one-shot, pre-compact normal
	// contract.Result and carried only to the qualification staging writer.
	// They are never serialized under the normal capture artifact.
	OracleControls *OracleControls `json:"-"`
}

// ValidateCapturedTranscript preserves contract-1 captures unchanged and,
// when slice 2 exists, proves it is exactly the span slice 1 designated from
// the pinned repository. A reader cannot choose or fabricate the follow-up.
func ValidateCapturedTranscript(repository fs.FS, queryID string, bundle CapturedCandidateBundle, real PayloadCounter, contractVersions ...string) error {
	if len(contractVersions) > 1 {
		return fmt.Errorf("retrieval follow-up transcript: query %s received %d contract versions, want at most one", queryID, len(contractVersions))
	}
	if len(contractVersions) == 1 {
		switch contractVersions[0] {
		case QrelBlindSmokeContractVersion:
			if bundle.FollowupRead != nil {
				return fmt.Errorf("retrieval follow-up transcript: query %s carries followup_read under %s, which accepts only one response", queryID, QrelBlindSmokeContractVersion)
			}
		case QrelBlindSmokeContractVersion2:
		default:
			return fmt.Errorf("retrieval follow-up transcript: query %s contract_version=%q, want %q or %q", queryID, contractVersions[0], QrelBlindSmokeContractVersion, QrelBlindSmokeContractVersion2)
		}
	}
	if bundle.FollowupRead == nil {
		return nil
	}
	designation, err := compactFollowupDesignation(queryID, bundle.Payload)
	if err != nil {
		return err
	}
	if designation == nil {
		return fmt.Errorf("retrieval follow-up transcript: query %s designated no follow-up but a second slice was preserved", queryID)
	}
	_, _, err = validateFollowupRead(repository, queryID, *designation, *bundle.FollowupRead, real)
	return err
}

// CapturedBundleDesignatesFollowup reports whether slice 1 itself designates a
// follow-up. It reads only captured response bytes; no qrel, judgement or
// target span participates in the decision.
func CapturedBundleDesignatesFollowup(bundle CapturedCandidateBundle) (bool, error) {
	designation, err := compactFollowupDesignation(bundle.QueryID, bundle.Payload)
	if err != nil {
		return false, err
	}
	return designation != nil, nil
}

// CheckPreRegisteredBundleBinding binds the pre-registered byte and token
// identities to the captured one- or two-slice bundle. Transcript byte
// validity against the pinned tree remains ValidateCapturedTranscript's job;
// this check proves that the bytes validated there are the bytes frozen here.
func CheckPreRegisteredBundleBinding(contractVersion string, pre PreRegisteredQuery, bundle CapturedCandidateBundle) error {
	queryID := pre.QueryID
	if queryID == "" {
		queryID = bundle.QueryID
	}
	if pre.QueryID != bundle.QueryID {
		return fmt.Errorf("retrieval %s: pre-registered query %q is bound to captured query %q", QrelBlindSmokeEvaluationName, pre.QueryID, bundle.QueryID)
	}
	if pre.BundleSHA256 != bundle.Payload.SHA256 || pre.BundleSHA256 != SHA256Hex(bundle.Payload.Bytes) {
		return fmt.Errorf("retrieval %s: query %s bundle_sha256=%q does not match the captured first slice %q", QrelBlindSmokeEvaluationName, queryID, pre.BundleSHA256, SHA256Hex(bundle.Payload.Bytes))
	}
	if pre.BundleByteCount != bundle.Payload.ByteCount || pre.BundleByteCount != len(bundle.Payload.Bytes) {
		return fmt.Errorf("retrieval %s: query %s bundle_byte_count=%d does not match the captured first slice byte count %d", QrelBlindSmokeEvaluationName, queryID, pre.BundleByteCount, len(bundle.Payload.Bytes))
	}
	if pre.BundleBoundary != bundle.Payload.Boundary {
		return fmt.Errorf("retrieval %s: query %s bundle boundary=%q does not match the captured first slice boundary=%q", QrelBlindSmokeEvaluationName, queryID, pre.BundleBoundary, bundle.Payload.Boundary)
	}
	if !reflect.DeepEqual(pre.BundleTokenCounts, bundle.Payload.TokenCounts) {
		return fmt.Errorf("retrieval %s: query %s bundle_token_counts do not match the captured first slice", QrelBlindSmokeEvaluationName, queryID)
	}

	metadataPresent := pre.FollowupSHA256 != "" || pre.FollowupByteCount != 0 || len(pre.FollowupTokenCounts) != 0
	switch contractVersion {
	case QrelBlindSmokeContractVersion:
		if bundle.FollowupRead != nil || metadataPresent {
			return fmt.Errorf("retrieval %s: query %s carries a followup_read or follow-up pre-registration fields under %s, which accepts only one response", QrelBlindSmokeEvaluationName, queryID, QrelBlindSmokeContractVersion)
		}
		return nil
	case QrelBlindSmokeContractVersion2:
	default:
		return fmt.Errorf("retrieval %s: query %s bundle binding contract_version=%q, want %q or %q", QrelBlindSmokeEvaluationName, queryID, contractVersion, QrelBlindSmokeContractVersion, QrelBlindSmokeContractVersion2)
	}

	designated, err := CapturedBundleDesignatesFollowup(bundle)
	if err != nil {
		return err
	}
	if designated && bundle.FollowupRead == nil {
		return fmt.Errorf("retrieval %s: query %s first slice designates a follow-up but the captured bundle has no followup_read", QrelBlindSmokeEvaluationName, queryID)
	}
	if !designated && bundle.FollowupRead != nil {
		return fmt.Errorf("retrieval %s: query %s designated no follow-up but the captured bundle carries followup_read", QrelBlindSmokeEvaluationName, queryID)
	}
	if (bundle.FollowupRead != nil) != metadataPresent {
		return fmt.Errorf("retrieval %s: query %s under %s must pre-register follow-up fields if and only if the captured bundle carries the designated read", QrelBlindSmokeEvaluationName, queryID, QrelBlindSmokeContractVersion2)
	}
	if bundle.FollowupRead == nil {
		return nil
	}
	followup := *bundle.FollowupRead
	if pre.FollowupSHA256 != followup.SHA256 || pre.FollowupSHA256 != SHA256Hex(followup.Bytes) {
		return fmt.Errorf("retrieval %s: query %s followup_sha256=%q does not match the captured follow-up read %q", QrelBlindSmokeEvaluationName, queryID, pre.FollowupSHA256, SHA256Hex(followup.Bytes))
	}
	if pre.FollowupByteCount != followup.ByteCount || pre.FollowupByteCount != len(followup.Bytes) {
		return fmt.Errorf("retrieval %s: query %s followup_byte_count=%d does not match the captured follow-up read byte count %d", QrelBlindSmokeEvaluationName, queryID, pre.FollowupByteCount, len(followup.Bytes))
	}
	if !reflect.DeepEqual(pre.FollowupTokenCounts, followup.TokenCounts) {
		return fmt.Errorf("retrieval %s: query %s followup_token_counts do not match the captured follow-up read", QrelBlindSmokeEvaluationName, queryID)
	}
	return nil
}

// CandidateBinding binds a capture to the exact candidate implementation and
// the exact indexed checkout it ran over.
//
// Recording a commit is not binding to it. The capture used to record the
// frozen candidate sha and the pinned checkout sha and check neither against
// the working tree, so an operator could edit graphi or the indexed repository
// WITHOUT committing — until retrieval happened to return the expected answers
// — capture and rate those bytes, then restore both trees, and the report would
// still name the frozen candidate and the pinned checkout and say every hash
// matched. These four observations are what close that: the capture refuses on
// a dirty tree on either side, and refuses when the candidate tree differs from
// the frozen candidate anywhere outside the run directory the run itself writes
// into.
type CandidateBinding struct {
	// CandidateSHA is the candidate worktree's HEAD at capture, and
	// FrozenCandidateSHA is what the precondition record froze. They may
	// differ only by commits that touch nothing outside the run directory,
	// which CandidateMatchesFrozen records.
	CandidateSHA           string `json:"candidate_sha"`
	FrozenCandidateSHA     string `json:"frozen_candidate_sha"`
	CandidateWorktreeClean bool   `json:"candidate_worktree_clean"`
	CandidateMatchesFrozen bool   `json:"candidate_matches_frozen_candidate_sha"`
	CandidateExcludedPath  string `json:"candidate_excluded_path"`
	CheckoutSHA            string `json:"checkout_sha"`
	CheckoutWorktreeClean  bool   `json:"checkout_worktree_clean"`
	// DifferingPaths is empty when the candidate matches. It is recorded
	// rather than summarised so a refusal names what actually moved.
	DifferingPaths []string `json:"differing_paths,omitempty"`
}

// RepoProbe is the seam the binding observes a git worktree through, so the
// refusals can be tested without building throwaway repositories.
type RepoProbe struct {
	// HeadSHA returns the worktree's HEAD commit.
	HeadSHA func(ctx context.Context, root string) (string, error)
	// WorktreeClean reports whether the worktree has no uncommitted change,
	// tracked or untracked.
	WorktreeClean func(ctx context.Context, root string) (bool, error)
	// PathsDifferingOutside lists the paths that differ between two commits,
	// excluding everything under exclude.
	PathsDifferingOutside func(ctx context.Context, root, from, to, exclude string) ([]string, error)
}

// CandidateBindingOptions is one binding observation.
type CandidateBindingOptions struct {
	CandidateRoot      string
	FrozenCandidateSHA string
	// ExcludePath is the run directory, repository-relative. The run
	// necessarily writes into it between the freeze and the capture, so it is
	// the one path a difference is expected in.
	ExcludePath  string
	CheckoutRoot string
	CheckoutSHA  string
}

// ObserveCandidateBinding records the binding and refuses the states that make
// the recorded commits meaningless.
func ObserveCandidateBinding(ctx context.Context, probe RepoProbe, o CandidateBindingOptions) (CandidateBinding, error) {
	var binding CandidateBinding
	if probe.HeadSHA == nil || probe.WorktreeClean == nil || probe.PathsDifferingOutside == nil {
		return binding, fmt.Errorf("retrieval %s capture: the candidate binding needs a complete repository probe", QrelBlindSmokeEvaluationName)
	}
	if !isLowerHexDigest(o.FrozenCandidateSHA, 40) {
		return binding, fmt.Errorf("retrieval %s capture: the candidate binding needs the frozen candidate sha as a 40-character commit id, not %q", QrelBlindSmokeEvaluationName, o.FrozenCandidateSHA)
	}
	// The excluded path is the one hole in the comparison against the frozen
	// candidate, so it is checked before it is used. A root or otherwise
	// over-broad exclusion turns `git diff … -- . ':(exclude)<path>'` into a
	// comparison that swallows the implementation and reports no difference.
	if err := CheckRunDirectoryRelativePath(o.ExcludePath); err != nil {
		return binding, fmt.Errorf("retrieval %s capture: the candidate binding would exclude %q from its comparison against the frozen candidate: %w", QrelBlindSmokeEvaluationName, o.ExcludePath, err)
	}
	head, err := probe.HeadSHA(ctx, o.CandidateRoot)
	if err != nil {
		return binding, fmt.Errorf("retrieval %s capture: candidate HEAD: %w", QrelBlindSmokeEvaluationName, err)
	}
	candidateClean, err := probe.WorktreeClean(ctx, o.CandidateRoot)
	if err != nil {
		return binding, fmt.Errorf("retrieval %s capture: candidate worktree state: %w", QrelBlindSmokeEvaluationName, err)
	}
	if !candidateClean {
		return binding, fmt.Errorf("retrieval %s capture: the candidate worktree at %s has uncommitted changes; the bytes a rater sees must come from the committed candidate this run froze, not from a tree that can be restored afterwards", QrelBlindSmokeEvaluationName, o.CandidateRoot)
	}
	checkoutClean, err := probe.WorktreeClean(ctx, o.CheckoutRoot)
	if err != nil {
		return binding, fmt.Errorf("retrieval %s capture: indexed checkout worktree state: %w", QrelBlindSmokeEvaluationName, err)
	}
	if !checkoutClean {
		return binding, fmt.Errorf("retrieval %s capture: the indexed checkout at %s has uncommitted changes; an edited corpus is not the pinned corpus", QrelBlindSmokeEvaluationName, o.CheckoutRoot)
	}
	differing, err := probe.PathsDifferingOutside(ctx, o.CandidateRoot, o.FrozenCandidateSHA, head, o.ExcludePath)
	if err != nil {
		return binding, fmt.Errorf("retrieval %s capture: candidate tree comparison: %w", QrelBlindSmokeEvaluationName, err)
	}
	binding = CandidateBinding{
		CandidateSHA:           head,
		FrozenCandidateSHA:     o.FrozenCandidateSHA,
		CandidateWorktreeClean: candidateClean,
		CandidateMatchesFrozen: len(differing) == 0,
		CandidateExcludedPath:  o.ExcludePath,
		CheckoutSHA:            o.CheckoutSHA,
		CheckoutWorktreeClean:  checkoutClean,
		DifferingPaths:         differing,
	}
	if !binding.CandidateMatchesFrozen {
		return binding, fmt.Errorf("retrieval %s capture: the candidate tree at %s differs from the frozen candidate %s outside %s (%s); the evaluation would be run against a candidate other than the one it froze",
			QrelBlindSmokeEvaluationName, head, o.FrozenCandidateSHA, o.ExcludePath, strings.Join(differing, ", "))
	}
	return binding, nil
}

// CandidateCaptureProvenance records the composition the bytes came out of.
type CandidateCaptureProvenance struct {
	CaptureVersion    string `json:"capture_version"`
	Transport         string `json:"transport"`
	Surface           string `json:"surface"`
	Boundary          string `json:"boundary"`
	RepoName          string `json:"repo_name"`
	RepoSHA           string `json:"repo_sha"`
	DatasetSHA256     string `json:"dataset_sha256"`
	EmbedderSelector  string `json:"embedder_selector"`
	ModelFingerprint  string `json:"model_fingerprint"`
	IndexFingerprint  string `json:"index_fingerprint"`
	GenerationID      string `json:"generation_id"`
	PersistedVectors  int    `json:"persisted_vectors"`
	SemanticState     string `json:"semantic_state"`
	TokenBudget       int    `json:"token_budget"`
	MethodVersion     string `json:"method_version"`
	TokenizerID       string `json:"tokenizer_id"`
	TokenizerVocabSHA string `json:"tokenizer_vocabulary_sha256"`
	QueryCount        int    `json:"query_count"`
	// Binding is nil only for a capture taken before the binding existed. A
	// nil binding is a release refusal, not a missing report row.
	Binding                  *CandidateBinding         `json:"candidate_binding,omitempty"`
	QualificationBuildDigest *QualificationBuildDigest `json:"qualification_build_digest,omitempty"`
}

// GitRepoProbe is the production RepoProbe. Each observation is one git
// command whose output is read directly rather than interpreted: a probe that
// guessed would defeat the point of observing.
func GitRepoProbe() RepoProbe {
	return RepoProbe{
		HeadSHA: CheckoutHEAD,
		WorktreeClean: func(ctx context.Context, root string) (bool, error) {
			out, err := exec.CommandContext(ctx, "git", "-C", root, "status", "--porcelain", "--untracked-files=normal").Output()
			if err != nil {
				return false, fmt.Errorf("git status --porcelain in %s: %w", root, err)
			}
			return strings.TrimSpace(string(out)) == "", nil
		},
		PathsDifferingOutside: func(ctx context.Context, root, from, to, exclude string) ([]string, error) {
			args := []string{"-C", root, "diff", "--name-only", from, to, "--", "."}
			if strings.TrimSpace(exclude) != "" {
				args = append(args, ":(exclude)"+exclude)
			}
			out, err := exec.CommandContext(ctx, "git", args...).Output()
			if err != nil {
				return nil, fmt.Errorf("git diff --name-only %s %s in %s: %w", from, to, root, err)
			}
			var paths []string
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if strings.TrimSpace(line) != "" {
					paths = append(paths, line)
				}
			}
			return paths, nil
		},
	}
}

// GitCommitResolver is the production CommitResolver: it asks git whether the
// id names a commit object in the repository at root.
//
// `git cat-file -e <sha>^{commit}` exits 0 when the id resolves to a commit,
// and non-zero when it resolves to nothing or to an object that is not a
// commit. That distinction matters: a tree or blob id is forty hex characters
// too, and binding a run to one would be the same error wearing a different
// shape. A failure to run git at all is returned as an error rather than as
// "does not exist", so a broken environment reads as unresolved rather than as
// a forged binding.
func GitCommitResolver(root string) CommitResolver {
	return func(sha string) (bool, error) {
		cmd := exec.Command("git", "-C", root, "cat-file", "-e", sha+"^{commit}")
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err == nil {
			return true, nil
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false, nil
		}
		return false, fmt.Errorf("git cat-file -e %s^{commit} in %s: %w (%s)", sha, root, err, strings.TrimSpace(stderr.String()))
	}
}

// CandidateCaptureOptions is one fail-closed capture run.
type CandidateCaptureOptions struct {
	RepoRoot         string
	RepoName         string
	RepoSHA          string
	Dataset          *Loaded
	Queries          []Query
	EmbedderSelector string
	WorkDir          string
	RealCounter      PayloadCounter
	Log              io.Writer
	// Embedder is an evaluation-only injection seam. Nil preserves the existing
	// selector-based construction path byte-for-byte.
	Embedder                     embed.Embedder
	ExpectedFingerprint          *embed.Fingerprint
	QualificationArm             QualificationArm
	QualificationBuild           int
	QualificationPreregistration *QualificationPreregistration
	ManifestBytes                []byte
	// Binding is the candidate/checkout binding this capture must observe
	// before it runs, and Probe is how it observes them. Both are required:
	// an unbound capture produces bytes nobody can attribute to a commit.
	Binding CandidateBindingOptions
	Probe   RepoProbe
	// ObservedBinding is supplied by the qualification driver after it binds
	// both repositories exactly once, before any staging write. Nil preserves
	// the existing per-capture observation behavior.
	ObservedBinding *CandidateBinding
}

// CaptureCandidateBundles builds the production index over the pinned checkout
// and captures exactly one complete MCP JSON-RPC `task_context/2` response per
// query at the frozen 1200-token budget.
//
// It fails closed on every degradation the measurement would otherwise absorb
// silently: a non-ready semantic generation, a retrieval fallback, a /1 bundle,
// more or fewer than one retrieval call, or a response that is not a
// well-formed single-line JSON-RPC result.
func CaptureCandidateBundles(ctx context.Context, o CandidateCaptureOptions) ([]CapturedCandidateBundle, CandidateCaptureProvenance, error) {
	var provenance CandidateCaptureProvenance
	if o.Dataset == nil || o.Dataset.Dataset == nil {
		return nil, provenance, fmt.Errorf("retrieval %s capture: no dataset", QrelBlindSmokeEvaluationName)
	}
	if len(o.Queries) == 0 {
		return nil, provenance, fmt.Errorf("retrieval %s capture: no queries", QrelBlindSmokeEvaluationName)
	}
	if strings.TrimSpace(o.RepoRoot) == "" || strings.TrimSpace(o.RepoSHA) == "" {
		return nil, provenance, fmt.Errorf("retrieval %s capture: repository root and sha are required", QrelBlindSmokeEvaluationName)
	}
	if o.QualificationArm != ArmLexical && o.Embedder == nil && strings.TrimSpace(o.EmbedderSelector) == "" {
		return nil, provenance, fmt.Errorf("retrieval %s capture: a production embedder selector is required", QrelBlindSmokeEvaluationName)
	}
	strictQualification := o.ExpectedFingerprint != nil || o.QualificationPreregistration != nil || o.QualificationArm != "" || len(o.ManifestBytes) != 0
	if strictQualification {
		if o.QualificationPreregistration == nil || o.QualificationArm == "" {
			return nil, provenance, fmt.Errorf("embedded-model qualification capture: arm and preregistration are required")
		}
		if o.QualificationBuild != 1 && o.QualificationBuild != 2 {
			return nil, provenance, fmt.Errorf("embedded-model qualification capture: build ordinal must be 1 or 2")
		}
		if o.QualificationArm != ArmLexical && (o.Embedder == nil || o.ExpectedFingerprint == nil) {
			return nil, provenance, fmt.Errorf("embedded-model qualification capture: semantic arms require an injected embedder and expected fingerprint")
		}
		if err := ValidateQualificationDataset(o.Dataset); err != nil {
			return nil, provenance, err
		}
		if o.Dataset.SHA256 != o.QualificationPreregistration.DatasetSHA256 {
			return nil, provenance, fmt.Errorf("embedded-model qualification capture: dataset differs from preregistration")
		}
		expected := embed.Fingerprint{}
		if o.ExpectedFingerprint != nil {
			expected = *o.ExpectedFingerprint
		}
		if err := validateQualificationCaptureBinding(o.QualificationArm, *o.QualificationPreregistration, expected, o.ManifestBytes); err != nil {
			return nil, provenance, err
		}
		if o.QualificationArm != ArmLexical && o.Embedder.ID() != o.ExpectedFingerprint.ModelID {
			return nil, provenance, fmt.Errorf("embedded-model qualification capture: injected embedder identity differs from expected fingerprint")
		}
	}
	if o.RealCounter.Count == nil || o.RealCounter.TokenizerID == "" || o.RealCounter.TokenizerID == TokenizerID {
		return nil, provenance, fmt.Errorf("retrieval %s capture: the pinned real tokenizer counter is required", QrelBlindSmokeEvaluationName)
	}
	if o.Log == nil {
		o.Log = io.Discard
	}

	head, err := CheckoutHEAD(ctx, o.RepoRoot)
	if err != nil {
		return nil, provenance, err
	}
	if !strings.EqualFold(head, o.RepoSHA) || !strings.EqualFold(head, o.Dataset.Dataset.RepoSHA) {
		return nil, provenance, fmt.Errorf("retrieval %s capture: checkout is at %s, option pins %s and dataset pins %s", QrelBlindSmokeEvaluationName, head, o.RepoSHA, o.Dataset.Dataset.RepoSHA)
	}
	var binding CandidateBinding
	if o.ObservedBinding != nil {
		binding = *o.ObservedBinding
		if !binding.CandidateWorktreeClean || !binding.CheckoutWorktreeClean || !binding.CandidateMatchesFrozen ||
			!strings.EqualFold(binding.CheckoutSHA, head) || !strings.EqualFold(binding.FrozenCandidateSHA, o.Binding.FrozenCandidateSHA) {
			return nil, provenance, fmt.Errorf("embedded-model qualification capture: pre-observed candidate binding does not match this capture")
		}
	} else {
		bindingOptions := o.Binding
		bindingOptions.CheckoutRoot = o.RepoRoot
		bindingOptions.CheckoutSHA = head
		binding, err = ObserveCandidateBinding(ctx, o.Probe, bindingOptions)
		if err != nil {
			return nil, provenance, err
		}
	}

	workDir := o.WorkDir
	if workDir == "" {
		workDir, err = os.MkdirTemp("", "graphi-qrel-blind-capture")
		if err != nil {
			return nil, provenance, fmt.Errorf("retrieval %s capture: workdir: %w", QrelBlindSmokeEvaluationName, err)
		}
		defer os.RemoveAll(workDir)
	}
	idx, err := buildCandidateCaptureIndex(ctx, o, o.RepoRoot, workDir, o.Log)
	if err != nil {
		return nil, provenance, err
	}
	defer idx.store.Close()

	semanticState := idx.search.SemanticState()
	if o.QualificationArm != ArmLexical && semanticState.State != embed.StateReady {
		return nil, provenance, fmt.Errorf("retrieval %s capture: semantic state is %s, want ready; refusing a lexical-fallback bundle", QrelBlindSmokeEvaluationName, semanticState.State)
	}
	if o.QualificationArm != ArmLexical && semanticState.Requested.Canonical() != idx.fingerprint.Canonical() {
		return nil, provenance, fmt.Errorf("retrieval %s capture: the search service's requested fingerprint does not equal the independently verified generation fingerprint", QrelBlindSmokeEvaluationName)
	}
	if err := validateCandidateCaptureFingerprint(o, idx); err != nil {
		return nil, provenance, err
	}

	querySvc := query.New(idx.store)
	realEngine := engineretrieval.New(resolve.Deps{Query: querySvc, Search: idx.search}, idx.search, idx.store)
	if realEngine == nil {
		return nil, provenance, fmt.Errorf("retrieval %s capture: retrieval.New returned nil", QrelBlindSmokeEvaluationName)
	}

	modelFingerprint := idx.embedderID
	indexFingerprint := idx.fingerprint.Canonical()
	if o.QualificationArm == ArmLexical {
		modelFingerprint = ""
		indexFingerprint = ""
	} else if strictQualification {
		modelFingerprint = idx.fingerprint.Canonical()
	}
	provenance = CandidateCaptureProvenance{
		CaptureVersion:    CandidateCaptureVersion,
		Transport:         "MCP stdio JSON-RPC 2.0 (surfaces/mcp.Server.Serve, line-delimited)",
		Surface:           "surfaces/mcp tools/call " + mcp.ToolTaskContext,
		Boundary:          string(PayloadBoundaryCandidate),
		RepoName:          o.RepoName,
		RepoSHA:           head,
		DatasetSHA256:     o.Dataset.SHA256,
		EmbedderSelector:  o.EmbedderSelector,
		ModelFingerprint:  modelFingerprint,
		IndexFingerprint:  indexFingerprint,
		GenerationID:      string(idx.generationID),
		PersistedVectors:  idx.persistedVectors,
		SemanticState:     semanticState.State.String(),
		TokenBudget:       SavingsCandidateBudget,
		MethodVersion:     taskcompact.Version,
		TokenizerID:       o.RealCounter.TokenizerID,
		TokenizerVocabSHA: o.RealCounter.VocabularySHA256,
		QueryCount:        len(o.Queries),
		Binding:           &binding,
	}

	captured := make([]CapturedCandidateBundle, 0, len(o.Queries))
	for _, q := range o.Queries {
		bundle, err := captureOneCandidateBundle(ctx, o, q, querySvc, idx, realEngine)
		if err != nil {
			return nil, provenance, err
		}
		captured = append(captured, bundle)
	}
	if strictQualification {
		inputs := qualificationBuildInputs{
			Rows: idx.rows, AdmittedDocuments: idx.admittedDocuments,
			QueryVectors: make(map[string][]float32, len(captured)), Payloads: make([]PreservedPayload, 0, len(captured)),
			OracleControls: make(map[string]OracleControls, len(captured)),
		}
		for _, bundle := range captured {
			inputs.QueryVectors[bundle.QueryID] = bundle.QualificationQueryVector
			inputs.Payloads = append(inputs.Payloads, bundle.Payload)
			if bundle.OracleControls == nil {
				return nil, provenance, fmt.Errorf("embedded-model qualification capture: query %s has no one-shot oracle controls", bundle.QueryID)
			}
			inputs.OracleControls[bundle.QueryID] = *bundle.OracleControls
		}
		digest := buildQualificationDigest(o.QualificationArm, inputs)
		digest.Build = o.QualificationBuild
		provenanceSnapshot := provenance
		provenanceSnapshot.QualificationBuildDigest = nil
		captureRecord, err := sealQualificationCaptureProvenanceRecord(QualificationCaptureProvenanceRecord{
			Arm: o.QualificationArm, Build: o.QualificationBuild, WorkDir: o.WorkDir, Provenance: provenanceSnapshot,
		})
		if err != nil {
			return nil, provenance, fmt.Errorf("embedded-model qualification capture: encode build provenance: %w", err)
		}
		digest.CaptureProvenance = captureRecord
		digest.Diagnostics = qualificationBuildDiagnostics(idx.rows, idx.admissionTruncations)
		provenance.QualificationBuildDigest = &digest
	}
	return captured, provenance, nil
}

func sealQualificationCaptureProvenanceRecord(record QualificationCaptureProvenanceRecord) (QualificationCaptureProvenanceRecord, error) {
	address, err := ContentAddress(record, func(v *QualificationCaptureProvenanceRecord) {
		v.SHA256 = ""
		v.Provenance.QualificationBuildDigest = nil
	})
	if err != nil {
		return QualificationCaptureProvenanceRecord{}, err
	}
	record.SHA256 = address
	return record, nil
}

func buildCandidateCaptureIndex(ctx context.Context, o CandidateCaptureOptions, root, workDir string, log io.Writer) (*taskContextIndex, error) {
	if o.QualificationArm == ArmLexical {
		return buildTaskContextLexicalIndex(ctx, root, workDir, log)
	}
	if o.Embedder != nil {
		return buildTaskContextIndexWithEmbedder(ctx, root, workDir, o.Embedder, o.EmbedderSelector, log)
	}
	return buildTaskContextIndex(ctx, root, workDir, o.EmbedderSelector, log)
}

func validateCandidateCaptureFingerprint(o CandidateCaptureOptions, idx *taskContextIndex) error {
	if o.ExpectedFingerprint == nil {
		return nil
	}
	if idx == nil {
		return fmt.Errorf("embedded-model qualification capture: no loaded index")
	}
	want := o.ExpectedFingerprint.Canonical()
	state := idx.search.SemanticState()
	if idx.fingerprint.Canonical() != want || state.Requested.Canonical() != want {
		return fmt.Errorf("embedded-model qualification capture: loaded generation and search request must equal the expected canonical fingerprint")
	}
	return nil
}

func captureOneCandidateBundle(ctx context.Context, o CandidateCaptureOptions, q Query, querySvc *query.Service, idx *taskContextIndex, realEngine TaskContextEngine) (CapturedCandidateBundle, error) {
	var qualificationResult engineretrieval.Result
	var semanticHits []search.SemanticHit
	var queryVector []float32
	strictQualification := o.QualificationPreregistration != nil
	if strictQualification {
		mode := engineretrieval.ModeLexicalOnly
		if o.QualificationArm != ArmLexical {
			vectors, err := embed.EmbedQuery(ctx, o.Embedder, q.Text)
			if err != nil {
				return CapturedCandidateBundle{}, fmt.Errorf("embedded-model qualification capture: query %s vector capture: %w", q.ID, err)
			}
			if len(vectors) != 1 || len(vectors[0]) != o.ExpectedFingerprint.Dim {
				return CapturedCandidateBundle{}, fmt.Errorf("embedded-model qualification capture: query %s vector shape is invalid", q.ID)
			}
			queryVector = append([]float32(nil), vectors[0]...)
			semantic, err := idx.search.SemanticSearch(ctx, q.Text, 50)
			if err != nil || !semantic.Available || semantic.State != embed.StateReady {
				return CapturedCandidateBundle{}, fmt.Errorf("embedded-model qualification capture: query %s semantic top-50 unavailable: %v", q.ID, err)
			}
			semanticHits = semantic.Hits
			mode = engineretrieval.ModeAuto
		}
		var err error
		qualificationResult, err = realEngine.Retrieve(ctx, engineretrieval.Request{Query: q.Text, Limit: 50, Mode: mode})
		if err != nil {
			return CapturedCandidateBundle{}, fmt.Errorf("embedded-model qualification capture: query %s post-fusion retrieval: %w", q.ID, err)
		}
	}
	// One adapter per query, so "exactly one task_context/2 call" is observed
	// per query rather than inferred from a running total.
	adapter := NewTaskContextRetriever(realEngine)
	direct := client.NewDirect(querySvc, idx.search).
		WithRetrieval(adapter).
		WithRepoRoot(o.RepoRoot)
	var surfaceClient client.Client = direct
	var oracleClient *qualificationOracleCaptureClient
	if strictQualification {
		retrievalState := embed.StateReady.String()
		if o.QualificationArm == ArmLexical {
			retrievalState = string(engineretrieval.StateLexicalOnly)
		}
		oracleClient = &qualificationOracleCaptureClient{
			Client: direct,
			Input: OracleInput{
				Query: q, Repository: os.DirFS(o.RepoRoot), RealCounter: o.RealCounter,
				RetrievalState: retrievalState,
			},
		}
		surfaceClient = oracleClient
	}
	serverOptions := []mcp.ServerOption{mcp.WithLabs(), mcp.WithRepository(client.Repository{Root: o.RepoRoot})}
	if o.QualificationArm == ArmLexical {
		serverOptions = append(serverOptions, mcp.WithEvaluationLexicalCompactControl())
	}
	server := mcp.NewServerWithClient(surfaceClient, serverOptions...)
	defer server.Close()

	request, err := candidateToolCallRequest(q.Text)
	if err != nil {
		return CapturedCandidateBundle{}, err
	}

	// The writer's buffer IS the artifact: whatever Serve's encoder emits is
	// what a client reads off the pipe, and nothing here reformats it.
	var out bytes.Buffer
	if err := server.Serve(ctx, bytes.NewReader(request), &out); err != nil {
		return CapturedCandidateBundle{}, fmt.Errorf("retrieval %s capture: query %s MCP serve: %w", QrelBlindSmokeEvaluationName, q.ID, err)
	}
	responseBytes := out.Bytes()
	if oracleClient != nil {
		if oracleClient.err != nil {
			return CapturedCandidateBundle{}, fmt.Errorf("embedded-model qualification capture: query %s oracle controls: %w", q.ID, oracleClient.err)
		}
		if oracleClient.called != 1 || oracleClient.controls == nil {
			return CapturedCandidateBundle{}, fmt.Errorf("embedded-model qualification capture: query %s captured %d frozen candidate results for oracle controls, want exactly 1", q.ID, oracleClient.called)
		}
	}

	if adapter.Called() != 1 {
		return CapturedCandidateBundle{}, fmt.Errorf("retrieval %s capture: query %s called the real retrieval instance %d times, want exactly 1", QrelBlindSmokeEvaluationName, q.ID, adapter.Called())
	}
	if adapter.LastErr() != nil {
		return CapturedCandidateBundle{}, fmt.Errorf("retrieval %s capture: query %s retrieval errored (%v); the bundle would be a fallback", QrelBlindSmokeEvaluationName, q.ID, adapter.LastErr())
	}
	last := adapter.LastResult()
	if o.QualificationArm != ArmLexical && last.Degradation != string(engineretrieval.StateReady) {
		return CapturedCandidateBundle{}, fmt.Errorf("retrieval %s capture: query %s retrieval state is %q, want ready", QrelBlindSmokeEvaluationName, q.ID, last.Degradation)
	}
	wantStrategy := "semantic_first"
	wantState := string(engineretrieval.StateReady)
	if o.QualificationArm == ArmLexical {
		wantStrategy = "lexical_only"
		wantState = string(engineretrieval.StateLexicalOnly)
	}
	if last.Summary.RetrievalVersion != engineretrieval.Version || last.Summary.Strategy != wantStrategy || last.Degradation != wantState {
		return CapturedCandidateBundle{}, fmt.Errorf("retrieval %s capture: query %s method is %s/%s state %s, want %s/%s state %s", QrelBlindSmokeEvaluationName, q.ID, last.Summary.RetrievalVersion, last.Summary.Strategy, last.Degradation, engineretrieval.Version, wantStrategy, wantState)
	}
	if strictQualification {
		expected := embed.Fingerprint{}
		if o.ExpectedFingerprint != nil {
			expected = *o.ExpectedFingerprint
		}
		if err := validateQualificationRetrieverSummary(o.QualificationArm, expected, last); err != nil {
			return CapturedCandidateBundle{}, err
		}
	}
	validateState := embed.StateReady.String()
	if o.QualificationArm == ArmLexical {
		validateState = string(engineretrieval.StateLexicalOnly)
	}
	summary, err := validateCompactCandidateBundleBytesState(q.ID, responseBytes, validateState)
	if err != nil {
		return CapturedCandidateBundle{}, err
	}

	payload, err := preserveCandidatePayload(q.ID, responseBytes, o.RealCounter)
	if err != nil {
		return CapturedCandidateBundle{}, err
	}
	capturedOut := CapturedCandidateBundle{
		QueryID:                  q.ID,
		RequestBytes:             request,
		Payload:                  payload,
		RetrievalStrategy:        last.Summary.Strategy,
		RetrievalState:           last.Degradation,
		BundleSummary:            summary,
		QualificationQueryVector: queryVector,
	}
	if oracleClient != nil {
		capturedOut.OracleControls = oracleClient.controls
	}
	if strictQualification {
		var structured taskcompact.Structured
		structured, err = qualificationStructuredFromPayload(responseBytes)
		if err != nil {
			return CapturedCandidateBundle{}, err
		}
		expected := embed.Fingerprint{}
		if o.ExpectedFingerprint != nil {
			expected = *o.ExpectedFingerprint
		}
		modelFingerprint := idx.fingerprint.Canonical()
		if o.QualificationArm == ArmLexical {
			modelFingerprint = ""
		}
		observation, err := captureQualificationObservation(qualificationCaptureFacts{
			Arm: o.QualificationArm, Query: q, SemanticState: idx.search.SemanticState().State,
			ExpectedFingerprint: expected, IndexFingerprint: idx.fingerprint,
			SearchFingerprint: idx.search.SemanticState().Requested, ModelFingerprint: modelFingerprint,
			Retrieval: qualificationResult, SemanticHits: semanticHits, Payload: payload, Structured: structured,
			QueryVector: queryVector, UnknownTokens: QualificationIntMetric{Reason: "embedder protocol does not expose unknown-token count"},
		})
		if err != nil {
			return CapturedCandidateBundle{}, err
		}
		capturedOut.Qualification = &observation
	}
	return capturedOut, nil
}

// qualificationOracleCaptureClient observes the exact canonical
// task_context/2 contract.Result returned during the one MCP call. Embedding
// client.Client promotes every other method unchanged; only TaskContext is
// intercepted, so no product surface or second retrieval call is introduced.
type qualificationOracleCaptureClient struct {
	client.Client
	Input    OracleInput
	called   int
	controls *OracleControls
	err      error
}

func (c *qualificationOracleCaptureClient) TaskContext(ctx context.Context, p client.TaskContextParams) ([]byte, error) {
	raw, err := c.Client.TaskContext(ctx, p)
	if err != nil {
		return nil, err
	}
	c.called++
	if c.called != 1 {
		c.err = fmt.Errorf("normal candidates were produced more than once")
		return nil, c.err
	}
	if p.Task != c.Input.Query.Text {
		c.err = fmt.Errorf("captured task %q differs from frozen query", p.Task)
		return nil, c.err
	}
	var candidates contract.Result
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&candidates); err != nil {
		c.err = fmt.Errorf("decode frozen normal candidates: %w", err)
		return nil, c.err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			c.err = fmt.Errorf("frozen normal candidates contain a trailing JSON value")
		} else {
			c.err = fmt.Errorf("frozen normal candidates contain trailing bytes: %w", err)
		}
		return nil, c.err
	}
	before := SHA256Hex(raw)
	in := c.Input
	in.CurrentCandidates = candidates
	controls, err := BuildOracleControls(in)
	if err != nil {
		c.err = err
		return nil, err
	}
	if SHA256Hex(raw) != before {
		c.err = fmt.Errorf("oracle construction mutated frozen normal candidate bytes")
		return nil, c.err
	}
	c.controls = &controls
	return raw, nil
}

func validateQualificationRetrieverSummary(arm QualificationArm, expected embed.Fingerprint, got resolve.RetrieverResult) error {
	if arm == ArmLexical {
		if got.Degradation != string(engineretrieval.StateLexicalOnly) || got.Summary.ModelFingerprint != "" || got.Summary.IndexFingerprint != "" {
			return fmt.Errorf("embedded-model qualification capture: lexical payload retrieval carries semantic state or identity")
		}
		return nil
	}
	want := expected.Canonical()
	if got.Degradation != string(engineretrieval.StateReady) || got.Summary.ModelFingerprint != want || got.Summary.IndexFingerprint != want {
		return fmt.Errorf("embedded-model qualification capture: payload retrieval state and fingerprints do not equal the preregistered canonical identity")
	}
	return nil
}

func validateQualificationLexicalBundleBytes(queryID string, raw []byte) (string, error) {
	if len(raw) == 0 || !bytes.HasPrefix(raw, []byte(candidateJSONRPCPrefix)) || raw[len(raw)-1] != '\n' || bytes.Count(raw, []byte{'\n'}) != 1 {
		return "", fmt.Errorf("embedded-model qualification capture: lexical query %s is not one exact MCP response", queryID)
	}
	var envelope candidateResponseEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Result == nil || envelope.Result.IsError || len(envelope.Result.Content) != 1 || envelope.Result.Content[0].Type != "text" {
		return "", fmt.Errorf("embedded-model qualification capture: lexical query %s is not one successful MCP result", queryID)
	}
	if strings.TrimSpace(envelope.Result.Content[0].Text) == "" {
		return "", fmt.Errorf("embedded-model qualification capture: lexical query %s returned an empty bundle", queryID)
	}
	var bundle contract.Result
	decoder := json.NewDecoder(strings.NewReader(envelope.Result.Content[0].Text))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil || contract.ValidateResult(&bundle) != nil {
		return "", fmt.Errorf("embedded-model qualification capture: lexical query %s did not serialize one valid task-context bundle", queryID)
	}
	return envelope.Result.Content[0].Text, nil
}

func qualificationLexicalSources(raw string) ([]taskcompact.Source, error) {
	var bundle contract.Result
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return nil, fmt.Errorf("embedded-model qualification capture: decode lexical bundle sources: %w", err)
	}
	sources := make([]taskcompact.Source, 0)
	for _, evidence := range bundle.Evidence {
		if evidence.Snippet == "" {
			continue
		}
		start, end := 0, 0
		if _, err := fmt.Sscanf(evidence.Span, "%d-%d", &start, &end); err != nil {
			start = evidence.Line
			end = start + len(strings.Split(evidence.Snippet, "\n")) - 1
		}
		if start < 1 || end < start || end-start+1 != len(strings.Split(evidence.Snippet, "\n")) {
			return nil, fmt.Errorf("embedded-model qualification capture: lexical evidence %s has inconsistent serialized span", evidence.RefID)
		}
		sources = append(sources, taskcompact.Source{Path: evidence.Path, StartLine: start, EndLine: end, Text: evidence.Snippet})
	}
	return sources, nil
}

func qualificationStructuredFromPayload(raw []byte) (taskcompact.Structured, error) {
	var envelope candidateResponseEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Result == nil {
		return taskcompact.Structured{}, fmt.Errorf("embedded-model qualification capture: decode preserved compact payload: %v", err)
	}
	var structured taskcompact.Structured
	decoder := json.NewDecoder(bytes.NewReader(envelope.Result.StructuredContent))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&structured); err != nil {
		return taskcompact.Structured{}, fmt.Errorf("embedded-model qualification capture: decode compact structured content: %w", err)
	}
	return structured, nil
}

func qualificationZeroVectors(rows []embed.Row) int {
	zero := 0
	for _, row := range rows {
		allZero := len(row.Vector) > 0
		for _, value := range row.Vector {
			if value != 0 {
				allZero = false
				break
			}
		}
		if allZero {
			zero++
		}
	}
	return zero
}

// candidateToolCallRequest builds the one request line. token_budget and
// version are literal constants here: there is no flag, option or environment
// variable that can change what the candidate was asked for.
func candidateToolCallRequest(task string) ([]byte, error) {
	budget := SavingsCandidateBudget
	version := 2
	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(candidateRequestID),
		"method":  "tools/call",
		"params": map[string]any{
			"name": mcp.ToolTaskContext,
			"arguments": map[string]any{
				"task":         task,
				"version":      version,
				"token_budget": budget,
			},
		},
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("retrieval %s capture: encode request: %w", QrelBlindSmokeEvaluationName, err)
	}
	return append(encoded, '\n'), nil
}

// candidateResponseEnvelope is the shape a captured response must decode to. It
// is parsed for VALIDATION only; the preserved bytes are never re-serialized
// from it.
type candidateResponseEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  *struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
	} `json:"result"`
	Error json.RawMessage `json:"error"`
}

// ValidateCompactCandidateBundleBytes is the current release-candidate wire
// validator. The older ValidateCandidateBundleBytes remains available solely
// to recount preserved historical inputs whose task_context contract was JSON
// nested in content[0].text; a new capture must use structuredContent.
func ValidateCompactCandidateBundleBytes(queryID string, raw []byte) (string, error) {
	return validateCompactCandidateBundleBytesState(queryID, raw, embed.StateReady.String())
}

func validateCompactCandidateBundleBytesState(queryID string, raw []byte, retrievalState string) (string, error) {
	if len(raw) == 0 || !bytes.HasPrefix(raw, []byte(candidateJSONRPCPrefix)) || raw[len(raw)-1] != '\n' || bytes.Count(raw, []byte{'\n'}) != 1 {
		return "", fmt.Errorf("retrieval %s capture: query %s is not one exact line-delimited MCP response", QrelBlindSmokeEvaluationName, queryID)
	}
	var envelope candidateResponseEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", fmt.Errorf("retrieval %s capture: query %s response is not JSON-RPC: %w", QrelBlindSmokeEvaluationName, queryID, err)
	}
	if envelope.JSONRPC != "2.0" || string(envelope.ID) != candidateRequestID || envelope.Result == nil || envelope.Result.IsError || len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		return "", fmt.Errorf("retrieval %s capture: query %s response is not one successful request-id %s result", QrelBlindSmokeEvaluationName, queryID, candidateRequestID)
	}
	if len(envelope.Result.Content) != 1 || envelope.Result.Content[0].Type != "text" || strings.TrimSpace(envelope.Result.Content[0].Text) == "" {
		return "", fmt.Errorf("retrieval %s capture: query %s compact response requires one concise text fallback", QrelBlindSmokeEvaluationName, queryID)
	}
	var structured taskcompact.Structured
	if len(envelope.Result.StructuredContent) == 0 || string(envelope.Result.StructuredContent) == "null" {
		return "", fmt.Errorf("retrieval %s capture: query %s compact response has no structuredContent", QrelBlindSmokeEvaluationName, queryID)
	}
	decoder := json.NewDecoder(bytes.NewReader(envelope.Result.StructuredContent))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&structured); err != nil {
		return "", fmt.Errorf("retrieval %s capture: query %s invalid compact structuredContent: %w", QrelBlindSmokeEvaluationName, queryID, err)
	}
	if structured.Version != taskcompact.Version {
		return "", fmt.Errorf("retrieval %s capture: query %s compact identity is invalid", QrelBlindSmokeEvaluationName, queryID)
	}
	p := structured.Provenance
	modelDigest := strings.TrimPrefix(p.Model, "sha256:")
	validModel := p.Model == "" && retrievalState == string(engineretrieval.StateLexicalOnly)
	validModel = validModel || (strings.HasPrefix(p.Model, "sha256:") && isLowerHexDigest(modelDigest, 16))
	validWeights := p.Weights != "" || retrievalState == string(engineretrieval.StateLexicalOnly)
	if !isLowerHexDigest(p.InputSHA256, 64) || p.Method != taskctx.MethodVersionV2 || !strings.HasPrefix(p.Retrieval, "retrieval/") || p.RetrievalState != retrievalState || !validWeights || !validModel || !strings.HasPrefix(p.SourceSelection, "context-definitions/") || p.SourceOrder != "ranked_coherent_regions" || p.SourceBudget != taskcompact.DefaultSourceBudget || p.BudgetUnit != TokenizerID {
		return "", fmt.Errorf("retrieval %s capture: query %s compact provenance is incomplete or not ready", QrelBlindSmokeEvaluationName, queryID)
	}
	seen := make(map[string]bool)
	used := 0
	for _, source := range structured.Sources {
		key := fmt.Sprintf("%s\x00%d\x00%d", source.Path, source.StartLine, source.EndLine)
		if !fs.ValidPath(source.Path) || source.StartLine < 1 || source.EndLine < source.StartLine || source.Text == "" || source.EndLine-source.StartLine+1 != len(strings.Split(source.Text, "\n")) || seen[key] {
			return "", fmt.Errorf("retrieval %s capture: query %s has invalid or duplicate compact source", QrelBlindSmokeEvaluationName, queryID)
		}
		seen[key] = true
		used += len(strings.Fields(source.Text))
	}
	if used > p.SourceBudget {
		return "", fmt.Errorf("retrieval %s capture: query %s compact source budget exceeded: %d > %d", QrelBlindSmokeEvaluationName, queryID, used, p.SourceBudget)
	}
	tokenizer, err := cltokenizer.LoadEmbedded()
	if err != nil {
		return "", fmt.Errorf("retrieval %s capture: query %s load governed tokenizer: %w", QrelBlindSmokeEvaluationName, queryID, err)
	}
	tokens, err := tokenizer.Count(raw)
	if err != nil {
		return "", fmt.Errorf("retrieval %s capture: query %s count exact response tokens: %w", QrelBlindSmokeEvaluationName, queryID, err)
	}
	if tokens > SavingsCandidateBudget {
		return "", fmt.Errorf("retrieval %s capture: query %s exact compact response costs %d cl100k tokens, frozen budget is %d", QrelBlindSmokeEvaluationName, queryID, tokens, SavingsCandidateBudget)
	}
	return envelope.Result.Content[0].Text, nil
}

// ValidateCandidateBundleBytes refuses everything that is not the exact
// transport bytes of one complete, successful, ready `task_context/2` response.
//
// Each clause below corresponds to a substitute AC-2 forbids, and each has a
// test that mutates a real capture into that shape and asserts the refusal:
//
//   - a reconstructed or re-marshaled envelope loses the encoder's field order
//     and fails the prefix check;
//   - a pretty-printed envelope carries interior newlines and fails the
//     single-line check;
//   - an extracted-text bundle has no envelope at all and fails both;
//   - a /1 bundle, or one assembled from a degraded retrieval, fails the
//     method-version and degradation checks.
//
// It returns the bundle summary so the caller can record what it validated.
func ValidateCandidateBundleBytes(queryID string, raw []byte) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("retrieval %s capture: query %s produced no response bytes", QrelBlindSmokeEvaluationName, queryID)
	}
	if !bytes.HasPrefix(raw, []byte(candidateJSONRPCPrefix)) {
		return "", fmt.Errorf("retrieval %s capture: query %s response does not begin with the stdio encoder's envelope %s; a reconstructed, re-marshaled or extracted-text bundle is not the measured object", QrelBlindSmokeEvaluationName, queryID, candidateJSONRPCPrefix)
	}
	if raw[len(raw)-1] != '\n' {
		return "", fmt.Errorf("retrieval %s capture: query %s response is missing the transport's terminating newline", QrelBlindSmokeEvaluationName, queryID)
	}
	if n := bytes.Count(raw, []byte{'\n'}); n != 1 {
		return "", fmt.Errorf("retrieval %s capture: query %s response holds %d newlines, want exactly the one that terminates a single line-delimited message; a pretty-printed or concatenated capture is not the measured object", QrelBlindSmokeEvaluationName, queryID, n)
	}
	var envelope candidateResponseEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", fmt.Errorf("retrieval %s capture: query %s response is not a JSON-RPC message: %w", QrelBlindSmokeEvaluationName, queryID, err)
	}
	if envelope.JSONRPC != "2.0" {
		return "", fmt.Errorf("retrieval %s capture: query %s response jsonrpc=%q", QrelBlindSmokeEvaluationName, queryID, envelope.JSONRPC)
	}
	// The capture sends exactly one request, with id 1. A response carrying
	// any other id is a reply to a different call, and pairing a bundle with
	// the wrong question is the one substitution the byte checks above cannot
	// see. One server instance per request means this cannot bite today; it is
	// one line, and the day it can bite it will be silent otherwise.
	if string(envelope.ID) != candidateRequestID {
		return "", fmt.Errorf("retrieval %s capture: query %s response answers request id %s, want %s; a response to a different request is not this query's bundle", QrelBlindSmokeEvaluationName, queryID, string(envelope.ID), candidateRequestID)
	}
	if len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		return "", fmt.Errorf("retrieval %s capture: query %s response carries a JSON-RPC error: %s", QrelBlindSmokeEvaluationName, queryID, string(envelope.Error))
	}
	if envelope.Result == nil {
		return "", fmt.Errorf("retrieval %s capture: query %s response carries no result", QrelBlindSmokeEvaluationName, queryID)
	}
	if envelope.Result.IsError {
		return "", fmt.Errorf("retrieval %s capture: query %s tool call reported isError", QrelBlindSmokeEvaluationName, queryID)
	}
	if len(envelope.Result.Content) != 1 || envelope.Result.Content[0].Type != "text" {
		return "", fmt.Errorf("retrieval %s capture: query %s response content is not exactly one text block", QrelBlindSmokeEvaluationName, queryID)
	}
	var result contract.Result
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &result); err != nil {
		return "", fmt.Errorf("retrieval %s capture: query %s inner bundle is not a contract result: %w", QrelBlindSmokeEvaluationName, queryID, err)
	}
	if !strings.HasPrefix(result.Summary, taskctx.MethodVersionV2+":") {
		return "", fmt.Errorf("retrieval %s capture: query %s bundle summary %q does not attest %s; a /1 bundle is a different candidate", QrelBlindSmokeEvaluationName, queryID, result.Summary, taskctx.MethodVersionV2)
	}
	if !strings.Contains(result.Summary, "degradation: ready") {
		return "", fmt.Errorf("retrieval %s capture: query %s bundle summary %q does not attest ready retrieval", QrelBlindSmokeEvaluationName, queryID, result.Summary)
	}
	return result.Summary, nil
}

// preserveCandidatePayload stores the captured bytes through the SAME
// PayloadLedger the comparator uses, so both arms are preserved and recounted
// by one implementation, and then re-checks the result through the measurement
// contract's own payload validation.
func preserveCandidatePayload(queryID string, raw []byte, real PayloadCounter) (PreservedPayload, error) {
	ledger := &PayloadLedger{}
	ledger.capture(PayloadBoundaryCandidate, PayloadOperationTaskContext, raw)
	payloads, err := ledger.PreservedPayloads(real)
	if err != nil {
		return PreservedPayload{}, fmt.Errorf("retrieval %s capture: query %s: %w", QrelBlindSmokeEvaluationName, queryID, err)
	}
	if len(payloads) != 1 {
		return PreservedPayload{}, fmt.Errorf("retrieval %s capture: query %s preserved %d payloads, want exactly 1", QrelBlindSmokeEvaluationName, queryID, len(payloads))
	}
	payload := payloads[0]
	requiredCounters := map[string]string{
		TokenizerID:      "",
		real.TokenizerID: real.VocabularySHA256,
	}
	counters := map[string]PayloadCounter{real.TokenizerID: real}
	if _, err := validatePreservedPayload(queryID, "candidate", payload, 1, PayloadBoundaryCandidate, requiredCounters, counters); err != nil {
		return PreservedPayload{}, err
	}
	return payload, nil
}
