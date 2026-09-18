package retrieval

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/embed"
)

// qualificationFingerprintFixture is a realistic eight-field canonical: the
// tests below mutate exactly one field at a time so a refusal can only be
// about that field.
func qualificationFingerprintFixture() embed.Fingerprint {
	return embed.Fingerprint{
		ModelID:         "static:potion-code-16M-v2:deadbeefcafe:mean:true:0123456789ab:fedcba987654:embedeach-f16-tree:0011223344ff",
		Revision:        "e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b",
		ModelSHA256:     strings.Repeat("a", 64),
		TokenizerSHA256: strings.Repeat("b", 64),
		Dim:             256,
		DocumentSchema:  embed.DocumentSchema,
		ChunkerConfig:   "",
		GraphGeneration: QualificationGraphGenerationPlaceholder,
	}
}

// encodeQualificationFingerprintFields mirrors embed.encodeCanonical (which is
// unexported) so a test can build a canonical with a deliberately wrong field
// COUNT — something embed.Fingerprint, being a struct of exactly eight fields,
// cannot express.
func encodeQualificationFingerprintFields(fields []string) string {
	var b strings.Builder
	for i, field := range fields {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strconv.Itoa(len(field)))
		b.WriteByte(':')
		b.WriteString(field)
	}
	return b.String()
}

func mustDecodeQualificationFingerprint(t *testing.T, canonical string) []string {
	t.Helper()
	fields, ok := decodeQualificationFingerprint(canonical)
	if !ok || len(fields) != qualificationFingerprintFieldCount {
		t.Fatalf("fixture canonical does not decode into %d fields: %q", qualificationFingerprintFieldCount, canonical)
	}
	return fields
}

// withQualificationFingerprintField returns canonical with one field replaced.
func withQualificationFingerprintField(t *testing.T, canonical string, index int, value string) string {
	t.Helper()
	fields := mustDecodeQualificationFingerprint(t, canonical)
	fields[index] = value
	return encodeQualificationFingerprintFields(fields)
}

// withQualificationGraphGeneration is the runtime binding a real index build
// performs: the same embedding space, a freshly minted random generation.
func withQualificationGraphGeneration(t *testing.T, canonical, generation string) string {
	t.Helper()
	return withQualificationFingerprintField(t, canonical, qualificationGraphGenerationField, generation)
}

// A generation is minted from crypto/rand on every index build, so the ONLY
// field a preregistration cannot pin is the one this test varies. Everything
// else must still be compared exactly.
func TestQualificationFingerprintsAgreeIgnoresOnlyTheGraphGeneration(t *testing.T) {
	base := qualificationFingerprintFixture().Canonical()

	for _, generation := range []string{
		"a4babe5c82f1a6e355ac363d1fa7d070",
		"c91cee9e050b4d0042042c9af208f012",
		"",
		QualificationGraphGenerationPlaceholder,
	} {
		observed := withQualificationGraphGeneration(t, base, generation)
		if err := qualificationFingerprintsAgree(observed, base); err != nil {
			t.Fatalf("graph generation %q was treated as a fingerprint difference: %v", generation, err)
		}
		if err := qualificationFingerprintsAgree(base, observed); err != nil {
			t.Fatalf("comparison is not symmetric for graph generation %q: %v", generation, err)
		}
	}

	for _, tc := range []struct {
		name  string
		index int
		value string
	}{
		{name: "model id", index: 0, value: "static:some-other-model"},
		{name: "revision", index: 1, value: "0000000000000000000000000000000000000000"},
		{name: "model digest", index: 2, value: strings.Repeat("9", 64)},
		{name: "tokenizer digest", index: 3, value: strings.Repeat("8", 64)},
		{name: "dimension", index: 4, value: "512"},
		{name: "document schema", index: 5, value: "v0"},
		{name: "chunker config", index: 6, value: "window:2048"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observed := withQualificationFingerprintField(t, base, tc.index, tc.value)
			err := qualificationFingerprintsAgree(observed, base)
			if err == nil {
				t.Fatalf("a changed %s was accepted as the same embedding space", tc.name)
			}
			// The operator must not have to count length prefixes: the
			// refusal names the field by index AND by name.
			wantIndex := fmt.Sprintf("field %d", tc.index)
			wantName := qualificationFingerprintFieldNames[tc.index]
			if !strings.Contains(err.Error(), wantIndex) || !strings.Contains(err.Error(), wantName) {
				t.Fatalf("refusal does not name %s (%s): %v", wantIndex, wantName, err)
			}
			for i, other := range qualificationFingerprintFieldNames {
				if i == tc.index || other == wantName {
					continue
				}
				if strings.Contains(err.Error(), other) {
					t.Fatalf("refusal blames the wrong field %q: %v", other, err)
				}
			}
		})
	}
}

