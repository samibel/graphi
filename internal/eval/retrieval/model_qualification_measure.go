package retrieval

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/embed/coderank"
)

type MeasureQualificationOptions struct {
	PreregistrationPath string
	DatasetPath         string
	ManifestPath        string
	RepoRoot            string
	CandidateRoot       string
	WorkDir             string
	OutputPath          string
	BackgroundProtocol  string
	BackgroundMetadata  string
}

type qualificationOperatingEmbedder interface {
	embed.Embedder
	embed.DiagnosticQueryEmbedder
	embed.RuntimeAttestor
	OperatingAttestation(context.Context) (coderank.OperatingAttestation, error)
}

type operatingMeasureOptions struct {
	pre        QualificationPreregistration
	dataset    *Loaded
	repoRoot   string
	workDir    string
	outputPath string
	background OperatingBackgroundLoad
}

type operatingReindexResult struct {
	admittedDocuments    []embed.SemanticDocument
	embedded             int
	reused               int
	fingerprintCanonical string
	state                embed.State
	flushed              bool
	closed               bool
	durableReady         bool
}

type operatingMeasureDeps struct {
	embedder         qualificationOperatingEmbedder
	machine          ReferenceMachine
	sourceTreeSHA256 string
	now              func() time.Time
	reindex          func(context.Context, string, string, embed.Embedder) (operatingReindexResult, error)
	reindexResult    operatingReindexResult
	writeOutput      bool
	candidateStart   CandidateBinding
	candidateEnd     CandidateBinding
}

func MeasureQualificationOperating(ctx context.Context, options MeasureQualificationOptions) (OperatingEvidence, error) {
	var pre QualificationPreregistration
	if err := decodeQualificationJSONFile(options.PreregistrationPath, &pre); err != nil {
		return OperatingEvidence{}, err
	}
	if err := ValidateQualificationPreregistration(pre); err != nil {
		return OperatingEvidence{}, err
	}
	dataset, err := loadStrictQualificationDataset(options.DatasetPath)
	if err != nil {
		return OperatingEvidence{}, err
	}
	if dataset.SHA256 != pre.DatasetSHA256 {
		return OperatingEvidence{}, fmt.Errorf("embedded-model qualification measure: dataset differs from preregistration")
	}
	var evidence OperatingEvidence
	startBinding, endBinding, err := withQualificationMeasurementBindings(ctx, options.CandidateRoot, options.RepoRoot, pre, func(snapshot, sourceDigest string) error {
		var sidecar *coderank.Embedder
		if _, manifestErr := readStableQualificationManifest(options.ManifestPath, pre.Arms[ArmCodeRank].ManifestSHA256, func() error {
			var constructErr error
			sidecar, constructErr = coderank.NewFromManifest(ctx, options.ManifestPath)
			return constructErr
		}); manifestErr != nil {
			return fmt.Errorf("embedded-model qualification measure: %w", manifestErr)
		}
		machine, probeErr := probeQualificationReferenceMachine()
		if probeErr != nil {
			return probeErr
		}
		var measureErr error
		evidence, measureErr = measureQualificationOperating(ctx, operatingMeasureOptions{
			pre: pre, dataset: dataset, repoRoot: snapshot, workDir: options.WorkDir, outputPath: options.OutputPath,
			background: OperatingBackgroundLoad{Declaration: pre.ReferenceMachine.BackgroundLoad, Protocol: options.BackgroundProtocol, Metadata: options.BackgroundMetadata},
		}, operatingMeasureDeps{embedder: sidecar, machine: machine, sourceTreeSHA256: sourceDigest, now: time.Now, reindex: qualificationOperatingReindex})
		return measureErr
	})
	if err != nil {
		return OperatingEvidence{}, err
	}
	evidence.CandidateBindingStart = startBinding
	evidence.CandidateBindingEnd = endBinding
	evidence, err = SealOperatingEvidence(evidence)
	if err != nil {
		return OperatingEvidence{}, err
	}
	if err := WriteOperatingEvidence(options.OutputPath, evidence, pre, dataset); err != nil {
		return OperatingEvidence{}, err
	}
	return evidence, nil
}

