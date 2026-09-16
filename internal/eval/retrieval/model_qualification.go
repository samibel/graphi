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
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	taskcompact "github.com/samibel/graphi/engine/agenttools/taskctx/compact"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/embed/coderank"
	"github.com/samibel/graphi/engine/embed/static"
	engineretrieval "github.com/samibel/graphi/engine/retrieval"
	"github.com/samibel/graphi/engine/search"
)

const (
	// QualificationSchemaVersion is the only preregistration schema accepted by
	// the embedded-model qualification gate.
	QualificationSchemaVersion = 1

	QualificationCompactVersion   = "compact/17"
	QualificationTokenBudget      = 1200
	QualificationBootstrapSamples = 100000

	QualificationMinPasses                     = 56
	QualificationMinPairedGain                 = 9
	QualificationMinWeakStrataWithPositiveGain = 2
	QualificationBootstrapConfidence           = 0.95
	QualificationMaxSidecarRSSBytes            = int64(2 << 30)
	QualificationMaxArtifactBytes              = int64(1 << 30)
	QualificationMaxQueryP95Millis             = int64(1000)
	QualificationMinQuerySamples               = 100
	QualificationMaxReindexSeconds             = int64(600)
)

var spentQualificationDatasetIDs = map[string]bool{
	"cobra-v1":        true,
	"cobra-v2":        true,
	"fixture-v1":      true,
	"grpc-go-perf-v1": true,
	"cobra-fresh-sealed-holdout-v5-2026-09-13":        true,
	"cobra-second-fresh-sealed-holdout-v5-2026-09-13": true,
	"cobra-compact16-fresh-sealed-holdout-2026-09-15": true,
	"cobra-v2-third-fresh-sealed-holdout":             true,
	"cobra-compact17-fresh-unseen-v2":                 true,
	"cobra-compact17-fresh-unseen-v3":                 true,
	"cobra-compact17-fresh-unseen-v4":                 true,
}

var spentQualificationDatasetSHA256 = map[string]bool{
	"be604ff7b17db5c35b0c63ddbb5d758633535e81e6771858ff860c724fb50d82": true,
	"7de5ce6eef0e58d952b64ea7beaa0b09d158724b1eaa52bbd064ceebf53f35fc": true,
	"671324c375af0e4ba0582378e1901ac53d461e95310ef9b91c8faf7cdbed2d2e": true,
	"bdac5107251b7c40bf85fa9fc9eaaed75ad642ef4005ce846850976b893381aa": true,
	"9f2289c71bbc8515bd58b0210ddcf528eb5be427896918b3e5a3d390ad10d5aa": true,
	"331bd256c3097c61b34b3ccbc7716a161a315c76f2a41e22c4a4f88085eb85ae": true,
	"8b194e4bb2bec052458d77e0e49e86269d7b1a0d4357c1f68f26009c455d3562": true,
	"a96cfa7d002dad127ff0a1fe7bdffed2515615c7e4cd14fb66dba45fffcf0190": true,
	"fb7737442cd251c30f6800d557e3b89c600dd0a66374daf53837aa0144d8936c": true,
	"c985a0b5e43cf42c6bf37eee1752cf0f01450e0baeecab7b5888817bb55551aa": true,
	"c4115b6331e46f1a8c0b0daad020e86e4826a51de8e06a68e3211dea0770a3a6": true,
}

var qualificationStratumCounts = map[string]int{
	StratumAmbiguous:        10,
	StratumArchitectureFlow: 11,
	StratumConfigDocs:       10,
	StratumExactIdentifier:  11,
	StratumExactPath:        11,
	StratumNLBehaviour:      11,
}

// QualificationArm is one immutable comparison arm in the embedded-model
// development qualification.
type QualificationArm string

const (
	ArmLexical    QualificationArm = "M0_lexical"
	ArmPotion512  QualificationArm = "M1_potion_512"
	ArmPotion8192 QualificationArm = "M2_potion_8192"
	ArmCodeRank   QualificationArm = "M3_coderank"
)

// QualificationPreregistration freezes every input that may affect the paired
// development comparison before any result is opened.
type QualificationPreregistration struct {
	SchemaVersion       int                         `json:"schema_version"`
	DatasetSHA256       string                      `json:"dataset_sha256"`
	SourceRepoSHA       string                      `json:"source_repo_sha"`
	CandidateSHA        string                      `json:"candidate_sha"`
	CandidateDiffSHA256 string                      `json:"candidate_diff_sha256"`
	ReaderPromptSHA256  string                      `json:"reader_prompt_sha256"`
	GraderPromptSHA256  string                      `json:"grader_prompt_sha256"`
	Arms                map[QualificationArm]ArmPin `json:"arms"`
	CompactVersion      string                      `json:"compact_version"`
	TokenBudget         int                         `json:"token_budget"`
	BootstrapSamples    int                         `json:"bootstrap_samples"`
	BootstrapSeed       uint64                      `json:"bootstrap_seed"`
	Thresholds          QualificationThresholds     `json:"thresholds"`
	ReferenceMachine    ReferenceMachine            `json:"reference_machine"`
}

// ArmPin binds one arm to its exact semantic identity. The lexical arm carries
// only its label; every semantic arm carries all four embedding pins.
type ArmPin struct {
	Label                string `json:"label"`
	EmbedderID           string `json:"embedder_id,omitempty"`
	FingerprintCanonical string `json:"fingerprint_canonical,omitempty"`
	ManifestSHA256       string `json:"manifest_sha256,omitempty"`
	AdmissionSHA256      string `json:"admission_sha256,omitempty"`
}

// QualificationThresholds is the complete immutable promotion and operating
// budget gate.
type QualificationThresholds struct {
	MinPasses                     int     `json:"min_passes"`
	MinPairedGain                 int     `json:"min_paired_gain"`
	MinWeakStrataWithPositiveGain int     `json:"min_weak_strata_with_positive_gain"`
	BootstrapConfidence           float64 `json:"bootstrap_confidence"`
	MaxSidecarRSSBytes            int64   `json:"max_sidecar_rss_bytes"`
	MaxArtifactBytes              int64   `json:"max_artifact_bytes"`
	MaxQueryP95Millis             int64   `json:"max_query_p95_millis"`
	MinQuerySamples               int     `json:"min_query_samples"`
	MaxReindexSeconds             int64   `json:"max_reindex_seconds"`
}

// ReferenceMachine freezes the CPU-only operating-budget environment.
type ReferenceMachine struct {
	OS             string `json:"os"`
	OSVersion      string `json:"os_version"`
	CPU            string `json:"cpu"`
	PhysicalCores  int    `json:"physical_cores"`
	RuntimeThreads int    `json:"runtime_threads"`
	BackgroundLoad string `json:"background_load"`
}

// StageHit records whether a grade-3 answer span appeared at a fixed ranking
// boundary and, when it did, its one-based best rank.
type StageHit struct {
	Present  bool `json:"present"`
	BestRank int  `json:"best_rank"`
}