// Fail-closed: a canonical that is not exactly eight fields is malformed, not
// "different". Reporting it as a mismatch would read as model drift.
func TestQualificationFingerprintHelpersFailClosedOnMalformedCanonicals(t *testing.T) {
	base := qualificationFingerprintFixture().Canonical()
	eight := mustDecodeQualificationFingerprint(t, base)

	for _, tc := range []struct {
		name      string
		canonical string
	}{
		{name: "empty", canonical: ""},
		{name: "blank", canonical: "   "},
		{name: "seven fields", canonical: encodeQualificationFingerprintFields(eight[:7])},
		{name: "nine fields", canonical: encodeQualificationFingerprintFields(append(append([]string{}, eight...), "extra"))},
		{name: "no length prefix", canonical: "not-a-canonical-fingerprint"},
		{name: "truncated field", canonical: "64:" + strings.Repeat("a", 10)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := qualificationFingerprintsAgree(tc.canonical, base); err == nil {
				t.Fatal("a malformed observed fingerprint was compared instead of refused")
			}
			if err := qualificationFingerprintsAgree(base, tc.canonical); err == nil {
				t.Fatal("a malformed preregistered fingerprint was compared instead of refused")
			}
			if generation, ok := qualificationGraphGenerationOf(tc.canonical); ok {
				t.Fatalf("a malformed canonical yielded graph generation %q instead of failing closed", generation)
			}
			if _, err := qualificationFingerprintsDifferOutsideGraphGeneration(tc.canonical, base); err == nil {
				t.Fatal("a malformed canonical was accepted as a distinctness comparison")
			}
		})
	}

	// A well-formed canonical reports its eighth field verbatim.
	observed := withQualificationGraphGeneration(t, base, "a4babe5c82f1a6e355ac363d1fa7d070")
	generation, ok := qualificationGraphGenerationOf(observed)
	if !ok || generation != "a4babe5c82f1a6e355ac363d1fa7d070" {
		t.Fatalf("graph generation = %q ok=%t", generation, ok)
	}
}

// Two arms are two embedding spaces only if they differ in a field that says
// something. Differing generations alone are two index builds of one model.
func TestQualificationFingerprintDistinctnessIgnoresTheGraphGeneration(t *testing.T) {
	base := qualificationFingerprintFixture().Canonical()
	sameSpace := withQualificationGraphGeneration(t, base, "c91cee9e050b4d0042042c9af208f012")

	distinct, err := qualificationFingerprintsDifferOutsideGraphGeneration(base, sameSpace)
	if err != nil {
		t.Fatalf("distinctness: %v", err)
	}
	if distinct {
		t.Fatal("two fingerprints differing only in their random graph generation were reported as distinct embedding spaces")
	}

	otherSpace := withQualificationFingerprintField(t, base, 4, "512")
	distinct, err = qualificationFingerprintsDifferOutsideGraphGeneration(base, otherSpace)
	if err != nil {
		t.Fatalf("distinctness: %v", err)
	}
	if !distinct {
		t.Fatal("a different dimension was not reported as a distinct embedding space")
	}
}

// The arm validator must refuse M1/M2 pins that name one embedding space under
// two generations. (In a real preregistration the earlier per-arm identity
// check fires first, because field 0 embeds each arm's own admission digest;
// this guard is what keeps that accident from being load-bearing.)
func TestQualificationArmsRejectPotionPinsThatDifferOnlyInTheGraphGeneration(t *testing.T) {
	pre := qualificationPreregistrationFixture()
	if err := validateQualificationArms(pre.Arms); err != nil {
		t.Fatalf("fixture arms are already refused: %v", err)
	}

	m2 := pre.Arms[ArmPotion8192]
	m2.FingerprintCanonical = withQualificationGraphGeneration(t, pre.Arms[ArmPotion512].FingerprintCanonical, "c91cee9e050b4d0042042c9af208f012")
	pre.Arms[ArmPotion8192] = m2
	if err := validateQualificationArms(pre.Arms); err == nil {
		t.Fatal("M1 and M2 were accepted as distinct arms while naming one embedding space under two graph generations")
	}
}

