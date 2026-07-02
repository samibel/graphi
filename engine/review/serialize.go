package review

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
)

// PublishResult is the versioned, byte-stable contract emitted over MCP(stdio)
// and CLI (and consumed by SW-043). It carries the gate decision, the rendered
// body (which itself contains the hidden sticky marker), and — when a publish
// occurred — the upsert result. When the run is a dry-run (offline, default),
// Upsert is nil and only render + gate are reported.
type PublishResult struct {
	SchemaVersion         int    `json:"schema_version"`
	WriterVersion         string `json:"writer_version"`
	ConfigHash            string `json:"config_hash"`
	IdentitySchemaVersion uint32 `json:"identity_schema_version"`
	// Outcome mirrors the sibling reports' status field: "found" when at least one
	// resolved finding (risk region / signal / question) was rendered, else
	// "empty" (the comment still renders a valid "no findings" body). Lets SW-043
	// cheaply distinguish "ran, nothing to report" from a populated result.
	Outcome string        `json:"outcome"`
	PR      string        `json:"pr,omitempty"`
	Gate    GateDecision  `json:"gate"`
	Body    string        `json:"body"`
	Upsert  *UpsertResult `json:"upsert,omitempty"`
}

// Outcome values — the enumerated publish status (mirrors query.OutcomeFound /
// OutcomeEmpty used by the sibling reports).
const (
	OutcomeFound = "found"
	OutcomeEmpty = "empty"
)

