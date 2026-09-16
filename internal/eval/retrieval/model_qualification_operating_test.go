package retrieval

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/embed/coderank"
)

func TestOperatingEvidenceValidatesBindingsScheduleAndP95(t *testing.T) {
	in := passingQualificationInput(t)
	evidence := validOperatingEvidenceFixture(t, in)
	if err := ValidateOperatingEvidence(evidence, in.Preregistration, in.Dataset); err != nil {
		t.Fatal(err)
	}
	measurements, err := operatingMeasurementsFromEvidence(evidence, in.Preregistration, in.Dataset)
	if err != nil {
		t.Fatal(err)
	}
	if measurements.QueryEmbedP95 != 122*time.Millisecond {
		t.Fatalf("p95=%s want 122ms (sorted index 121)", measurements.QueryEmbedP95)
	}
	if measurements.ArtifactBytes != 512<<20 || measurements.PeakSidecarRSSBytes != 1<<30 || measurements.FullReindex != 5*time.Minute {
		t.Fatalf("measurements=%+v", measurements)
	}
}

func TestOperatingEvidenceRejectsMutatedBindingsEvenWhenResealed(t *testing.T) {
	in := passingQualificationInput(t)
	valid := validOperatingEvidenceFixture(t, in)
	tests := []struct {
		name  string
		apply func(*OperatingEvidence)
	}{
		{"preregistration", func(v *OperatingEvidence) { v.PreregistrationSHA256 = strings.Repeat("f", 64) }},
		{"dataset", func(v *OperatingEvidence) { v.DatasetSHA256 = strings.Repeat("f", 64) }},
		{"source", func(v *OperatingEvidence) { v.SourceRepoSHA = strings.Repeat("f", 40) }},
		{"candidate", func(v *OperatingEvidence) { v.CandidateSHA = strings.Repeat("f", 40) }},
		{"candidate diff", func(v *OperatingEvidence) { v.CandidateDiffSHA256 = strings.Repeat("f", 64) }},
		{"manifest", func(v *OperatingEvidence) { v.ManifestSHA256 = strings.Repeat("f", 64) }},
		{"fingerprint", func(v *OperatingEvidence) { v.FingerprintCanonical += "x" }},
		{"machine", func(v *OperatingEvidence) { v.Machine.CPU += " changed" }},
		{"background declaration", func(v *OperatingEvidence) { v.BackgroundLoad.Declaration += " changed" }},
		{"start epoch", func(v *OperatingEvidence) { v.StartAttestation.Runtime.Epoch = "epoch-2" }},
		{"end identity", func(v *OperatingEvidence) { v.EndAttestation.Runtime.IdentityDigest = strings.Repeat("e", 64) }},
		{"artifact drift", func(v *OperatingEvidence) { v.EndAttestation.ArtifactBytes++ }},
		{"thread drift", func(v *OperatingEvidence) { v.EndAttestation.RuntimeThreads++ }},
		{"peak decrease", func(v *OperatingEvidence) { v.EndAttestation.PeakRSSBytes = v.StartAttestation.PeakRSSBytes - 1 }},
		{"source tree", func(v *OperatingEvidence) { v.SourceTreeSHA256 = "bad" }},
		{"corpus", func(v *OperatingEvidence) { v.CorpusSHA256 = "bad" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := valid
			tc.apply(&got)
			got = mustSealOperatingEvidence(t, got)
			if err := ValidateOperatingEvidence(got, in.Preregistration, in.Dataset); err == nil {
				t.Fatal("accepted rebound operating evidence")
			}
		})
	}
}

