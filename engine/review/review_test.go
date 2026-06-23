package review

import (
	"bytes"
	"context"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
)

// fixedBundle builds a deterministic FindingsBundle covering resolved + degraded
// risk records, hub/bridge/surprise signals, and reviewer questions, so the
// render/gate/upsert paths are all exercised with stable data.
func fixedBundle() FindingsBundle {
	return FindingsBundle{
		PR: "owner/repo#42",
		Risk: analysis.RiskReport{
			SchemaVersion:         analysis.RiskSchemaVersion,
			ScorerVersion:         analysis.ScorerVersion,
			Outcome:               "found",
			IdentitySchemaVersion: model.IdentitySchemaVersion,
			Regions: []analysis.RiskRecord{
				{
					Region:          model.NodeId("aaaa1111bbbb2222"),
					Score:           "0.800",
					Confidence:      analysis.ConfidenceHigh,
					ProvenanceLevel: analysis.ProvenanceSummary,
					Evidence: []analysis.EvidenceItem{
						{Kind: analysis.EvidenceImpactBlastRadius, Reason: "blast radius of 12 dependent node(s) (bucket 4)"},
						{Kind: analysis.EvidenceTaintPath, Reason: "changed region lies on a taint path (length 3) [provenance redacted]"},
					},
				},
				{
					Region:          model.NodeId("cccc3333dddd4444"),
					Score:           "0.300",
					Confidence:      analysis.ConfidenceHigh,
					ProvenanceLevel: analysis.ProvenanceSummary,
					Evidence:        []analysis.EvidenceItem{{Kind: analysis.EvidenceImpactBlastRadius, Reason: "blast radius of 2 dependent node(s) (bucket 2)"}},
				},
				{
					Region:          "",
					Score:           "0.000",
					Degraded:        true,
					UnresolvedID:    "pkg/x.go:Gone",
					ProvenanceLevel: analysis.ProvenanceSummary,
					Evidence:        []analysis.EvidenceItem{{Kind: analysis.EvidenceUnresolved, Reason: "changed node could not be resolved to the indexed graph"}},
				},
			},
		},
		Signals: analysis.SignalReport{
			SchemaVersion:         analysis.SignalSchemaVersion,
			DetectorVersion:       analysis.DetectorVersion,
			Outcome:               "found",
			IdentitySchemaVersion: model.IdentitySchemaVersion,
			Regions: []analysis.SignalRecord{
				{
					Region:          model.NodeId("aaaa1111bbbb2222"),
					ProvenanceLevel: analysis.ProvenanceSummary,
					Signals: []analysis.SignalFlag{
						{Kind: analysis.SignalHub, Reason: "changed node has high fan-in/out (degree 9 >= threshold 3)", Score: "9"},
						{Kind: analysis.SignalSurprise, Detail: analysis.SurpriseUnexpectedCoupling, Reason: "changed region is unexpectedly coupled to 2 node(s) in other modules [provenance redacted]"},
					},
				},
				{
					Region:          model.NodeId("cccc3333dddd4444"),
					ProvenanceLevel: analysis.ProvenanceSummary,
					Signals:         []analysis.SignalFlag{},
				},
			},
		},
		Questions: analysis.QuestionReport{
			SchemaVersion:         analysis.QuestionSchemaVersion,
			GeneratorVersion:      analysis.GeneratorVersion,
			Outcome:               "found",
			IdentitySchemaVersion: model.IdentitySchemaVersion,
			Questions: []analysis.ReviewerQuestion{
				{
					RuleID: analysis.RuleHubCallers,
					Region: model.NodeId("aaaa1111bbbb2222"),
					Text:   "This changed symbol is a hub (high fan-in: degree 9). Many call sites depend on it — are all callers covered by this change?",
				},
				{
					RuleID: analysis.RuleRiskSurprise,
					Region: model.NodeId("aaaa1111bbbb2222"),
					Text:   "This changed region is high-risk (risk 0.800, above threshold 0.500) and carries a surprise signal (surprise-unexpected-coupling). Has this unexpected, risky change been reviewed with extra care?",
				},
			},
		},
	}
}

// stubSource is an injected findingsSource returning a fixed bundle (no analyzers
// re-run, no network) so the publisher pipeline is exercised offline.
type stubSource struct {
	bundle FindingsBundle
	calls  int
}

func (s *stubSource) Bundle(_ context.Context, pr, _, _ string) (FindingsBundle, error) {
	s.calls++
	b := s.bundle
	if pr != "" {
		b.PR = pr
	}
	return b, nil
}