// The end-to-end property: a run whose observations carry a graph generation
// NOTHING preregistered still promotes, as long as every arm and every
// observation carries the SAME one.
func TestEvaluateQualificationBindsTheGraphGenerationAtRuntime(t *testing.T) {
	in := passingQualificationInput(t)
	const runGeneration = "a4babe5c82f1a6e355ac363d1fa7d070"
	for i := range in.Observations {
		if in.Observations[i].Arm == ArmLexical {
			continue
		}
		// Only the index fingerprint is a canonical and therefore the only
		// observed value that carries a graph generation at all;
		// ModelFingerprint is a model id (see QualificationObservation).
		in.Observations[i].IndexFingerprint = withQualificationGraphGeneration(t, in.Observations[i].IndexFingerprint, runGeneration)
	}
	resealQualificationBuildEvidence(t, &in)

	got, err := EvaluateQualification(in)
	if err != nil {
		t.Fatalf("a run that minted its own graph generation was refused: %v", err)
	}
	if !gatePassed(t, got, "fingerprint_equality") {
		t.Fatal("fingerprint_equality failed on a fingerprint that differs from the pin only in the runtime-bound generation")
	}
	if !gatePassed(t, got, "graph_generation_consistency") {
		t.Fatal("graph_generation_consistency failed on a run carrying exactly one generation")
	}
	if !got.Promote {
		t.Fatalf("a valid run did not promote: %+v", got.Gates)
	}
}

// ... and a run whose arms disagree about the generation is refused outright:
// its arms were measured against different graphs, so the paired comparison
// between them means nothing.
func TestEvaluateQualificationRejectsDivergentGraphGenerations(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(*testing.T, *QualificationInput)
	}{
		{
			name: "one observation of one arm",
			apply: func(t *testing.T, in *QualificationInput) {
				observation := semanticObservation(in, ArmCodeRank, 3)
				observation.IndexFingerprint = withQualificationGraphGeneration(t, observation.IndexFingerprint, "c91cee9e050b4d0042042c9af208f012")
			},
		},
		{
			name: "a whole arm",
			apply: func(t *testing.T, in *QualificationInput) {
				for i := range in.Observations {
					if in.Observations[i].Arm != ArmPotion8192 {
						continue
					}
					in.Observations[i].IndexFingerprint = withQualificationGraphGeneration(t, in.Observations[i].IndexFingerprint, "c91cee9e050b4d0042042c9af208f012")
				}
			},
		},
		{
			// The generation is bound by the FIRST decodable observation, so
			// a divergence in the very last one must be caught too: the gate
			// must not be satisfied by whatever happened to bind it.
			name: "the last observation of the run",
			apply: func(t *testing.T, in *QualificationInput) {
				for i := len(in.Observations) - 1; i >= 0; i-- {
					if in.Observations[i].Arm == ArmLexical {
						continue
					}
					in.Observations[i].IndexFingerprint = withQualificationGraphGeneration(t, in.Observations[i].IndexFingerprint, "c91cee9e050b4d0042042c9af208f012")
					return
				}
				t.Fatal("fixture has no semantic observation")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := passingQualificationInput(t)
			tc.apply(t, &in)
			resealQualificationBuildEvidence(t, &in)

			evidence, err := validateQualificationEvidence(in)
			if err == nil {
				t.Fatal("divergent graph generations were accepted")
			}
			if !strings.Contains(err.Error(), "graph generation") {
				t.Fatalf("refusal does not name the graph generation: %v", err)
			}
			if evidence.graphGenerationConsistent {
				t.Fatal("the graph_generation_consistency gate stayed true on divergent generations")
			}
			if _, err := EvaluateQualification(in); err == nil {
				t.Fatal("EvaluateQualification promoted a run whose arms name different graph generations")
			}
		})
	}
}