func measureQualificationOperating(ctx context.Context, options operatingMeasureOptions, deps operatingMeasureDeps) (OperatingEvidence, error) {
	if deps.embedder == nil || deps.now == nil || strings.TrimSpace(options.outputPath) == "" {
		return OperatingEvidence{}, fmt.Errorf("embedded-model qualification measure: incomplete dependencies or output")
	}
	if err := ValidateQualificationPreregistration(options.pre); err != nil {
		return OperatingEvidence{}, err
	}
	if err := ValidateQualificationDataset(options.dataset); err != nil || options.dataset.SHA256 != options.pre.DatasetSHA256 {
		return OperatingEvidence{}, fmt.Errorf("embedded-model qualification measure: invalid or unpinned dataset: %w", err)
	}
	if len(options.dataset.Dataset.Queries) != 64 {
		return OperatingEvidence{}, fmt.Errorf("embedded-model qualification measure: dataset has %d queries, want 64", len(options.dataset.Dataset.Queries))
	}
	if err := prepareQualificationMeasureWorkDir(options.workDir); err != nil {
		return OperatingEvidence{}, err
	}
	if _, err := os.Lstat(options.outputPath); err == nil || !os.IsNotExist(err) {
		return OperatingEvidence{}, fmt.Errorf("embedded-model qualification measure: output path must not exist")
	}
	start, err := deps.embedder.OperatingAttestation(ctx)
	if err != nil {
		return OperatingEvidence{}, fmt.Errorf("embedded-model qualification measure: start attestation: %w", err)
	}
	deps.machine.RuntimeThreads = start.RuntimeThreads
	deps.machine.BackgroundLoad = options.background.Declaration
	if !reflectReferenceMachine(deps.machine, options.pre.ReferenceMachine) {
		return OperatingEvidence{}, fmt.Errorf("embedded-model qualification measure: observed reference machine differs from preregistration")
	}
	reindexStart := deps.now()
	reindex := deps.reindexResult
	if deps.reindex != nil {
		reindex, err = deps.reindex(ctx, options.repoRoot, options.workDir, deps.embedder)
		if err != nil {
			return OperatingEvidence{}, err
		}
	}
	reindexElapsed := deps.now().Sub(reindexStart)
	if reindexElapsed <= 0 {
		return OperatingEvidence{}, fmt.Errorf("embedded-model qualification measure: non-positive reindex duration")
	}
	if len(reindex.admittedDocuments) != 768 || reindex.embedded != 768 || reindex.reused != 0 ||
		reindex.fingerprintCanonical != options.pre.Arms[ArmCodeRank].FingerprintCanonical || reindex.state != embed.StateReady ||
		!reindex.flushed || !reindex.closed || !reindex.durableReady {
		return OperatingEvidence{}, fmt.Errorf("embedded-model qualification measure: reindex is not a fresh exact 768-document durable M3 build")
	}
	corpusDigest, err := qualificationCorpusDigest(reindex.admittedDocuments)
	if err != nil {
		return OperatingEvidence{}, err
	}

	warmups := make([]OperatingWarmup, 0, 64)
	for i, query := range options.dataset.Dataset.Queries {
		if _, err := qualificationMeasuredQuery(ctx, deps.embedder, query.Text); err != nil {
			return OperatingEvidence{}, fmt.Errorf("embedded-model qualification measure: warmup %d: %w", i, err)
		}
		warmups = append(warmups, OperatingWarmup{Ordinal: i, QueryID: query.ID, QueryTextSHA256: SHA256Hex([]byte(query.Text))})
	}
	samples := make([]OperatingQuerySample, 0, 128)
	for pass := 0; pass < 2; pass++ {
		for i, query := range options.dataset.Dataset.Queries {
			before := deps.now()
			result, err := qualificationMeasuredQuery(ctx, deps.embedder, query.Text)
			latency := deps.now().Sub(before)
			if err != nil {
				return OperatingEvidence{}, fmt.Errorf("embedded-model qualification measure: sample %d: %w", len(samples), err)
			}
			if latency <= 0 {
				return OperatingEvidence{}, fmt.Errorf("embedded-model qualification measure: sample %d has non-positive latency", len(samples))
			}
			samples = append(samples, OperatingQuerySample{Ordinal: pass*64 + i, QueryID: query.ID,
				QueryTextSHA256: SHA256Hex([]byte(query.Text)), LatencyNS: int64(latency),
				VectorSHA256: qualificationVectorSHA256(result.Vectors[0]), UnknownTokens: *result.UnknownTokens})
		}
	}
	end, err := deps.embedder.OperatingAttestation(ctx)
	if err != nil {
		return OperatingEvidence{}, fmt.Errorf("embedded-model qualification measure: end attestation: %w", err)
	}
	preSHA, err := qualificationPreregistrationDigest(options.pre)
	if err != nil {
		return OperatingEvidence{}, err
	}
	evidence, err := SealOperatingEvidence(OperatingEvidence{
		SchemaVersion: QualificationOperatingEvidenceSchemaVersion, PreregistrationSHA256: preSHA,
		DatasetSHA256: options.dataset.SHA256, SourceRepoSHA: options.pre.SourceRepoSHA,
		CandidateSHA: options.pre.CandidateSHA, CandidateDiffSHA256: options.pre.CandidateDiffSHA256,
		CandidateBindingStart: deps.candidateStart, CandidateBindingEnd: deps.candidateEnd,
		ManifestSHA256: options.pre.Arms[ArmCodeRank].ManifestSHA256, FingerprintCanonical: options.pre.Arms[ArmCodeRank].FingerprintCanonical,
		StartAttestation: start, EndAttestation: end, Machine: deps.machine, BackgroundLoad: options.background,
		SourceTreeSHA256: deps.sourceTreeSHA256, CorpusSHA256: corpusDigest,
		Reindex: OperatingReindexEvidence{FreshEmptyWorkDir: true, AdmittedDocuments: len(reindex.admittedDocuments),
			FingerprintCanonical: reindex.fingerprintCanonical, State: reindex.state.String(), Flushed: reindex.flushed,
			Closed: reindex.closed, DurableReady: reindex.durableReady, ElapsedNS: int64(reindexElapsed)},
		Warmups: warmups, Samples: samples,
	})
	if err != nil {
		return OperatingEvidence{}, err
	}
	if deps.writeOutput {
		if err := WriteOperatingEvidence(options.outputPath, evidence, options.pre, options.dataset); err != nil {
			return OperatingEvidence{}, err
		}
	}
	return evidence, nil
}