func TestOperatingEvidenceRejectsScheduleAndReindexMutations(t *testing.T) {
	in := passingQualificationInput(t)
	valid := validOperatingEvidenceFixture(t, in)
	tests := []struct {
		name  string
		apply func(*OperatingEvidence)
	}{
		{"63 warmups", func(v *OperatingEvidence) { v.Warmups = v.Warmups[:63] }},
		{"127 samples", func(v *OperatingEvidence) { v.Samples = v.Samples[:127] }},
		{"warmup order", func(v *OperatingEvidence) { v.Warmups[0], v.Warmups[1] = v.Warmups[1], v.Warmups[0] }},
		{"sample order", func(v *OperatingEvidence) { v.Samples[64], v.Samples[65] = v.Samples[65], v.Samples[64] }},
		{"query text", func(v *OperatingEvidence) { v.Samples[0].QueryTextSHA256 = strings.Repeat("f", 64) }},
		{"zero latency", func(v *OperatingEvidence) { v.Samples[0].LatencyNS = 0 }},
		{"negative unknown", func(v *OperatingEvidence) { v.Samples[0].UnknownTokens = -1 }},
		{"767 docs", func(v *OperatingEvidence) { v.Reindex.AdmittedDocuments = 767 }},
		{"769 docs", func(v *OperatingEvidence) { v.Reindex.AdmittedDocuments = 769 }},
		{"nonfresh", func(v *OperatingEvidence) { v.Reindex.FreshEmptyWorkDir = false }},
		{"not ready", func(v *OperatingEvidence) { v.Reindex.State = embed.StateStale.String() }},
		{"wrong fingerprint", func(v *OperatingEvidence) { v.Reindex.FingerprintCanonical += "x" }},
		{"not flushed", func(v *OperatingEvidence) { v.Reindex.Flushed = false }},
		{"not closed", func(v *OperatingEvidence) { v.Reindex.Closed = false }},
		{"not durable", func(v *OperatingEvidence) { v.Reindex.DurableReady = false }},
		{"zero duration", func(v *OperatingEvidence) { v.Reindex.ElapsedNS = 0 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := cloneOperatingEvidence(t, valid)
			tc.apply(&got)
			got = mustSealOperatingEvidence(t, got)
			if err := ValidateOperatingEvidence(got, in.Preregistration, in.Dataset); err == nil {
				t.Fatal("accepted invalid schedule or reindex evidence")
			}
		})
	}
}

func TestOperatingEvidenceWriteLoadAndTamper(t *testing.T) {
	in := passingQualificationInput(t)
	want := validOperatingEvidenceFixture(t, in)
	path := filepath.Join(t.TempDir(), "operating.json")
	if err := WriteOperatingEvidence(path, want, in.Preregistration, in.Dataset); err != nil {
		t.Fatal(err)
	}
	got, err := LoadOperatingEvidence(path, in.Preregistration, in.Dataset)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("round trip changed evidence")
	}
	if err := WriteOperatingEvidence(path, want, in.Preregistration, in.Dataset); err == nil {
		t.Fatal("overwrote sealed evidence")
	}
}

func TestOperatingEvidenceRejectsUnsupportedReferenceMachine(t *testing.T) {
	in := passingQualificationInput(t)
	in.Preregistration.ReferenceMachine.OS = "windows"
	evidence := validOperatingEvidenceFixture(t, in)
	if err := ValidateOperatingEvidence(evidence, in.Preregistration, in.Dataset); err == nil {
		t.Fatal("accepted an OS without a defined peak-RSS unit contract")
	}
}