// bindQualificationCaptureFixture points every runtime identity of a capture
// fixture at one loaded generation, each in its OWN kind: the search request
// and capture model are canonical fingerprints, the retrieval summary carries
// a model id AND a canonical (see QualificationObservation), exactly as
// engine/retrieval produces them.
func bindQualificationCaptureFixture(f *qualificationCaptureFixture, loaded embed.Fingerprint) {
	f.indexFingerprint = loaded
	f.searchFingerprint = loaded
	f.modelFingerprint = loaded.Canonical()
	f.retrieval.Summary.ModelFingerprint = loaded.ModelID
	f.retrieval.Summary.IndexFingerprint = loaded.Canonical()
}

// The capture path is where the generation is actually bound. A capture whose
// index carries a freshly minted generation must be accepted — that is every
// real run — while the identities inside one capture must still agree with the
// loaded generation, each in its own kind.
func TestQualificationCaptureBindsTheGraphGenerationAtRuntime(t *testing.T) {
	const runGeneration = "a4babe5c82f1a6e355ac363d1fa7d070"

	// A capture that minted its own generation: the pin still says
	// "graph-1", every runtime identity says runGeneration.
	f := validQualificationCaptureFixture(t)
	runtime := f.indexFingerprint
	runtime.GraphGeneration = runGeneration
	bindQualificationCaptureFixture(&f, runtime)
	if _, err := f.capture(); err != nil {
		t.Fatalf("a capture that bound its own graph generation was refused: %v", err)
	}

	// One identity left behind on the preregistered generation means the
	// capture mixed two graphs, and that is still a refusal.
	for _, tc := range []struct {
		name  string
		stale func(*qualificationCaptureFixture)
	}{
		{name: "search request", stale: func(f *qualificationCaptureFixture) { f.searchFingerprint.GraphGeneration = "graph-1" }},
		{name: "capture model", stale: func(f *qualificationCaptureFixture) {
			stale := f.indexFingerprint
			stale.GraphGeneration = "graph-1"
			f.modelFingerprint = stale.Canonical()
		}},
		// The retrieval summary's MODEL field cannot carry a stale generation
		// at all — it is a model id, which has no eighth field — so what it
		// can go wrong about is the model identity itself.
		{name: "retrieval model id", stale: func(f *qualificationCaptureFixture) {
			f.retrieval.Summary.ModelFingerprint = "some-other-model"
		}},
		{name: "retrieval index", stale: func(f *qualificationCaptureFixture) {
			stale := f.indexFingerprint
			stale.GraphGeneration = "graph-1"
			f.retrieval.Summary.IndexFingerprint = stale.Canonical()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mixed := validQualificationCaptureFixture(t)
			bound := mixed.indexFingerprint
			bound.GraphGeneration = runGeneration
			bindQualificationCaptureFixture(&mixed, bound)
			tc.stale(&mixed)
			if _, err := mixed.capture(); err == nil {
				t.Fatalf("a capture mixing two graph generations was accepted (%s)", tc.name)
			}
		})
	}

	// And a loaded generation from a different embedding space stays refused,
	// however its eighth field reads.
	other := validQualificationCaptureFixture(t)
	wrongSpace := other.indexFingerprint
	wrongSpace.GraphGeneration = runGeneration
	wrongSpace.Dim = 99
	bindQualificationCaptureFixture(&other, wrongSpace)
	if _, err := other.capture(); err == nil {
		t.Fatal("a capture against a different embedding space was accepted")
	}
}

// The uniform case keeps the gate true, so a passing run is not passing merely
// because the gate never gets a chance to fire.
func TestValidateQualificationEvidenceHoldsTheGraphGenerationGateOnAUniformRun(t *testing.T) {
	in := passingQualificationInput(t)
	evidence, err := validateQualificationEvidence(in)
	if err != nil {
		t.Fatalf("validate evidence: %v", err)
	}
	if !evidence.graphGenerationConsistent {
		t.Fatal("a run carrying one graph generation was reported as inconsistent")
	}
	if !evidence.fingerprintsOK {
		t.Fatal("a run carrying the preregistered fingerprints was reported as mismatched")
	}
}