func qualificationMeasuredQuery(ctx context.Context, emb qualificationOperatingEmbedder, text string) (embed.QueryEmbedding, error) {
	result, err := embed.EmbedQueryWithDiagnostics(ctx, emb, text)
	if err != nil {
		return embed.QueryEmbedding{}, err
	}
	if len(result.Vectors) != 1 || len(result.Vectors[0]) != emb.Dim() || result.UnknownTokens == nil || *result.UnknownTokens < 0 {
		return embed.QueryEmbedding{}, fmt.Errorf("query embedding has incomplete vector or diagnostics")
	}
	for _, value := range result.Vectors[0] {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return embed.QueryEmbedding{}, fmt.Errorf("query embedding has non-finite vector value")
		}
	}
	return result, nil
}

func qualificationVectorSHA256(vector []float32) string {
	var raw bytes.Buffer
	for _, value := range vector {
		_ = binary.Write(&raw, binary.BigEndian, math.Float32bits(value))
	}
	return SHA256Hex(raw.Bytes())
}

func qualificationCorpusDigest(documents []embed.SemanticDocument) (string, error) {
	canonical := append([]embed.SemanticDocument(nil), documents...)
	sort.Slice(canonical, func(i, j int) bool {
		if canonical[i].NodeID != canonical[j].NodeID {
			return canonical[i].NodeID < canonical[j].NodeID
		}
		return canonical[i].DocumentID < canonical[j].DocumentID
	})
	var framed bytes.Buffer
	for _, document := range canonical {
		raw, err := json.Marshal(document)
		if err != nil {
			return "", fmt.Errorf("embedded-model qualification measure: encode admitted document: %w", err)
		}
		qualificationWriteBytes(&framed, raw)
	}
	return SHA256Hex(framed.Bytes()), nil
}

func prepareQualificationMeasureWorkDir(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("embedded-model qualification measure: workdir is required")
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err := os.Mkdir(path, 0o755); err != nil {
			return fmt.Errorf("embedded-model qualification measure: create fresh workdir: %w", err)
		}
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("embedded-model qualification measure: workdir must be a real empty directory")
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		return fmt.Errorf("embedded-model qualification measure: workdir must be empty")
	}
	return nil
}

func reflectReferenceMachine(got, want ReferenceMachine) bool {
	return got == want
}

func qualificationSourceTreeDigest(ctx context.Context, root, expectedHEAD string) (string, error) {
	head, err := CheckoutHEAD(ctx, root)
	if err != nil || head != expectedHEAD {
		return "", fmt.Errorf("embedded-model qualification measure: source checkout HEAD differs: got %q want %q: %w", head, expectedHEAD, err)
	}
	if err := exec.CommandContext(ctx, "git", "-C", root, "symbolic-ref", "-q", "HEAD").Run(); err == nil {
		return "", fmt.Errorf("embedded-model qualification measure: source checkout must be detached")
	}
	status, err := exec.CommandContext(ctx, "git", "-C", root, "status", "--porcelain=v1", "--untracked-files=all").Output()
	if err != nil || len(status) != 0 {
		return "", fmt.Errorf("embedded-model qualification measure: source checkout must be clean and private")
	}
	listed, err := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		return "", fmt.Errorf("embedded-model qualification measure: list source files: %w", err)
	}
	var framed bytes.Buffer
	for _, relBytes := range bytes.Split(listed, []byte{0}) {
		if len(relBytes) == 0 {
			continue
		}
		rel := string(relBytes)
		path := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return "", fmt.Errorf("embedded-model qualification measure: tracked source %s is not a regular file", rel)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		qualificationWriteString(&framed, rel)
		qualificationWriteBytes(&framed, raw)
	}
	return SHA256Hex(framed.Bytes()), nil
}