// ---------------------------------------------------------------------------
// AC1 — sticky / idempotent upsert keyed on the marker.
// ---------------------------------------------------------------------------

func TestUpsertCreatesExactlyOneWhenAbsent(t *testing.T) {
	host := NewMockHost()
	body := Render(fixedBundle(), defaultRenderConfig, nil)

	res, err := Upsert(context.Background(), host, body)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if res.Action != ActionCreated {
		t.Fatalf("action = %q, want %q", res.Action, ActionCreated)
	}
	if host.Count() != 1 {
		t.Fatalf("comment count = %d, want 1", host.Count())
	}
	if host.Creates != 1 || host.Updates != 0 {
		t.Fatalf("creates=%d updates=%d, want creates=1 updates=0", host.Creates, host.Updates)
	}
}

func TestUpsertUpdatesInPlaceWhenMarkerPresent(t *testing.T) {
	host := NewMockHost()
	body := Render(fixedBundle(), defaultRenderConfig, nil)

	// First run creates exactly one sticky comment.
	first, err := Upsert(context.Background(), host, body)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Second run (different body but same marker) must UPDATE the SAME comment,
	// keyed on the marker's comment id — no duplicate.
	body2 := body + "\n<!-- changed -->\n"
	second, err := Upsert(context.Background(), host, body2)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	if second.Action != ActionUpdated {
		t.Fatalf("second action = %q, want %q", second.Action, ActionUpdated)
	}
	if second.CommentID != first.CommentID {
		t.Fatalf("update keyed on %q, want same id as create %q", second.CommentID, first.CommentID)
	}
	if host.Count() != 1 {
		t.Fatalf("comment count after re-run = %d, want exactly 1 (no duplicate)", host.Count())
	}
	if host.Updates != 1 {
		t.Fatalf("updates = %d, want 1", host.Updates)
	}
	if len(host.UpdatedIDs) != 1 || host.UpdatedIDs[0] != first.CommentID {
		t.Fatalf("UpdatedIDs = %v, want [%s]", host.UpdatedIDs, first.CommentID)
	}
	// The stored comment must carry the new body and still the marker.
	stored, ok := host.Get(second.CommentID)
	if !ok || stored.Body != body2 || !strings.Contains(stored.Body, StickyMarker) {
		t.Fatalf("stored comment not updated in place / lost marker")
	}
}

func TestUpsertIdempotentAcrossManyRuns(t *testing.T) {
	host := NewMockHost()
	body := Render(fixedBundle(), defaultRenderConfig, nil)
	for i := 0; i < 5; i++ {
		if _, err := Upsert(context.Background(), host, body); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if host.Count() != 1 {
		t.Fatalf("after 5 runs comment count = %d, want 1", host.Count())
	}
	if host.Creates != 1 {
		t.Fatalf("creates = %d, want exactly 1 across 5 runs", host.Creates)
	}
	if host.Updates != 4 {
		t.Fatalf("updates = %d, want 4 across 5 runs", host.Updates)
	}
}

func TestUpsertSeededExistingCommentIsUpdated(t *testing.T) {
	host := NewMockHost()
	// Simulate a prior sticky comment already on the PR.
	seededID := host.Seed("old body\n" + StickyMarker + "\nstale\n")
	body := Render(fixedBundle(), defaultRenderConfig, nil)

	res, err := Upsert(context.Background(), host, body)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if res.Action != ActionUpdated || res.CommentID != seededID {
		t.Fatalf("got action=%q id=%q, want updated id=%q", res.Action, res.CommentID, seededID)
	}
	if host.Count() != 1 {
		t.Fatalf("count = %d, want 1 (updated seeded comment, no duplicate)", host.Count())
	}
}

func TestUpsertRejectsBodyWithoutMarker(t *testing.T) {
	host := NewMockHost()
	if _, err := Upsert(context.Background(), host, "no marker here"); err == nil {
		t.Fatal("want error for body missing the sticky marker, got nil")
	}
	if host.Count() != 0 {
		t.Fatalf("count = %d, want 0 (nothing posted)", host.Count())
	}
}

// ---------------------------------------------------------------------------
// AC2 — merge gate PASS/BLOCK across boundary scores, evidence-linked BLOCK.
// ---------------------------------------------------------------------------

func TestGateTableDriven(t *testing.T) {
	// One region scoring "0.800" (= 800 units), one "0.300".
	bundle := fixedBundle()

	cases := []struct {
		name      string
		cfg       GateConfig
		want      string
		wantEvid  bool
	}{
		{"disabled any score", GateConfig{Enabled: false, BlockThreshold: 100}, VerdictPass, false},
		{"below threshold", GateConfig{Enabled: true, BlockThreshold: 900}, VerdictPass, false},
		{"exactly at threshold (inclusive pass)", GateConfig{Enabled: true, BlockThreshold: 800}, VerdictPass, false},
		{"just above threshold", GateConfig{Enabled: true, BlockThreshold: 799}, VerdictBlock, true},
		{"well below worst", GateConfig{Enabled: true, BlockThreshold: 500}, VerdictBlock, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dec := Evaluate(tc.cfg, bundle)
			if dec.Verdict != tc.want {
				t.Fatalf("verdict = %q, want %q", dec.Verdict, tc.want)
			}
			if tc.wantEvid && len(dec.Evidence) == 0 {
				t.Fatalf("BLOCK must carry evidence, got none")
			}
			if !tc.wantEvid && len(dec.Evidence) != 0 {
				t.Fatalf("PASS must carry no evidence, got %d", len(dec.Evidence))
			}
		})
	}
}