// configHash fingerprints the combined render + gate config so a stored result
// can be tied to the exact models that produced it (mirrors the sibling
// weights_hash / config_hash discipline).
func configHash(rc renderConfig, gc GateConfig) string {
	b, _ := json.Marshal(struct {
		Render renderConfig `json:"render"`
		Gate   GateConfig   `json:"gate"`
	}{rc, gc})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// findingsSource is the injectable seam through which the publisher consumes the
// SW-039 RiskReport, SW-040 SignalReport, and SW-041 QuestionReport RESULTS for a
// PR diff — WITHOUT recomputing them and without hard-coupling beyond the three
// sibling reports. The default implementation wraps the shared analysis.Service;
// unit tests inject a stub so render/gate/upsert are exercised with fixed reports
// (no network, no analyzers re-run).
type findingsSource interface {
	Bundle(ctx context.Context, pr, diff, provenance string) (FindingsBundle, error)
}

// analysisFindingsSource wraps the shared analysis.Service and consumes the three
// sibling analyzers ONCE each for the given diff. It RUNS them (consuming their
// versioned reports) but the publisher never re-implements scoring, signals, or
// question generation — that logic stays in engine/analysis.
type analysisFindingsSource struct {
	svc *analysis.Service
}

// Bundle dispatches pr-risk, pr-signals, and pr-questions for the diff and
// assembles their reports. Each analyzer is dispatched once.
func (s analysisFindingsSource) Bundle(ctx context.Context, pr, diff, provenance string) (FindingsBundle, error) {
	if s.svc == nil {
		return FindingsBundle{}, fmt.Errorf("review: nil analysis service")
	}
	p := analysis.Params{Diff: diff, Provenance: provenance}

	riskA, err := s.svc.Dispatch(ctx, analysis.PriskAnalyzerName, p)
	if err != nil {
		return FindingsBundle{}, fmt.Errorf("review: dispatch pr-risk: %w", err)
	}
	sigA, err := s.svc.Dispatch(ctx, analysis.PrSignalsAnalyzerName, p)
	if err != nil {
		return FindingsBundle{}, fmt.Errorf("review: dispatch pr-signals: %w", err)
	}
	qA, err := s.svc.Dispatch(ctx, analysis.PrQuestionsAnalyzerName, p)
	if err != nil {
		return FindingsBundle{}, fmt.Errorf("review: dispatch pr-questions: %w", err)
	}

	bundle := FindingsBundle{PR: pr}
	if riskA.RiskReport != nil {
		bundle.Risk = *riskA.RiskReport
	}
	if sigA.SignalReport != nil {
		bundle.Signals = *sigA.SignalReport
	}
	if qA.QuestionReport != nil {
		bundle.Questions = *qA.QuestionReport
	}
	return bundle, nil
}

// Service is graphi's single PR-comment publisher service. It holds the injectable
// findings seam plus the fixed render/gate config; it is stateless per call and
// safe for concurrent use (the host is passed per call). It is the ONE place the
// render + gate + upsert pipeline is composed, so CLI and MCP stay in parity.
type Service struct {
	source findingsSource
	render renderConfig
}

// NewService builds the production publisher over the shared analysis.Service.
func NewService(analysisSvc *analysis.Service) *Service {
	return &Service{
		source: analysisFindingsSource{svc: analysisSvc},
		render: defaultRenderConfig,
	}
}

// newServiceWithSource builds a publisher over an injected findings seam (tests).
func newServiceWithSource(src findingsSource) *Service {
	return &Service{source: src, render: defaultRenderConfig}
}

// PublishOptions carry the per-call inputs the surfaces pass through verbatim.
type PublishOptions struct {
	PR         string     // PR reference (rendered in the header)
	Diff       string     // local-first PR diff (untrusted; bounded/sanitized by the consumed analyzers)
	Provenance string     // evidence redaction passed to the risk scorer ("summary" recommended for public comments)
	Gate       GateConfig // optional merge gate (Enabled=false => always PASS)
	Publish    bool       // when true, upsert the body through host; when false (default), dry-run (render+gate only)
}

// Publish runs the full pipeline for a PR diff: consume the three reports once
// (seam), render the deterministic body, evaluate the optional gate, and — when
// opts.Publish is true — upsert the body through the mockable host. It returns the
// versioned PublishResult. When opts.Publish is false (the offline default), the
// host is never touched and Upsert is nil. The provided host may be nil for a
// dry-run.
func (s *Service) Publish(ctx context.Context, host CommentHost, opts PublishOptions) (PublishResult, error) {
	bundle, err := s.source.Bundle(ctx, opts.PR, opts.Diff, opts.Provenance)
	if err != nil {
		return PublishResult{}, err
	}
	return s.publishBundle(ctx, host, bundle, opts)
}

// publishBundle is the core pipeline over an already-consumed bundle. Split out so
// it can be unit-tested directly with a fixed bundle (mirrors the sibling
// analyzers' score/detect/generate split).
func (s *Service) publishBundle(ctx context.Context, host CommentHost, bundle FindingsBundle, opts PublishOptions) (PublishResult, error) {
	gate := Evaluate(opts.Gate, bundle)
	body := Render(bundle, s.render, &gate)

	result := PublishResult{
		SchemaVersion:         PublishSchemaVersion,
		WriterVersion:         WriterVersion,
		ConfigHash:            configHash(s.render, opts.Gate),
		IdentitySchemaVersion: model.IdentitySchemaVersion,
		Outcome:               bundleOutcome(bundle),
		PR:                    bundle.PR,
		Gate:                  gate,
		Body:                  body,
	}

	if opts.Publish {
		up, err := Upsert(ctx, host, body)
		if err != nil {
			return PublishResult{}, err
		}
		result.Upsert = &up
	}
	return result, nil
}

// bundleOutcome reports "found" when the consumed bundle carries at least one
// resolved finding (a non-degraded risk region, a signal-bearing region, or a
// reviewer question), else "empty". Degraded-only / no-finding bundles still
// render a valid comment but report "empty" so SW-043 can branch cheaply.
func bundleOutcome(b FindingsBundle) string {
	for _, rec := range b.Risk.Regions {
		if !rec.Degraded && rec.Region != "" {
			return OutcomeFound
		}
	}
	for _, rec := range b.Signals.Regions {
		if !rec.Degraded && len(rec.Signals) > 0 {
			return OutcomeFound
		}
	}
	if len(b.Questions.Questions) > 0 {
		return OutcomeFound
	}
	return OutcomeEmpty
}

// Marshal is the single canonical serializer for a PublishResult, shared by every
// surface (CLI, MCP). It disables HTML escaping (the body is Markdown with HTML
// comments — escaping would corrupt the marker), trims the trailing newline, and
// materializes never-null slices — byte-for-byte stable across runs and surfaces
// (mirrors analysis.Marshal / MarshalRisk / MarshalSignals / MarshalQuestions).
func Marshal(res PublishResult) ([]byte, error) {
	out := res
	if out.Gate.Evidence == nil {
		out.Gate.Evidence = []GateEvidence{}
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, fmt.Errorf("review: marshal publish result: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