func withQualificationMeasurementBindings(ctx context.Context, candidateRoot, sourceRepository string, pre QualificationPreregistration, callback func(snapshot, sourceDigest string) error) (CandidateBinding, CandidateBinding, error) {
	if strings.TrimSpace(candidateRoot) == "" || strings.TrimSpace(sourceRepository) == "" || callback == nil {
		return CandidateBinding{}, CandidateBinding{}, fmt.Errorf("embedded-model qualification measure: candidate root, source repository, and callback are required")
	}
	var start, end CandidateBinding
	err := withDetachedQualificationCheckout(ctx, sourceRepository, pre.SourceRepoSHA, func(snapshot string) error {
		bindingOptions := CandidateBindingOptions{CandidateRoot: candidateRoot, FrozenCandidateSHA: pre.CandidateSHA,
			ExcludePath: QualificationCandidateExcludedPath, ExpectedCandidateDiffSHA256: pre.CandidateDiffSHA256,
			CheckoutRoot: snapshot, CheckoutSHA: pre.SourceRepoSHA}
		var err error
		start, err = ObserveCandidateBinding(ctx, GitRepoProbe(), bindingOptions)
		if err != nil {
			return fmt.Errorf("embedded-model qualification measure: candidate start binding: %w", err)
		}
		before, err := qualificationSourceTreeDigest(ctx, snapshot, pre.SourceRepoSHA)
		if err != nil {
			return err
		}
		if err := callback(snapshot, before); err != nil {
			return err
		}
		afterDigest, err := qualificationSourceTreeDigest(ctx, snapshot, pre.SourceRepoSHA)
		if err != nil || afterDigest != before {
			return fmt.Errorf("embedded-model qualification measure: private source snapshot digest changed: %w", err)
		}
		end, err = ObserveCandidateBinding(ctx, GitRepoProbe(), bindingOptions)
		if err != nil {
			return fmt.Errorf("embedded-model qualification measure: candidate end binding: %w", err)
		}
		if !reflect.DeepEqual(start, end) {
			return fmt.Errorf("embedded-model qualification measure: candidate binding changed during measurement")
		}
		return nil
	})
	return start, end, err
}

func qualificationOperatingReindex(ctx context.Context, repoRoot, workDir string, emb embed.Embedder) (operatingReindexResult, error) {
	// The frozen query schedule starts after reindex. Validate readiness from
	// persisted state below instead of issuing the default builder's query probe.
	idx, err := buildTaskContextIndexWithEmbedderOptions(ctx, repoRoot, workDir, emb, io.Discard, taskContextIndexBuildOptions{semanticReadinessProbe: false})
	if err != nil {
		return operatingReindexResult{}, err
	}
	state := idx.search.SemanticState()
	result := operatingReindexResult{admittedDocuments: idx.admittedDocuments, embedded: idx.generatedEmbedded, reused: idx.generatedReused,
		fingerprintCanonical: idx.fingerprint.Canonical(), state: state.State, flushed: true, durableReady: state.State == embed.StateReady}
	if err := idx.store.Close(); err != nil {
		return operatingReindexResult{}, fmt.Errorf("embedded-model qualification measure: close graph store: %w", err)
	}
	result.closed = true
	reopenedGraph, err := graphstore.OpenSQLite(filepath.Join(workDir, "task-context-eval.db"))
	if err != nil {
		return operatingReindexResult{}, fmt.Errorf("embedded-model qualification measure: reopen graph store: %w", err)
	}
	reopenedGeneration, err := embed.OpenSQLiteGenerationStore(ctx, filepath.Join(workDir, "task-context-eval-meta"))
	if err != nil {
		_ = reopenedGraph.Close()
		return operatingReindexResult{}, fmt.Errorf("embedded-model qualification measure: reopen generation store: %w", err)
	}
	generation, durableState, activeErr := reopenedGeneration.Active(ctx, idx.fingerprint, embed.NodeReferencerFromGraphLookup(reopenedGraph.GetNode))
	var rows []embed.Row
	var loadErr error
	if activeErr == nil && durableState == embed.StateReady && generation.ID != "" {
		rows, loadErr = reopenedGeneration.Load(ctx, generation.ID)
	}
	generationCloseErr := reopenedGeneration.Close()
	graphCloseErr := reopenedGraph.Close()
	if activeErr != nil || loadErr != nil || generationCloseErr != nil || graphCloseErr != nil {
		return operatingReindexResult{}, fmt.Errorf("embedded-model qualification measure: durable reopen validation failed: active=%v load=%v generation-close=%v graph-close=%v", activeErr, loadErr, generationCloseErr, graphCloseErr)
	}
	result.durableReady = durableState == embed.StateReady && generation.ID != "" && generation.Fingerprint.Canonical() == idx.fingerprint.Canonical() && len(rows) == 768
	return result, nil
}