// QualificationIntMetric distinguishes a measured zero from a metric the
// active embedder protocol cannot expose. Qualification gates must inspect
// Available rather than interpreting the Go zero value as evidence.
type QualificationIntMetric struct {
	Available bool   `json:"available"`
	Value     *int   `json:"value,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// QualificationBoolMetric is the equivalent representation for a measured
// predicate such as an all-zero query vector.
type QualificationBoolMetric struct {
	Available bool  `json:"available"`
	Value     *bool `json:"value,omitempty"`
}

// QualificationBuildDiagnostics contains index-build aggregates. These are
// deliberately not copied onto each query observation.
type QualificationBuildDiagnostics struct {
	AdmissionTruncations int `json:"admission_truncations"`
	DocumentZeroVectors  int `json:"document_zero_vectors"`
}

// QualificationObservation is one immutable arm/query observation. Candidate
// and bundle production complete before the grade-3 spans are consulted to
// populate the three evaluation-only stage fields.
type QualificationObservation struct {
	Arm                QualificationArm        `json:"arm"`
	QueryID            string                  `json:"query_id"`
	Stratum            string                  `json:"stratum"`
	SemanticTop50      StageHit                `json:"semantic_top_50"`
	PostFusion         StageHit                `json:"post_fusion"`
	CompleteGrade3Span bool                    `json:"complete_grade_3_span"`
	BundleSHA256       string                  `json:"bundle_sha256"`
	PayloadSHA256      string                  `json:"payload_sha256"`
	BundleTokens       int                     `json:"bundle_tokens"`
	UnknownTokens      QualificationIntMetric  `json:"unknown_tokens"`
	QueryVectorAllZero QualificationBoolMetric `json:"query_vector_all_zero"`
	RetrievalState     string                  `json:"retrieval_state"`
	ModelFingerprint   string                  `json:"model_fingerprint"`
	IndexFingerprint   string                  `json:"index_fingerprint"`
	Degraded           bool                    `json:"degraded"`
}

// QualificationBuildDigest separates the independently reproducible byte
// classes. Staging generation IDs are intentionally absent; no other
// persisted field is excluded. Oracle payload bytes/digests and their real
// token counts have independent digests so either can invalidate publication.
type QualificationBuildDigest struct {
	Arm                     QualificationArm              `json:"arm"`
	VectorBytesSHA256       string                        `json:"vector_bytes_sha256"`
	PersistedRowsSHA256     string                        `json:"persisted_rows_sha256"`
	BundlesSHA256           string                        `json:"bundles_sha256"`
	TokenCountsSHA256       string                        `json:"token_counts_sha256"`
	OraclePayloadsSHA256    string                        `json:"oracle_payloads_sha256"`
	OracleTokenCountsSHA256 string                        `json:"oracle_token_counts_sha256"`
	Diagnostics             QualificationBuildDiagnostics `json:"diagnostics"`
}

type qualificationCaptureFacts struct {
	Arm                 QualificationArm
	Query               Query
	SemanticState       embed.State
	ExpectedFingerprint embed.Fingerprint
	IndexFingerprint    embed.Fingerprint
	SearchFingerprint   embed.Fingerprint
	ModelFingerprint    string
	Retrieval           engineretrieval.Result
	SemanticHits        []search.SemanticHit
	Payload             PreservedPayload
	Structured          taskcompact.Structured
	BundleBytes         []byte
	QueryVector         []float32
	UnknownTokens       QualificationIntMetric
}

// captureQualificationObservation validates every semantic-space identity
// before it accepts payload bytes into a qualification observation. Qrels are
// read only after the two unmodified candidate lists and payload already exist.
func captureQualificationObservation(f qualificationCaptureFacts) (QualificationObservation, error) {
	if strings.TrimSpace(f.Query.ID) == "" {
		return QualificationObservation{}, fmt.Errorf("embedded-model qualification capture: query id is required")
	}
	if f.Retrieval.Summary.RetrievalVersion != engineretrieval.Version || f.Retrieval.Summary.Limit != 50 {
		return QualificationObservation{}, fmt.Errorf("embedded-model qualification capture: query %s retrieval method is %s limit %d, want %s limit 50", f.Query.ID, f.Retrieval.Summary.RetrievalVersion, f.Retrieval.Summary.Limit, engineretrieval.Version)
	}

	expected := f.ExpectedFingerprint.Canonical()
	if f.Arm == ArmLexical {
		if f.ExpectedFingerprint != (embed.Fingerprint{}) || f.IndexFingerprint != (embed.Fingerprint{}) ||
			f.SearchFingerprint != (embed.Fingerprint{}) || f.ModelFingerprint != "" {
			return QualificationObservation{}, fmt.Errorf("embedded-model qualification capture: lexical control query %s carries loaded semantic identity", f.Query.ID)
		}
		if f.Retrieval.Degradation != engineretrieval.StateLexicalOnly || f.Retrieval.Summary.Strategy != "lexical_only" {
			return QualificationObservation{}, fmt.Errorf("embedded-model qualification capture: lexical control query %s is %s/%s, want recorded lexical_only control", f.Query.ID, f.Retrieval.Degradation, f.Retrieval.Summary.Strategy)
		}
		if f.Retrieval.Summary.ModelFingerprint != "" || f.Retrieval.Summary.IndexFingerprint != "" {
			return QualificationObservation{}, fmt.Errorf("embedded-model qualification capture: lexical control query %s carries a semantic fingerprint", f.Query.ID)
		}
	} else {
		if f.SemanticState != embed.StateReady {
			return QualificationObservation{}, fmt.Errorf("embedded-model qualification capture: query %s semantic state is %s, want ready", f.Query.ID, f.SemanticState)
		}
		for _, identity := range []struct{ name, value string }{
			{name: "loaded generation", value: f.IndexFingerprint.Canonical()},
			{name: "search request", value: f.SearchFingerprint.Canonical()},
			{name: "capture model", value: f.ModelFingerprint},
			{name: "retrieval model", value: f.Retrieval.Summary.ModelFingerprint},
			{name: "retrieval index", value: f.Retrieval.Summary.IndexFingerprint},
		} {
			if identity.value != expected {
				return QualificationObservation{}, fmt.Errorf("embedded-model qualification capture: query %s %s fingerprint does not equal preregistered canonical fingerprint", f.Query.ID, identity.name)
			}
		}
		if f.Retrieval.Degradation != engineretrieval.StateReady || f.Retrieval.Summary.Strategy != "semantic_first" {
			return QualificationObservation{}, fmt.Errorf("embedded-model qualification capture: query %s retrieval is %s/%s, want ready semantic_first", f.Query.ID, f.Retrieval.Degradation, f.Retrieval.Summary.Strategy)
		}
	}
	if len(f.Payload.Bytes) == 0 || f.Payload.SHA256 != SHA256Hex(f.Payload.Bytes) || f.Payload.ByteCount != len(f.Payload.Bytes) {
		return QualificationObservation{}, fmt.Errorf("embedded-model qualification capture: query %s payload bytes are not content-addressed", f.Query.ID)
	}
	bundleRaw := f.BundleBytes
	if bundleRaw == nil {
		var err error
		bundleRaw, err = json.Marshal(f.Structured)
		if err != nil {
			return QualificationObservation{}, fmt.Errorf("embedded-model qualification capture: query %s bundle: %w", f.Query.ID, err)
		}
	}
	bundleTokens := -1
	for _, count := range f.Payload.TokenCounts {
		if count.TokenizerID == TokenizerID {
			bundleTokens = count.Tokens
			break
		}
	}
	if bundleTokens < 0 || bundleTokens > QualificationTokenBudget {
		return QualificationObservation{}, fmt.Errorf("embedded-model qualification capture: query %s governed token count is %d, want 0..%d", f.Query.ID, bundleTokens, QualificationTokenBudget)
	}

	return QualificationObservation{
		Arm: f.Arm, QueryID: f.Query.ID, Stratum: f.Query.Stratum,
		SemanticTop50:      qualificationSemanticStage(f.Query, f.SemanticHits),
		PostFusion:         qualificationRetrievalStage(f.Query, f.Retrieval.Rows),
		CompleteGrade3Span: qualificationCompleteGrade3Span(f.Query, f.Structured.Sources),
		BundleSHA256:       SHA256Hex(bundleRaw), PayloadSHA256: f.Payload.SHA256, BundleTokens: bundleTokens,
		UnknownTokens: f.UnknownTokens, QueryVectorAllZero: qualificationAllZeroMetric(f.QueryVector),
		RetrievalState: string(f.Retrieval.Degradation), ModelFingerprint: f.Retrieval.Summary.ModelFingerprint,
		IndexFingerprint: f.Retrieval.Summary.IndexFingerprint, Degraded: false,
	}, nil
}

func qualificationAllZeroMetric(vector []float32) QualificationBoolMetric {
	if len(vector) == 0 {
		return QualificationBoolMetric{}
	}
	for _, value := range vector {
		if value != 0 {
			measured := false
			return QualificationBoolMetric{Available: true, Value: &measured}
		}
	}
	measured := true
	return QualificationBoolMetric{Available: true, Value: &measured}
}

func qualificationBuildDiagnostics(rows []embed.Row, admissionTruncations int) QualificationBuildDiagnostics {
	return QualificationBuildDiagnostics{
		AdmissionTruncations: admissionTruncations,
		DocumentZeroVectors:  qualificationZeroVectors(rows),
	}
}

func validateQualificationRunDiagnostics(arm QualificationArm, observations []QualificationObservation) error {
	if arm == ArmLexical {
		return nil
	}
	for _, observation := range observations {
		if !observation.QueryVectorAllZero.Available || observation.QueryVectorAllZero.Value == nil {
			return fmt.Errorf("embedded-model qualification capture: query %s all-zero vector diagnostic is unavailable", observation.QueryID)
		}
		if !observation.UnknownTokens.Available || observation.UnknownTokens.Value == nil {
			return fmt.Errorf("embedded-model qualification capture: query %s unknown-token diagnostic is unavailable", observation.QueryID)
		}
	}
	return nil
}

func qualificationSemanticStage(q Query, hits []search.SemanticHit) StageHit {
	for i, hit := range hits {
		for _, judgement := range q.Judgements {
			if judgement.Grade == GradeMax && SpanMatches(hit.SourcePath, hit.Line, judgement) {
				return StageHit{Present: true, BestRank: i + 1}
			}
		}
	}
	return StageHit{}
}

func qualificationRetrievalStage(q Query, rows []engineretrieval.Row) StageHit {
	for i, row := range rows {
		line := taskContextLineFromSpan(row.Span)
		for _, judgement := range q.Judgements {
			if judgement.Grade == GradeMax && SpanMatches(row.Path, line, judgement) {
				return StageHit{Present: true, BestRank: i + 1}
			}
		}
	}
	return StageHit{}
}

func qualificationCompleteGrade3Span(q Query, sources []taskcompact.Source) bool {
	for _, judgement := range q.Judgements {
		if judgement.Grade != GradeMax {
			continue
		}
		complete := true
		for line := judgement.StartLine; line <= judgement.EndLine; line++ {
			covered := false
			for _, source := range sources {
				if SpanMatches(source.Path, line, judgement) && line >= source.StartLine && line <= source.EndLine {
					covered = true
					break
				}
			}
			if !covered {
				complete = false
				break
			}
		}
		if complete {
			return true
		}
	}
	return false
}

func validateQualificationCaptureBinding(arm QualificationArm, pre QualificationPreregistration, expected embed.Fingerprint, manifestBytes []byte) error {
	if err := ValidateQualificationPreregistration(pre); err != nil {
		return err
	}
	pin, ok := pre.Arms[arm]
	if !ok {
		return fmt.Errorf("embedded-model qualification capture: arm %s is not preregistered", arm)
	}
	if arm == ArmLexical {
		if expected != (embed.Fingerprint{}) {
			return fmt.Errorf("embedded-model qualification capture: lexical arm has an expected semantic fingerprint")
		}
		return nil
	}
	if pin.FingerprintCanonical != expected.Canonical() {
		return fmt.Errorf("embedded-model qualification capture: arm %s expected fingerprint does not equal preregistration", arm)
	}
	if arm == ArmCodeRank {
		if len(manifestBytes) == 0 || SHA256Hex(manifestBytes) != pin.ManifestSHA256 {
			return fmt.Errorf("embedded-model qualification capture: CodeRank manifest bytes do not equal preregistered manifest_sha256")
		}
	}
	return nil
}

type qualificationBuildInputs struct {
	Rows              []embed.Row
	AdmittedDocuments []embed.SemanticDocument
	QueryVectors      map[string][]float32
	Payloads          []PreservedPayload
	OracleControls    map[string]OracleControls
}

func buildQualificationDigest(arm QualificationArm, in qualificationBuildInputs) QualificationBuildDigest {
	rows := append([]embed.Row(nil), in.Rows...)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].NodeID != rows[j].NodeID {
			return rows[i].NodeID < rows[j].NodeID
		}
		return rows[i].DocumentID < rows[j].DocumentID
	})
	var vectors, persisted, bundles, tokens, oraclePayloads, oracleTokens bytes.Buffer
	documents := append([]embed.SemanticDocument(nil), in.AdmittedDocuments...)
	sort.Slice(documents, func(i, j int) bool {
		if documents[i].NodeID != documents[j].NodeID {
			return documents[i].NodeID < documents[j].NodeID
		}
		return documents[i].DocumentID < documents[j].DocumentID
	})
	for _, document := range documents {
		raw, _ := json.Marshal(document)
		qualificationWriteString(&persisted, "admitted_document")
		qualificationWriteBytes(&persisted, raw)
		qualificationWriteString(&tokens, "admitted_document")
		qualificationWriteString(&tokens, string(document.NodeID))
		_ = binary.Write(&tokens, binary.BigEndian, int64(document.AdmissionTokenCount))
		_ = binary.Write(&tokens, binary.BigEndian, int64(document.AdmissionLimit))
	}
	for _, row := range rows {
		qualificationWriteString(&persisted, "persisted_row")
		qualificationWriteString(&persisted, string(row.NodeID))
		qualificationWriteString(&persisted, row.DocumentID)
		qualificationWriteString(&persisted, row.TextHash)
		qualificationWriteString(&persisted, row.Path)
		_ = binary.Write(&persisted, binary.BigEndian, int64(row.StartLine))
		_ = binary.Write(&persisted, binary.BigEndian, int64(row.EndLine))
		qualificationWriteString(&persisted, row.SpanMethod)
		qualificationWriteVector(&persisted, row.Vector)
		qualificationWriteString(&vectors, "document")
		qualificationWriteString(&vectors, string(row.NodeID))
		qualificationWriteVector(&vectors, row.Vector)
	}
	queryIDs := make([]string, 0, len(in.QueryVectors))
	for id := range in.QueryVectors {
		queryIDs = append(queryIDs, id)
	}
	sort.Strings(queryIDs)
	for _, id := range queryIDs {
		qualificationWriteString(&vectors, "query")
		qualificationWriteString(&vectors, id)
		qualificationWriteVector(&vectors, in.QueryVectors[id])
	}
	for _, payload := range in.Payloads {
		qualificationWriteBytes(&bundles, payload.Bytes)
		qualificationWriteString(&bundles, payload.SHA256)
		counts := append([]PayloadTokenCount(nil), payload.TokenCounts...)
		sort.Slice(counts, func(i, j int) bool { return counts[i].TokenizerID < counts[j].TokenizerID })
		for _, count := range counts {
			qualificationWriteString(&tokens, "payload")
			qualificationWriteString(&tokens, count.TokenizerID)
			qualificationWriteString(&tokens, count.VocabularySHA256)
			_ = binary.Write(&tokens, binary.BigEndian, int64(count.Tokens))
		}
	}
	oracleQueryIDs := make([]string, 0, len(in.OracleControls))
	for queryID := range in.OracleControls {
		oracleQueryIDs = append(oracleQueryIDs, queryID)
	}
	sort.Strings(oracleQueryIDs)
	for _, queryID := range oracleQueryIDs {
		for _, control := range oracleControlBundles(in.OracleControls[queryID]) {
			qualificationWriteString(&oraclePayloads, queryID)
			qualificationWriteString(&oraclePayloads, control.ControlKind)
			qualificationWriteString(&oraclePayloads, control.CandidateProvenance)
			qualificationWriteString(&oraclePayloads, control.CandidateSHA256)
			qualificationWriteBytes(&oraclePayloads, control.Payload.Bytes)
			qualificationWriteString(&oraclePayloads, control.Payload.SHA256)
			qualificationWriteString(&oracleTokens, queryID)
			qualificationWriteString(&oracleTokens, control.ControlKind)
			_ = binary.Write(&oracleTokens, binary.BigEndian, int64(control.TokenCount))
			counts := append([]PayloadTokenCount(nil), control.Payload.TokenCounts...)
			sort.Slice(counts, func(i, j int) bool { return counts[i].TokenizerID < counts[j].TokenizerID })
			for _, count := range counts {
				qualificationWriteString(&oracleTokens, count.TokenizerID)
				qualificationWriteString(&oracleTokens, count.VocabularySHA256)
				_ = binary.Write(&oracleTokens, binary.BigEndian, int64(count.Tokens))
			}
		}
	}
	return QualificationBuildDigest{
		Arm: arm, VectorBytesSHA256: SHA256Hex(vectors.Bytes()), PersistedRowsSHA256: SHA256Hex(persisted.Bytes()),
		BundlesSHA256: SHA256Hex(bundles.Bytes()), TokenCountsSHA256: SHA256Hex(tokens.Bytes()),
		OraclePayloadsSHA256: SHA256Hex(oraclePayloads.Bytes()), OracleTokenCountsSHA256: SHA256Hex(oracleTokens.Bytes()),
	}
}

func compareQualificationBuildDigests(first, second QualificationBuildDigest) error {
	if first.Arm != second.Arm {
		return fmt.Errorf("embedded-model qualification reproducibility: arm changed from %s to %s", first.Arm, second.Arm)
	}
	for _, digest := range []struct{ name, first, second string }{
		{"vector bytes", first.VectorBytesSHA256, second.VectorBytesSHA256},
		{"persisted rows", first.PersistedRowsSHA256, second.PersistedRowsSHA256},
		{"bundles", first.BundlesSHA256, second.BundlesSHA256},
		{"token counts", first.TokenCountsSHA256, second.TokenCountsSHA256},
		{"oracle payloads", first.OraclePayloadsSHA256, second.OraclePayloadsSHA256},
		{"oracle token counts", first.OracleTokenCountsSHA256, second.OracleTokenCountsSHA256},
	} {
		if digest.first != digest.second {
			return fmt.Errorf("embedded-model qualification reproducibility: arm %s %s digest differs across independent builds", first.Arm, digest.name)
		}
	}
	if first.Diagnostics != second.Diagnostics {
		return fmt.Errorf("embedded-model qualification reproducibility: arm %s build diagnostics differ across independent builds", first.Arm)
	}
	return nil
}

func qualificationWriteString(buf *bytes.Buffer, value string) {
	qualificationWriteBytes(buf, []byte(value))
}

func qualificationWriteBytes(buf *bytes.Buffer, value []byte) {
	_ = binary.Write(buf, binary.BigEndian, uint64(len(value)))
	_, _ = buf.Write(value)
}

func qualificationWriteVector(buf *bytes.Buffer, vector []float32) {
	_ = binary.Write(buf, binary.BigEndian, uint64(len(vector)))
	for _, value := range vector {
		_ = binary.Write(buf, binary.BigEndian, math.Float32bits(value))
	}
}

type qualificationEnvironment struct {
	Repo, Dataset, Preregistration, CodeRankManifest, Out, StaticModelDir string
}

func qualificationEnvironmentFromOS() qualificationEnvironment {
	return qualificationEnvironment{
		Repo: os.Getenv("GRAPHI_QUALIFICATION_REPO"), Dataset: os.Getenv("GRAPHI_QUALIFICATION_DATASET"),
		Preregistration: os.Getenv("GRAPHI_QUALIFICATION_PREREGISTRATION"), CodeRankManifest: os.Getenv("GRAPHI_CODERANK_MANIFEST"),
		Out: os.Getenv("GRAPHI_QUALIFICATION_OUT"), StaticModelDir: os.Getenv("GRAPHI_STATIC_MODEL_DIR"),
	}
}

func runEmbeddedModelQualificationCapture(ctx context.Context, env qualificationEnvironment) error {
	for _, field := range []struct{ name, value string }{
		{"repository", env.Repo}, {"dataset", env.Dataset}, {"preregistration", env.Preregistration},
		{"CodeRank manifest", env.CodeRankManifest}, {"output", env.Out}, {"static model directory", env.StaticModelDir},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("embedded-model qualification capture: %s is required", field.name)
		}
	}
	entries, err := os.ReadDir(env.Out)
	if err != nil {
		return fmt.Errorf("embedded-model qualification capture: read output directory: %w", err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("embedded-model qualification capture: output directory %s is not empty", env.Out)
	}
	loaded, err := LoadDataset(env.Dataset)
	if err != nil {
		return err
	}
	if err := ValidateQualificationDataset(loaded); err != nil {
		return err
	}
	raw, err := os.ReadFile(env.Preregistration)
	if err != nil {
		return fmt.Errorf("embedded-model qualification capture: read preregistration: %w", err)
	}
	pre, err := decodeQualificationCapturePreregistration(raw)
	if err != nil {
		return fmt.Errorf("embedded-model qualification capture: parse preregistration: %w", err)
	}
	if pre.DatasetSHA256 != loaded.SHA256 {
		return fmt.Errorf("embedded-model qualification capture: dataset differs from preregistration")
	}
	manifestBytes, err := os.ReadFile(env.CodeRankManifest)
	if err != nil {
		return fmt.Errorf("embedded-model qualification capture: read CodeRank manifest: %w", err)
	}
	if SHA256Hex(manifestBytes) != pre.Arms[ArmCodeRank].ManifestSHA256 {
		return fmt.Errorf("embedded-model qualification capture: loaded CodeRank manifest bytes differ from preregistration")
	}
	// The live driver refuses to manufacture partial evidence. Arm construction,
	// capture, and two-build comparison are implemented by the test harness in a
	// single invocation; absence of the pinned model artifacts is an error.
	if _, err := os.Stat(filepath.Join(env.StaticModelDir, static.FileSafetensors)); err != nil {
		return fmt.Errorf("embedded-model qualification capture: pinned Potion artifact: %w", err)
	}
	candidateRoot, err := qualificationModuleRoot()
	if err != nil {
		return err
	}
	counter, err := LoadPinnedRealPayloadCounter()
	if err != nil {
		return fmt.Errorf("embedded-model qualification capture: load pinned payload tokenizer: %w", err)
	}
	checkoutSHA, err := CheckoutHEAD(ctx, env.Repo)
	if err != nil {
		return err
	}
	bindingOptions := CandidateBindingOptions{
		CandidateRoot: candidateRoot, FrozenCandidateSHA: pre.CandidateSHA,
		ExcludePath:  "docs/eval/retrieval/runs/embedded-model-qualification",
		CheckoutRoot: env.Repo, CheckoutSHA: checkoutSHA,
	}
	binding, err := ObserveCandidateBinding(ctx, GitRepoProbe(), bindingOptions)
	if err != nil {
		return err
	}
	return publishQualificationAtomically(env.Out, func(stage string) error {
		return captureQualificationBuilds(ctx, stage, env, loaded, pre, counter, bindingOptions, binding)
	})
}

func captureQualificationBuilds(ctx context.Context, out string, env qualificationEnvironment, loaded *Loaded, pre QualificationPreregistration, counter PayloadCounter, bindingOptions CandidateBindingOptions, binding CandidateBinding) error {
	arms := []QualificationArm{ArmLexical, ArmPotion512, ArmPotion8192, ArmCodeRank}
	first := make(map[QualificationArm]QualificationBuildDigest, len(arms))
	for build := 1; build <= 2; build++ {
		for _, arm := range arms {
			emb, expected, armManifest, err := qualificationArmEmbedder(ctx, arm, pre, env.CodeRankManifest)
			if err != nil {
				return err
			}
			armDir := filepath.Join(out, fmt.Sprintf("build-%d", build), string(arm))
			workDir := filepath.Join(armDir, "work")
			if err := os.MkdirAll(workDir, 0o755); err != nil {
				return fmt.Errorf("embedded-model qualification capture: create arm directory: %w", err)
			}
			opts := CandidateCaptureOptions{
				RepoRoot: env.Repo, RepoName: loaded.Dataset.Repo, RepoSHA: pre.SourceRepoSHA,
				Dataset: loaded, Queries: append([]Query(nil), loaded.Dataset.Queries...), EmbedderSelector: pre.Arms[arm].Label, WorkDir: workDir,
				RealCounter: counter, Log: io.Discard, Embedder: emb, ExpectedFingerprint: expected,
				QualificationArm: arm, QualificationPreregistration: &pre,
				Binding: bindingOptions, ObservedBinding: &binding,
			}
			if arm == ArmCodeRank {
				opts.ManifestBytes = armManifest
			}
			captured, provenance, err := CaptureCandidateBundles(ctx, opts)
			if err != nil {
				return fmt.Errorf("embedded-model qualification capture: build %d arm %s: %w", build, arm, err)
			}
			if provenance.QualificationBuildDigest == nil || len(captured) != len(loaded.Dataset.Queries) {
				return fmt.Errorf("embedded-model qualification capture: build %d arm %s produced incomplete evidence", build, arm)
			}
			observations := make([]QualificationObservation, 0, len(captured))
			for _, bundle := range captured {
				if bundle.Qualification == nil {
					return fmt.Errorf("embedded-model qualification capture: build %d arm %s query %s has no observation", build, arm, bundle.QueryID)
				}
				observations = append(observations, *bundle.Qualification)
			}
			if err := validateQualificationRunDiagnostics(arm, observations); err != nil {
				return err
			}
			if err := writeQualificationOracleControls(armDir, captured, loaded.Dataset.Queries); err != nil {
				return fmt.Errorf("embedded-model qualification capture: build %d arm %s: %w", build, arm, err)
			}
			artifact := struct {
				Build        int                        `json:"build"`
				Arm          QualificationArm           `json:"arm"`
				Provenance   CandidateCaptureProvenance `json:"provenance"`
				Digest       QualificationBuildDigest   `json:"digest"`
				Observations []QualificationObservation `json:"observations"`
				Bundles      []CapturedCandidateBundle  `json:"bundles"`
			}{build, arm, provenance, *provenance.QualificationBuildDigest, observations, captured}
			artifactBytes, err := json.MarshalIndent(artifact, "", "  ")
			if err != nil {
				return fmt.Errorf("embedded-model qualification capture: encode build %d arm %s: %w", build, arm, err)
			}
			artifactBytes = append(artifactBytes, '\n')
			if err := os.WriteFile(filepath.Join(armDir, "capture.json"), artifactBytes, 0o644); err != nil {
				return fmt.Errorf("embedded-model qualification capture: write build %d arm %s: %w", build, arm, err)
			}
			if build == 1 {
				first[arm] = *provenance.QualificationBuildDigest
			} else if err := compareQualificationBuildDigests(first[arm], *provenance.QualificationBuildDigest); err != nil {
				return err
			}
		}
	}
	return nil
}

func publishQualificationAtomically(out string, capture func(stage string) error) (err error) {
	entries, err := os.ReadDir(out)
	if err != nil {
		return fmt.Errorf("embedded-model qualification capture: read output directory: %w", err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("embedded-model qualification capture: output directory %s is not empty", out)
	}
	parent, base := filepath.Dir(out), filepath.Base(out)
	stage, err := os.MkdirTemp(parent, "."+base+".staging-")
	if err != nil {
		return fmt.Errorf("embedded-model qualification capture: create staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := capture(stage); err != nil {
		return err
	}
	if err := os.Remove(out); err != nil {
		return fmt.Errorf("embedded-model qualification capture: prepare atomic publish: %w", err)
	}
	if err := os.Rename(stage, out); err != nil {
		_ = os.Mkdir(out, 0o755)
		return fmt.Errorf("embedded-model qualification capture: atomic publish: %w", err)
	}
	return nil
}

func decodeQualificationCapturePreregistration(raw []byte) (QualificationPreregistration, error) {
	var pre QualificationPreregistration
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&pre); err != nil {
		return QualificationPreregistration{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			return QualificationPreregistration{}, fmt.Errorf("trailing JSON value")
		}
		return QualificationPreregistration{}, fmt.Errorf("trailing bytes: %w", err)
	}
	if err := ValidateQualificationPreregistration(pre); err != nil {
		return QualificationPreregistration{}, err
	}
	return pre, nil
}

func qualificationArmEmbedder(ctx context.Context, arm QualificationArm, pre QualificationPreregistration, manifestPath string) (embed.Embedder, *embed.Fingerprint, []byte, error) {
	if arm == ArmLexical {
		return nil, nil, nil, nil
	}
	pin, ok := pre.Arms[arm]
	if !ok {
		return nil, nil, nil, fmt.Errorf("embedded-model qualification capture: arm %s is not preregistered", arm)
	}
	fingerprint, err := qualificationFingerprintFromCanonical(pin.FingerprintCanonical)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("embedded-model qualification capture: arm %s fingerprint: %w", arm, err)
	}
	var emb embed.Embedder
	var loadedManifest []byte
	switch arm {
	case ArmPotion512:
		emb, err = static.New(static.PinnedModel + "@" + static.PinnedRevision)
	case ArmPotion8192:
		emb, err = static.NewForEvaluation(static.PinnedModel+"@"+static.PinnedRevision, 8192)
	case ArmCodeRank:
		loadedManifest, err = readStableQualificationManifest(manifestPath, pin.ManifestSHA256, func() error {
			var constructErr error
			emb, constructErr = coderank.NewFromManifest(ctx, manifestPath)
			return constructErr
		})
	default:
		return nil, nil, nil, fmt.Errorf("embedded-model qualification capture: unknown arm %s", arm)
	}
	if err != nil {
		return nil, nil, nil, fmt.Errorf("embedded-model qualification capture: construct arm %s: %w", arm, err)
	}
	return emb, &fingerprint, loadedManifest, nil
}

func readStableQualificationManifest(path, expectedSHA256 string, duringRead func() error) ([]byte, error) {
	before, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest before constructor: %w", err)
	}
	if SHA256Hex(before) != expectedSHA256 {
		return nil, fmt.Errorf("manifest before constructor differs from preregistered sha256")
	}
	if err := duringRead(); err != nil {
		return nil, err
	}
	after, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest after constructor: %w", err)
	}
	if !bytes.Equal(before, after) || SHA256Hex(after) != expectedSHA256 {
		return nil, fmt.Errorf("manifest changed while the CodeRank adapter was constructed")
	}
	return after, nil
}

func qualificationFingerprintFromCanonical(canonical string) (embed.Fingerprint, error) {
	fields, ok := decodeQualificationFingerprint(canonical)
	if !ok || len(fields) != 8 {
		return embed.Fingerprint{}, fmt.Errorf("invalid canonical fingerprint")
	}
	dim, err := strconv.Atoi(fields[4])
	if err != nil {
		return embed.Fingerprint{}, fmt.Errorf("invalid dimension: %w", err)
	}
	return embed.Fingerprint{
		ModelID: fields[0], Revision: fields[1], ModelSHA256: fields[2], TokenizerSHA256: fields[3],
		Dim: dim, DocumentSchema: fields[5], ChunkerConfig: fields[6], GraphGeneration: fields[7],
	}, nil
}

func qualificationModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("embedded-model qualification capture: working directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("embedded-model qualification capture: could not find module root")
		}
		dir = parent
	}
}

// ValidateQualificationDataset accepts exactly one fresh, holdout-shaped
// development population. A path is not an identity: spent evidence is
// rejected by its immutable dataset ID or byte digest even after a rename.
func ValidateQualificationDataset(loaded *Loaded) error {
	if loaded == nil || loaded.Dataset == nil {
		return fmt.Errorf("embedded-model qualification dataset is missing")
	}
	if len(loaded.Raw) == 0 {
		return fmt.Errorf("embedded-model qualification dataset raw bytes are missing")
	}
	if !isLowerHexDigest(loaded.SHA256, 64) {
		return fmt.Errorf("embedded-model qualification dataset sha256 must be 64 lowercase hex characters")
	}
	if observed := SHA256Hex(loaded.Raw); observed != loaded.SHA256 {
		return fmt.Errorf("embedded-model qualification dataset sha256 %s does not match raw bytes %s", loaded.SHA256, observed)
	}
	if spentQualificationDatasetIDs[strings.TrimSpace(loaded.Dataset.ID)] || spentQualificationDatasetSHA256[loaded.SHA256] {
		return fmt.Errorf("embedded-model qualification refuses spent holdout identity id=%q sha256=%q", loaded.Dataset.ID, loaded.SHA256)
	}
	if err := loaded.Dataset.Validate(); err != nil {
		return fmt.Errorf("embedded-model qualification dataset: %w", err)
	}
	if loaded.Dataset.RelevantMinGrade != GradeMax {
		return fmt.Errorf("embedded-model qualification relevant_min_grade is %d, want exact grade %d", loaded.Dataset.RelevantMinGrade, GradeMax)
	}
	if len(loaded.Dataset.Queries) != 64 {
		return fmt.Errorf("embedded-model qualification requires exactly 64 queries, got %d", len(loaded.Dataset.Queries))
	}

	counts := make(map[string]int, len(qualificationStratumCounts))
	families := make(map[string]string, len(loaded.Dataset.Queries))
	for _, query := range loaded.Dataset.Queries {
		if query.Split != SplitDev {
			return fmt.Errorf("embedded-model qualification query %q has split %q, want %q", query.ID, query.Split, SplitDev)
		}
		if _, known := qualificationStratumCounts[query.Stratum]; !known {
			return fmt.Errorf("embedded-model qualification query %q has forbidden stratum %q", query.ID, query.Stratum)
		}
		counts[query.Stratum]++
		family := strings.TrimSpace(query.FamilyID)
		if family == "" {
			return fmt.Errorf("embedded-model qualification query %q has blank family_id", query.ID)
		}
		if strings.TrimSpace(query.Provenance) == "" {
			return fmt.Errorf("embedded-model qualification query %q has blank provenance", query.ID)
		}
		if prior, exists := families[family]; exists {
			return fmt.Errorf("embedded-model qualification family %q is reused by queries %q and %q", family, prior, query.ID)
		}
		families[family] = query.ID
		hasGrade3 := false
		for _, judgement := range query.Judgements {
			if judgement.Grade == GradeMax {
				hasGrade3 = true
				break
			}
		}
		if !hasGrade3 {
			return fmt.Errorf("embedded-model qualification query %q has no grade-3 answer span", query.ID)
		}
	}
	for stratum, want := range qualificationStratumCounts {
		if got := counts[stratum]; got != want {
			return fmt.Errorf("embedded-model qualification stratum %s has %d queries, want %d", stratum, got, want)
		}
	}
	return nil
}

// ValidateQualificationPreregistration fails closed unless every binding,
// arm, promotion threshold and reference-machine field equals the approved
// experiment contract.
func ValidateQualificationPreregistration(pre QualificationPreregistration) error {
	if pre.SchemaVersion != QualificationSchemaVersion {
		return fmt.Errorf("embedded-model qualification schema_version is %d, want %d", pre.SchemaVersion, QualificationSchemaVersion)
	}
	for _, digest := range []struct {
		name  string
		value string
		size  int
	}{
		{name: "dataset_sha256", value: pre.DatasetSHA256, size: 64},
		{name: "source_repo_sha", value: pre.SourceRepoSHA, size: 40},
		{name: "candidate_sha", value: pre.CandidateSHA, size: 40},
		{name: "candidate_diff_sha256", value: pre.CandidateDiffSHA256, size: 64},
		{name: "reader_prompt_sha256", value: pre.ReaderPromptSHA256, size: 64},
		{name: "grader_prompt_sha256", value: pre.GraderPromptSHA256, size: 64},
	} {
		if !isLowerHexDigest(digest.value, digest.size) {
			return fmt.Errorf("embedded-model qualification %s must be %d lowercase hex characters", digest.name, digest.size)
		}
	}
	if spentQualificationDatasetSHA256[pre.DatasetSHA256] {
		return fmt.Errorf("embedded-model qualification preregistration names spent holdout sha256 %s", pre.DatasetSHA256)
	}
	if pre.CompactVersion != QualificationCompactVersion {
		return fmt.Errorf("embedded-model qualification compact_version is %q, want %q", pre.CompactVersion, QualificationCompactVersion)
	}
	if pre.TokenBudget != QualificationTokenBudget {
		return fmt.Errorf("embedded-model qualification token_budget is %d, want %d", pre.TokenBudget, QualificationTokenBudget)
	}
	if pre.BootstrapSamples != QualificationBootstrapSamples {
		return fmt.Errorf("embedded-model qualification bootstrap_samples is %d, want %d", pre.BootstrapSamples, QualificationBootstrapSamples)
	}
	if pre.BootstrapSeed == 0 {
		return fmt.Errorf("embedded-model qualification bootstrap_seed must be non-zero")
	}
	if err := validateQualificationArms(pre.Arms); err != nil {
		return err
	}
	if err := validateQualificationThresholds(pre.Thresholds); err != nil {
		return err
	}
	return validateQualificationReferenceMachine(pre.ReferenceMachine)
}

func validateQualificationArms(arms map[QualificationArm]ArmPin) error {
	required := []QualificationArm{ArmLexical, ArmPotion512, ArmPotion8192, ArmCodeRank}
	if len(arms) != len(required) {
		return fmt.Errorf("embedded-model qualification requires exactly four arms, got %d", len(arms))
	}
	fingerprints := make(map[QualificationArm][]string, len(required)-1)
	for _, arm := range required {
		pin, exists := arms[arm]
		if !exists {
			return fmt.Errorf("embedded-model qualification arm %s is missing", arm)
		}
		if pin.Label != string(arm) {
			return fmt.Errorf("embedded-model qualification arm %s label is %q", arm, pin.Label)
		}
		if arm == ArmLexical {
			if pin.EmbedderID != "" || pin.FingerprintCanonical != "" || pin.ManifestSHA256 != "" || pin.AdmissionSHA256 != "" {
				return fmt.Errorf("embedded-model qualification lexical arm must not carry embedding pins")
			}
			continue
		}
		if strings.TrimSpace(pin.EmbedderID) == "" {
			return fmt.Errorf("embedded-model qualification arm %s embedder_id is required", arm)
		}
		if err := validateQualificationFingerprint(pin.FingerprintCanonical, pin.EmbedderID); err != nil {
			return fmt.Errorf("embedded-model qualification arm %s fingerprint: %w", arm, err)
		}
		fingerprints[arm], _ = decodeQualificationFingerprint(pin.FingerprintCanonical)
		if !isLowerHexDigest(pin.ManifestSHA256, 64) {
			return fmt.Errorf("embedded-model qualification arm %s manifest_sha256 must be 64 lowercase hex characters", arm)
		}
		if !isLowerHexDigest(pin.AdmissionSHA256, 64) {
			return fmt.Errorf("embedded-model qualification arm %s admission_sha256 must be 64 lowercase hex characters", arm)
		}
	}
	if err := validatePotionQualificationArms(arms, fingerprints); err != nil {
		return err
	}
	return validateCodeRankQualificationArm(arms, fingerprints)
}

func validateQualificationFingerprint(canonical, embedderID string) error {
	fields, ok := decodeQualificationFingerprint(canonical)
	if !ok || len(fields) != 8 {
		return fmt.Errorf("must be a complete eight-field canonical fingerprint")
	}
	if fields[0] != embedderID {
		return fmt.Errorf("model id %q does not match embedder_id %q", fields[0], embedderID)
	}
	if strings.TrimSpace(fields[1]) == "" {
		return fmt.Errorf("revision is required")
	}
	if !isLowerHexDigest(fields[2], 64) || !isLowerHexDigest(fields[3], 64) {
		return fmt.Errorf("model and tokenizer digests must be complete lowercase sha256 values")
	}
	dim, err := strconv.Atoi(fields[4])
	if err != nil || dim <= 0 || strconv.Itoa(dim) != fields[4] {
		return fmt.Errorf("dimension %q must be a canonical positive integer", fields[4])
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "document schema", value: fields[5]},
		{name: "graph generation", value: fields[7]},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s is required", field.name)
		}
	}
	return nil
}

func validatePotionQualificationArms(arms map[QualificationArm]ArmPin, fingerprints map[QualificationArm][]string) error {
	for _, arm := range []struct {
		name      QualificationArm
		maxTokens int
	}{
		{name: ArmPotion512, maxTokens: static.DefaultMaxLength},
		{name: ArmPotion8192, maxTokens: 8192},
	} {
		pin := arms[arm.name]
		fields := fingerprints[arm.name]
		modelID, admissionSHA := qualificationPotionIdentity(arm.maxTokens)
		if pin.EmbedderID != modelID || pin.AdmissionSHA256 != admissionSHA {
			return fmt.Errorf("embedded-model qualification arm %s does not match the pinned Potion/%d identity and admission profile", arm.name, arm.maxTokens)
		}
		if fields[0] != modelID || fields[1] != static.PinnedRevision ||
			fields[2] != static.PinnedSHA256[static.FileSafetensors] ||
			fields[3] != static.PinnedSHA256[static.FileTokenizer] ||
			fields[4] != "256" || fields[5] != embed.DocumentSchema || fields[6] != "" {
			return fmt.Errorf("embedded-model qualification arm %s fingerprint is not the pinned Potion/%d space", arm.name, arm.maxTokens)
		}
	}
	if arms[ArmPotion512].ManifestSHA256 != arms[ArmPotion8192].ManifestSHA256 {
		return fmt.Errorf("embedded-model qualification Potion arms must share one pinned artifact manifest")
	}
	if arms[ArmPotion512].AdmissionSHA256 == arms[ArmPotion8192].AdmissionSHA256 ||
		arms[ArmPotion512].FingerprintCanonical == arms[ArmPotion8192].FingerprintCanonical {
		return fmt.Errorf("embedded-model qualification Potion/512 and Potion/8192 profiles must be distinct")
	}
	if fingerprints[ArmPotion512][7] != fingerprints[ArmPotion8192][7] {
		return fmt.Errorf("embedded-model qualification Potion arms must name the same graph generation")
	}
	return nil
}

func qualificationPotionIdentity(maxTokens int) (modelID, admissionSHA string) {
	profile := embed.AdmissionSpec{
		TokenizerID:      "model2vec-wordpiece",
		TokenizerSHA256:  static.PinnedSHA256[static.FileTokenizer],
		TokenizerVersion: "1.0",
		MaxTokens:        maxTokens,
		Reserve:          static.SpecialTokenReserve,
		Algorithm:        "first-n-tokens",
		AlgorithmVersion: "1",
	}
	admissionSHA = SHA256Hex([]byte(profile.String()))
	contract := "embedeach-f16-tree"
	if maxTokens != static.DefaultMaxLength {
		contract += "-eval-max-" + strconv.Itoa(maxTokens)
	}
	modelID = static.PinnedSelector + ":" + static.PinnedSHA256[static.FileSafetensors][:12] +
		":mean:" + strconv.FormatBool(static.PinnedNormalize) + ":" + static.PinnedSHA256[static.FileTokenizer][:12] +
		":" + static.PinnedSHA256[static.FileConfig][:12] + ":" + contract + ":" + admissionSHA[:12]
	return modelID, admissionSHA
}

func validateCodeRankQualificationArm(arms map[QualificationArm]ArmPin, fingerprints map[QualificationArm][]string) error {
	pin := arms[ArmCodeRank]
	fields := fingerprints[ArmCodeRank]
	if fields[5] != embed.DocumentSchema || fields[7] != fingerprints[ArmPotion512][7] {
		return fmt.Errorf("embedded-model qualification CodeRank arm must use the common document schema and graph generation")
	}
	var manifest coderank.Manifest
	if err := json.Unmarshal([]byte(fields[6]), &manifest); err != nil {
		return fmt.Errorf("embedded-model qualification CodeRank durable profile: %w", err)
	}
	if manifest.Endpoint != "" {
		return fmt.Errorf("embedded-model qualification CodeRank durable profile must exclude its relocatable endpoint")
	}
	canonical, err := json.Marshal(manifest)
	if err != nil || string(canonical) != fields[6] {
		return fmt.Errorf("embedded-model qualification CodeRank durable profile is not canonical or contains unknown fields")
	}
	manifest.Endpoint = "http://127.0.0.1"
	if err := manifest.Validate(); err != nil {
		return fmt.Errorf("embedded-model qualification CodeRank durable profile: %w", err)
	}
	if manifest.Model.ID != "nomic-ai/CodeRankEmbed" || manifest.Tokenizer.ID != manifest.Model.ID {
		return fmt.Errorf("embedded-model qualification CodeRank arm must pin the CodeRankEmbed model and tokenizer")
	}
	wantID := "coderank:" + manifest.Model.ID + "@" + manifest.Model.Revision + ":" + manifest.IdentityDigest()
	wantAdmissionSHA := SHA256Hex([]byte(manifest.AdmissionSpec().String()))
	if pin.EmbedderID != wantID || pin.AdmissionSHA256 != wantAdmissionSHA ||
		fields[0] != wantID || fields[1] != manifest.Model.Revision || fields[2] != manifest.Model.SHA256 ||
		fields[3] != manifest.Tokenizer.SHA256 || fields[4] != strconv.Itoa(manifest.Dimension) {
		return fmt.Errorf("embedded-model qualification CodeRank arm does not match its pinned durable profile")
	}
	if pin.ManifestSHA256 == arms[ArmPotion512].ManifestSHA256 ||
		pin.AdmissionSHA256 == arms[ArmPotion512].AdmissionSHA256 ||
		pin.AdmissionSHA256 == arms[ArmPotion8192].AdmissionSHA256 {
		return fmt.Errorf("embedded-model qualification CodeRank manifest and admission profile must be distinct from Potion")
	}
	return nil
}

func decodeQualificationFingerprint(canonical string) ([]string, bool) {
	if canonical == "" {
		return nil, false
	}
	var fields []string
	for pos := 0; pos < len(canonical); {
		colon := strings.IndexByte(canonical[pos:], ':')
		if colon <= 0 {
			return nil, false
		}
		colon += pos
		lengthText := canonical[pos:colon]
		for _, digit := range lengthText {
			if digit < '0' || digit > '9' {
				return nil, false
			}
		}
		length, err := strconv.Atoi(lengthText)
		if err != nil || length < 0 || strconv.Itoa(length) != lengthText {
			return nil, false
		}
		start := colon + 1
		end := start + length
		if end < start || end > len(canonical) {
			return nil, false
		}
		fields = append(fields, canonical[start:end])
		if end == len(canonical) {
			return fields, true
		}
		if canonical[end] != '\n' {
			return nil, false
		}
		pos = end + 1
	}
	return nil, false
}

func validateQualificationThresholds(got QualificationThresholds) error {
	want := QualificationThresholds{
		MinPasses:                     QualificationMinPasses,
		MinPairedGain:                 QualificationMinPairedGain,
		MinWeakStrataWithPositiveGain: QualificationMinWeakStrataWithPositiveGain,
		BootstrapConfidence:           QualificationBootstrapConfidence,
		MaxSidecarRSSBytes:            QualificationMaxSidecarRSSBytes,
		MaxArtifactBytes:              QualificationMaxArtifactBytes,
		MaxQueryP95Millis:             QualificationMaxQueryP95Millis,
		MinQuerySamples:               QualificationMinQuerySamples,
		MaxReindexSeconds:             QualificationMaxReindexSeconds,
	}
	if got != want {
		return fmt.Errorf("embedded-model qualification thresholds do not match the frozen promotion and operating-budget gates")
	}
	return nil
}

func validateQualificationReferenceMachine(machine ReferenceMachine) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "os", value: machine.OS},
		{name: "os_version", value: machine.OSVersion},
		{name: "cpu", value: machine.CPU},
		{name: "background_load", value: machine.BackgroundLoad},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("embedded-model qualification reference_machine.%s is required", field.name)
		}
	}
	if machine.PhysicalCores <= 0 {
		return fmt.Errorf("embedded-model qualification reference_machine.physical_cores must be positive")
	}
	if machine.RuntimeThreads <= 0 {
		return fmt.Errorf("embedded-model qualification reference_machine.runtime_threads must be positive")
	}
	return nil
}