func TestGateBlockReasonLinksEvidence(t *testing.T) {
	bundle := fixedBundle()
	dec := Evaluate(GateConfig{Enabled: true, BlockThreshold: 500}, bundle)
	if dec.Verdict != VerdictBlock {
		t.Fatalf("verdict = %q, want BLOCK", dec.Verdict)
	}
	// The offending region (0.800) must be named in the evidence with its
	// verbatim score and its signal kinds (hub + surprise).
	var found *GateEvidence
	for i := range dec.Evidence {
		if dec.Evidence[i].Region == model.NodeId("aaaa1111bbbb2222") {
			found = &dec.Evidence[i]
		}
	}
	if found == nil {
		t.Fatalf("offending region not in evidence: %+v", dec.Evidence)
	}
	if found.RiskScore != "0.800" {
		t.Fatalf("evidence risk score = %q, want verbatim 0.800", found.RiskScore)
	}
	if !containsAll(found.Signals, analysis.SignalHub, analysis.SignalSurprise) {
		t.Fatalf("evidence signals = %v, want hub+surprise", found.Signals)
	}
	// The region at 0.300 must NOT be an offender at this threshold.
	for _, ev := range dec.Evidence {
		if ev.Region == model.NodeId("cccc3333dddd4444") {
			t.Fatalf("low-risk region wrongly flagged as offender")
		}
	}
}

func TestGateNoScoredRegionsPasses(t *testing.T) {
	empty := FindingsBundle{}
	dec := Evaluate(GateConfig{Enabled: true, BlockThreshold: 100}, empty)
	if dec.Verdict != VerdictPass {
		t.Fatalf("verdict = %q, want PASS for empty bundle", dec.Verdict)
	}
}

func TestGateNegativeThresholdClamped(t *testing.T) {
	// A nonsensical negative threshold is clamped to 0: the audit string and the
	// compared value must agree. Every positive score then exceeds 0 -> BLOCK.
	dec := Evaluate(GateConfig{Enabled: true, BlockThreshold: -50}, fixedBundle())
	if dec.Threshold != "0.000" {
		t.Fatalf("threshold display = %q, want clamped 0.000", dec.Threshold)
	}
	if dec.Verdict != VerdictBlock {
		t.Fatalf("verdict = %q, want BLOCK (every positive score exceeds clamped 0)", dec.Verdict)
	}
}

func TestParseRenderFixedRoundTrip(t *testing.T) {
	for _, units := range []int{0, 1, 300, 500, 700, 800, 999, 1000} {
		s := renderFixed(units)
		got, ok := parseFixed(s)
		if !ok || got != units {
			t.Fatalf("roundtrip %d -> %q -> (%d,%v)", units, s, got, ok)
		}
	}
}

// ---------------------------------------------------------------------------
// AC3 — deterministic render + golden snapshot + mockable, no-network boundary.
// ---------------------------------------------------------------------------

func TestRenderContainsMarkerAndAllSections(t *testing.T) {
	body := Render(fixedBundle(), defaultRenderConfig, nil)
	if !strings.HasPrefix(body, StickyMarker) {
		t.Fatalf("body must START with the sticky marker for stable identity")
	}
	for _, want := range []string{"### Risk", "### Signals", "### Reviewer questions"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing section %q", want)
		}
	}
}