func validOperatingEvidenceFixture(t *testing.T, in QualificationInput) OperatingEvidence {
	t.Helper()
	preSHA, err := qualificationPreregistrationDigest(in.Preregistration)
	if err != nil {
		t.Fatal(err)
	}
	identity := strings.TrimPrefix(in.Preregistration.Arms[ArmCodeRank].EmbedderID[strings.LastIndex(in.Preregistration.Arms[ArmCodeRank].EmbedderID, ":"):], ":")
	evidence := OperatingEvidence{
		SchemaVersion:         QualificationOperatingEvidenceSchemaVersion,
		PreregistrationSHA256: preSHA,
		DatasetSHA256:         in.Dataset.SHA256,
		SourceRepoSHA:         in.Preregistration.SourceRepoSHA,
		CandidateSHA:          in.Preregistration.CandidateSHA,
		CandidateDiffSHA256:   in.Preregistration.CandidateDiffSHA256,
		ManifestSHA256:        in.Preregistration.Arms[ArmCodeRank].ManifestSHA256,
		FingerprintCanonical:  in.Preregistration.Arms[ArmCodeRank].FingerprintCanonical,
		StartAttestation:      coderank.OperatingAttestation{Runtime: embed.RuntimeAttestation{IdentityDigest: identity, Epoch: "epoch-1"}, PeakRSSBytes: 900 << 20, ArtifactBytes: 512 << 20, RuntimeThreads: in.Preregistration.ReferenceMachine.RuntimeThreads},
		EndAttestation:        coderank.OperatingAttestation{Runtime: embed.RuntimeAttestation{IdentityDigest: identity, Epoch: "epoch-1"}, PeakRSSBytes: 1 << 30, ArtifactBytes: 512 << 20, RuntimeThreads: in.Preregistration.ReferenceMachine.RuntimeThreads},
		Machine:               in.Preregistration.ReferenceMachine,
		BackgroundLoad:        OperatingBackgroundLoad{Declaration: in.Preregistration.ReferenceMachine.BackgroundLoad, Protocol: "operator-observed-v1", Metadata: "terminal and process list checked before measurement"},
		SourceTreeSHA256:      strings.Repeat("6", 64), CorpusSHA256: strings.Repeat("7", 64),
		Reindex: OperatingReindexEvidence{FreshEmptyWorkDir: true, AdmittedDocuments: 768, FingerprintCanonical: in.Preregistration.Arms[ArmCodeRank].FingerprintCanonical,
			State: embed.StateReady.String(), Flushed: true, Closed: true, DurableReady: true, ElapsedNS: int64(5 * time.Minute)},
	}
	for i, query := range in.Dataset.Dataset.Queries {
		evidence.Warmups = append(evidence.Warmups, OperatingWarmup{Ordinal: i, QueryID: query.ID, QueryTextSHA256: SHA256Hex([]byte(query.Text))})
	}
	for pass := 0; pass < 2; pass++ {
		for i, query := range in.Dataset.Dataset.Queries {
			ordinal := pass*64 + i
			evidence.Samples = append(evidence.Samples, OperatingQuerySample{Ordinal: ordinal, QueryID: query.ID,
				QueryTextSHA256: SHA256Hex([]byte(query.Text)), LatencyNS: int64(ordinal+1) * int64(time.Millisecond),
				VectorSHA256: SHA256Hex([]byte("vector:" + query.ID)), UnknownTokens: ordinal % 3})
		}
	}
	return mustSealOperatingEvidence(t, evidence)
}

func mustSealOperatingEvidence(t *testing.T, evidence OperatingEvidence) OperatingEvidence {
	t.Helper()
	sealed, err := SealOperatingEvidence(evidence)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

func cloneOperatingEvidence(t *testing.T, evidence OperatingEvidence) OperatingEvidence {
	t.Helper()
	return OperatingEvidence{
		SchemaVersion: evidence.SchemaVersion, PreregistrationSHA256: evidence.PreregistrationSHA256,
		DatasetSHA256: evidence.DatasetSHA256, SourceRepoSHA: evidence.SourceRepoSHA, CandidateSHA: evidence.CandidateSHA,
		CandidateDiffSHA256: evidence.CandidateDiffSHA256, ManifestSHA256: evidence.ManifestSHA256,
		FingerprintCanonical: evidence.FingerprintCanonical, StartAttestation: evidence.StartAttestation, EndAttestation: evidence.EndAttestation,
		Machine: evidence.Machine, BackgroundLoad: evidence.BackgroundLoad, SourceTreeSHA256: evidence.SourceTreeSHA256,
		CorpusSHA256: evidence.CorpusSHA256, Reindex: evidence.Reindex,
		Warmups: append([]OperatingWarmup(nil), evidence.Warmups...), Samples: append([]OperatingQuerySample(nil), evidence.Samples...), SHA256: evidence.SHA256,
	}
}