func TestRenderDeterministicByteIdentical(t *testing.T) {
	b1 := Render(fixedBundle(), defaultRenderConfig, nil)
	b2 := Render(fixedBundle(), defaultRenderConfig, nil)
	if b1 != b2 {
		t.Fatalf("render not deterministic:\n--- 1 ---\n%s\n--- 2 ---\n%s", b1, b2)
	}
}

func TestRenderGoldenSnapshot(t *testing.T) {
	gate := Evaluate(GateConfig{Enabled: true, BlockThreshold: 500}, fixedBundle())
	body := Render(fixedBundle(), defaultRenderConfig, &gate)

	golden := filepath.Join("testdata", "comment_golden.md")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with UPDATE_GOLDEN=1 to create): %v", err)
	}
	if body != string(want) {
		t.Fatalf("rendered body != golden snapshot.\n--- got ---\n%s\n--- want ---\n%s", body, string(want))
	}
}

// leakyBundle is a bundle whose risk evidence carries VERBATIM taint source/sink
// provenance (as it would arrive if a caller asked the scorer for "full"
// provenance). The publisher must NOT render these into a public comment by
// default — the renderer redacts independent of the upstream level.
func leakyBundle() FindingsBundle {
	b := fixedBundle()
	b.Risk.Regions[0].ProvenanceLevel = analysis.ProvenanceFull
	b.Risk.Regions[0].Evidence = []analysis.EvidenceItem{{
		Kind:   analysis.EvidenceTaintPath,
		Reason: "changed region lies on a source->sink taint path",
		Source: "secretSource@/internal/auth/token.go:42",
		Sink:   "secretSink@/internal/net/out.go:7",
		Steps:  []string{"s1", "s2"},
	}}
	return b
}

func TestRenderRedactsSensitiveProvenanceByDefault(t *testing.T) {
	if !defaultRenderConfig.Redact {
		t.Fatal("default render config must redact for a public comment")
	}
	// Default config redacts: even though the record carries verbatim Source/Sink,
	// they must NOT appear in the rendered body (Security S2, defense-in-depth at
	// the publisher independent of the upstream provenance level).
	redacted := Render(leakyBundle(), defaultRenderConfig, nil)
	for _, secret := range []string{"secretSource", "secretSink", "/internal/auth/token.go", "/internal/net/out.go"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("redacted body leaked sensitive taint provenance %q:\n%s", secret, redacted)
		}
	}

	// When the caller EXPLICITLY opts out of redaction, the verbatim path appears —
	// proving the redaction is what suppressed it above (not just absent data).
	unredacted := Render(leakyBundle(), renderConfig{Redact: false, MaxQuestions: 50}, nil)
	if !strings.Contains(unredacted, "secretSource") || !strings.Contains(unredacted, "secretSink") {
		t.Fatalf("un-redacted body should contain the verbatim path:\n%s", unredacted)
	}
}

func TestEmptyBundleRendersValidBody(t *testing.T) {
	body := Render(FindingsBundle{PR: "x/y#1"}, defaultRenderConfig, nil)
	if !strings.HasPrefix(body, StickyMarker) {
		t.Fatal("empty bundle body must still carry the marker")
	}
	for _, want := range []string{"No scored regions", "No hub/bridge/surprise signals", "No questions generated"} {
		if !strings.Contains(body, want) {
			t.Fatalf("empty body missing graceful readout %q", want)
		}
	}
}

// TestNoNetworkOrExecImports is the structural guard (Security S4 / AC2 / AC3):
// the render + gate + comment + serialize source files MUST NOT import any network
// or process-exec package. The CommentHost seam is the single permitted egress;
// its real network implementation lives EXCLUSIVELY in githubhost.go (SW-043), so
// that file is the one and only exception — every other file stays fully offline.
func TestNoNetworkOrExecImports(t *testing.T) {
	forbidden := []string{"net", "net/http", "os/exec", "net/rpc"}
	// All non-test files EXCEPT githubhost.go (the single intentional egress).
	files := []string{"render.go", "gate.go", "comment.go", "serialize.go", "host.go", "doc.go"}
	fset := token.NewFileSet()
	for _, f := range files {
		af, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, imp := range af.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			for _, bad := range forbidden {
				if path == bad {
					t.Fatalf("%s imports forbidden network/exec package %q (render+gate must run fully offline)", f, bad)
				}
			}
		}
	}
}

// TestGitHubHostIsSoleNetworkUser is the SW-043 egress assertion (AC2): the real
// GitHub comment host is the ONLY file in the PR-review engine package that
// imports a network package, and it imports net/http (not os/exec). This proves,
// by source inspection, that the single outbound boundary is exactly the
// sticky-comment upsert — engine analysis, render, and gate perform zero outbound.
func TestGitHubHostIsSoleNetworkUser(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	netImporters := map[string]bool{}
	ghImportsHTTP := false
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range af.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if path == "net" || path == "net/http" || path == "net/rpc" || path == "os/exec" {
				netImporters[name] = true
				if name == "githubhost.go" && path == "net/http" {
					ghImportsHTTP = true
				}
			}
		}
	}
	if len(netImporters) != 1 || !netImporters["githubhost.go"] {
		t.Fatalf("expected githubhost.go to be the SOLE network importer, got: %v", netImporters)
	}
	if !ghImportsHTTP {
		t.Fatalf("githubhost.go must import net/http (the single egress boundary)")
	}
}

// ---------------------------------------------------------------------------
// Service pipeline (offline, via injected seam + mock host).
// ---------------------------------------------------------------------------

func TestServicePublishDryRunNoHostTouch(t *testing.T) {
	src := &stubSource{bundle: fixedBundle()}
	svc := newServiceWithSource(src)
	host := NewMockHost()

	res, err := svc.Publish(context.Background(), host, PublishOptions{
		PR:   "owner/repo#42",
		Gate: GateConfig{Enabled: true, BlockThreshold: 500},
		// Publish: false (dry-run default)
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if res.Upsert != nil {
		t.Fatalf("dry-run must not upsert, got %+v", res.Upsert)
	}
	if host.Creates != 0 || host.Updates != 0 || host.Finds != 0 {
		t.Fatalf("dry-run must not touch host: creates=%d updates=%d finds=%d", host.Creates, host.Updates, host.Finds)
	}
	if res.Gate.Verdict != VerdictBlock {
		t.Fatalf("gate verdict = %q, want BLOCK", res.Gate.Verdict)
	}
	if !strings.Contains(res.Body, StickyMarker) {
		t.Fatal("result body missing marker")
	}
	if src.calls != 1 {
		t.Fatalf("findings source consumed %d times, want exactly 1", src.calls)
	}
}

func TestServicePublishUpsertsThroughHost(t *testing.T) {
	src := &stubSource{bundle: fixedBundle()}
	svc := newServiceWithSource(src)
	host := NewMockHost()
	opts := PublishOptions{PR: "owner/repo#42", Publish: true}

	r1, err := svc.Publish(context.Background(), host, opts)
	if err != nil {
		t.Fatalf("publish 1: %v", err)
	}
	if r1.Upsert == nil || r1.Upsert.Action != ActionCreated {
		t.Fatalf("first publish must create, got %+v", r1.Upsert)
	}
	r2, err := svc.Publish(context.Background(), host, opts)
	if err != nil {
		t.Fatalf("publish 2: %v", err)
	}
	if r2.Upsert == nil || r2.Upsert.Action != ActionUpdated {
		t.Fatalf("second publish must update in place, got %+v", r2.Upsert)
	}
	if host.Count() != 1 {
		t.Fatalf("publish twice -> %d comments, want exactly 1", host.Count())
	}
}

func TestMarshalDeterministicByteStable(t *testing.T) {
	src := &stubSource{bundle: fixedBundle()}
	svc := newServiceWithSource(src)
	res, err := svc.Publish(context.Background(), nil, PublishOptions{PR: "p", Gate: GateConfig{Enabled: true, BlockThreshold: 500}})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	b1, err := Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	b2, _ := Marshal(res)
	if !bytes.Equal(b1, b2) {
		t.Fatal("Marshal not byte-stable across runs")
	}
	// Contract symmetry with the sibling reports: an Outcome status is present.
	if res.Outcome != OutcomeFound {
		t.Fatalf("outcome = %q, want found for a populated bundle", res.Outcome)
	}
	if !bytes.Contains(b1, []byte("\"outcome\":\"found\"")) {
		t.Fatalf("serialized result missing outcome field: %s", b1)
	}
	// Body must survive serialization with the marker intact (no HTML escaping).
	if !bytes.Contains(b1, []byte(StickyMarker)) {
		t.Fatalf("serialized result lost the marker (HTML escaping?): %s", b1)
	}
	if bytes.HasSuffix(b1, []byte("\n")) {
		t.Fatal("Marshal must trim trailing newline (byte-stable contract)")
	}
}

// containsAll reports whether every want is present in got.
func containsAll(got []string, want ...string) bool {
	set := map[string]struct{}{}
	for _, g := range got {
		set[g] = struct{}{}
	}
	for _, w := range want {
		if _, ok := set[w]; !ok {
			return false
		}
	}
	return true
}
