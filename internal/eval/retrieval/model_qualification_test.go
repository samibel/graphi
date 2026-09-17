package retrieval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/embed/coderank"
	"github.com/samibel/graphi/engine/embed/static"
)

func TestValidateQualificationDatasetAcceptsExactFreshDevelopmentPopulation(t *testing.T) {
	loaded := qualificationDatasetFixture()
	loaded.Path = "/tmp/cobra-compact17-fresh-unseen-v4/fresh-development.json"

	if err := ValidateQualificationDataset(loaded); err != nil {
		t.Fatalf("valid qualification dataset: %v", err)
	}
}

func TestValidateQualificationDatasetRejectsWrongPopulationSize(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Loaded)
	}{
		{name: "63", mutate: func(l *Loaded) { l.Dataset.Queries = l.Dataset.Queries[:63] }},
		{name: "65", mutate: func(l *Loaded) {
			l.Dataset.Queries = append(l.Dataset.Queries, qualificationQuery("extra", StratumAmbiguous))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loaded := qualificationDatasetFixture()
			tc.mutate(loaded)
			sealQualificationDatasetFixture(loaded)
			if err := ValidateQualificationDataset(loaded); err == nil {
				t.Fatal("accepted wrong population size")
			}
		})
	}
}

func TestValidateQualificationDatasetRejectsEveryWrongStratumCount(t *testing.T) {
	for _, stratum := range []string{
		StratumAmbiguous,
		StratumArchitectureFlow,
		StratumConfigDocs,
		StratumExactIdentifier,
		StratumExactPath,
		StratumNLBehaviour,
	} {
		t.Run(stratum, func(t *testing.T) {
			loaded := qualificationDatasetFixture()
			for i := range loaded.Dataset.Queries {
				if loaded.Dataset.Queries[i].Stratum == stratum {
					loaded.Dataset.Queries[i].Stratum = StratumNoHit
					loaded.Dataset.Queries[i].Judgements = nil
					break
				}
			}
			sealQualificationDatasetFixture(loaded)
			if err := ValidateQualificationDataset(loaded); err == nil {
				t.Fatalf("accepted wrong %s count", stratum)
			}
		})
	}
}

func TestValidateQualificationDatasetRejectsNonDevelopmentOrUnanswerableRows(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Query)
	}{
		{name: "holdout", mutate: func(q *Query) { q.Split = SplitHoldout }},
		{name: "no hit", mutate: func(q *Query) { q.Stratum, q.Judgements = StratumNoHit, nil }},
		{name: "missing grade 3", mutate: func(q *Query) { q.Judgements[0].Grade = 2 }},
		{name: "blank family", mutate: func(q *Query) { q.FamilyID = " \t" }},
		{name: "blank provenance", mutate: func(q *Query) { q.Provenance = "\n" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loaded := qualificationDatasetFixture()
			tc.mutate(&loaded.Dataset.Queries[0])
			sealQualificationDatasetFixture(loaded)
			if err := ValidateQualificationDataset(loaded); err == nil {
				t.Fatalf("accepted dataset with %s", tc.name)
			}
		})
	}
}

func TestValidateQualificationDatasetRequiresUniqueFamilies(t *testing.T) {
	loaded := qualificationDatasetFixture()
	loaded.Dataset.Queries[1].FamilyID = loaded.Dataset.Queries[0].FamilyID
	sealQualificationDatasetFixture(loaded)

	if err := ValidateQualificationDataset(loaded); err == nil {
		t.Fatal("accepted duplicate family")
	}
}

func TestValidateQualificationDatasetRejectsSpentHoldoutContentIdentity(t *testing.T) {
	const (
		spentID  = "cobra-compact17-fresh-unseen-v4"
		spentSHA = "c4115b6331e46f1a8c0b0daad020e86e4826a51de8e06a68e3211dea0770a3a6"
	)
	for _, tc := range []struct {
		name   string
		mutate func(*Loaded)
	}{
		{name: "renamed copy with spent id", mutate: func(l *Loaded) {
			l.Dataset.ID = spentID
			sealQualificationDatasetFixture(l)
			l.Path = "/tmp/renamed.json"
		}},
		{name: "spent id padded with whitespace", mutate: func(l *Loaded) {
			l.Dataset.ID = "\t" + spentID + " "
			sealQualificationDatasetFixture(l)
			l.Path = "/tmp/renamed.json"
		}},
		{name: "renamed copy with spent sha", mutate: func(l *Loaded) {
			l.SHA256 = spentSHA
			l.Path = "/tmp/renamed.json"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loaded := qualificationDatasetFixture()
			tc.mutate(loaded)
			if err := ValidateQualificationDataset(loaded); err == nil {
				t.Fatal("accepted spent holdout content identity")
			}
		})
	}
}

func TestValidateQualificationDatasetBindsLoadedDigestToRawBytes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Loaded)
	}{
		{name: "empty raw", mutate: func(l *Loaded) { l.Raw = nil }},
		{name: "stale sha", mutate: func(l *Loaded) { l.SHA256 = strings.Repeat("f", 64) }},
		{name: "forged raw", mutate: func(l *Loaded) { l.Raw = append(append([]byte(nil), l.Raw...), '\n') }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loaded := qualificationDatasetFixture()
			tc.mutate(loaded)
			if err := ValidateQualificationDataset(loaded); err == nil {
				t.Fatal("accepted unbound loaded dataset identity")
			}
		})
	}
}

func TestValidateQualificationDatasetRejectsRenamedOrRelabelledSpentEvidence(t *testing.T) {
	path := filepath.Join(repoRootForTest(t), "docs/eval/retrieval/runs/2026-09-15-product-compact-v17-fresh-unseen-v4/sealed-dataset.json")
	loaded, err := LoadDataset(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Path = filepath.Join(t.TempDir(), "fresh-development.json")
	if err := ValidateQualificationDataset(loaded); err == nil || !strings.Contains(err.Error(), "spent holdout identity") {
		t.Fatalf("renamed spent dataset error = %v", err)
	}

	loaded.Dataset.ID = "new-label"
	if err := ValidateQualificationDataset(loaded); err == nil || !strings.Contains(err.Error(), "spent holdout identity") {
		t.Fatalf("relabelled spent dataset error = %v", err)
	}
}

func TestValidateQualificationDatasetSpentRegistryMatchesEveryCommittedHoldoutDataset(t *testing.T) {
	root := repoRootForTest(t)
	patterns := []string{
		"internal/eval/retrieval/testdata/datasets/*.json",
		"docs/eval/retrieval/runs/*/dataset.json",
		"docs/eval/retrieval/runs/*/sealed-dataset.json",
	}
	found := map[string]bool{}
	for _, pattern := range patterns {
		paths, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var dataset Dataset
			if err := json.Unmarshal(raw, &dataset); err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			hasHoldout := false
			for _, query := range dataset.Queries {
				if query.Split == SplitHoldout {
					hasHoldout = true
					break
				}
			}
			if !hasHoldout {
				continue
			}
			key := strings.TrimSpace(dataset.ID) + "\x00" + SHA256Hex(raw)
			found[key] = true
			if !spentQualificationDatasetIDs[strings.TrimSpace(dataset.ID)] {
				t.Errorf("committed holdout id %q from %s is absent from registry", dataset.ID, path)
			}
			if !spentQualificationDatasetSHA256[SHA256Hex(raw)] {
				t.Errorf("committed holdout sha256 %s from %s is absent from registry", SHA256Hex(raw), path)
			}
		}
	}
	if got, want := len(found), 11; got != want {
		t.Fatalf("unique committed holdout identities = %d, want %d", got, want)
	}
	if len(spentQualificationDatasetIDs) != 11 || len(spentQualificationDatasetSHA256) != 11 {
		t.Fatalf("spent registry sizes = ids:%d sha256:%d, want 11 each", len(spentQualificationDatasetIDs), len(spentQualificationDatasetSHA256))
	}
}

func TestValidateQualificationDatasetRequiresExactGradeThreshold(t *testing.T) {
	loaded := qualificationDatasetFixture()
	loaded.Dataset.RelevantMinGrade = 0
	sealQualificationDatasetFixture(loaded)

	if err := ValidateQualificationDataset(loaded); err == nil {
		t.Fatal("accepted qualification dataset without relevant_min_grade=3")
	}
}

func TestValidateQualificationDatasetRejectsMissingOrMalformedIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		loaded *Loaded
	}{
		{name: "nil loaded", loaded: nil},
		{name: "nil dataset", loaded: &Loaded{SHA256: strings.Repeat("a", 64)}},
		{name: "malformed sha", loaded: func() *Loaded {
			l := qualificationDatasetFixture()
			l.SHA256 = strings.Repeat("A", 64)
			return l
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateQualificationDataset(tc.loaded); err == nil {
				t.Fatal("accepted missing or malformed identity")
			}
		})
	}
}

func TestValidateQualificationPreregistrationAcceptsExactContract(t *testing.T) {
	if err := ValidateQualificationPreregistration(qualificationPreregistrationFixture()); err != nil {
		t.Fatalf("valid preregistration: %v", err)
	}
}

func TestValidateQualificationPreregistrationRequiresFrozenBindings(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*QualificationPreregistration)
	}{
		{name: "schema", mutate: func(p *QualificationPreregistration) { p.SchemaVersion++ }},
		{name: "dataset digest", mutate: func(p *QualificationPreregistration) { p.DatasetSHA256 = strings.Repeat("A", 64) }},
		{name: "spent dataset digest", mutate: func(p *QualificationPreregistration) {
			p.DatasetSHA256 = "c4115b6331e46f1a8c0b0daad020e86e4826a51de8e06a68e3211dea0770a3a6"
		}},
		{name: "source sha", mutate: func(p *QualificationPreregistration) { p.SourceRepoSHA = strings.Repeat("a", 39) }},
		{name: "candidate sha", mutate: func(p *QualificationPreregistration) { p.CandidateSHA = "" }},
		{name: "candidate diff digest", mutate: func(p *QualificationPreregistration) { p.CandidateDiffSHA256 = "x" }},
		{name: "reader prompt digest", mutate: func(p *QualificationPreregistration) { p.ReaderPromptSHA256 = "" }},
		{name: "grader prompt digest", mutate: func(p *QualificationPreregistration) { p.GraderPromptSHA256 = strings.Repeat("F", 64) }},
		{name: "compact version", mutate: func(p *QualificationPreregistration) { p.CompactVersion = "compact/18" }},
		{name: "token budget", mutate: func(p *QualificationPreregistration) { p.TokenBudget = 1201 }},
		{name: "bootstrap samples", mutate: func(p *QualificationPreregistration) { p.BootstrapSamples = 99999 }},
		{name: "bootstrap seed", mutate: func(p *QualificationPreregistration) { p.BootstrapSeed = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pre := qualificationPreregistrationFixture()
			tc.mutate(&pre)
			if err := ValidateQualificationPreregistration(pre); err == nil {
				t.Fatalf("accepted invalid %s", tc.name)
			}
		})
	}
}

func TestValidateQualificationPreregistrationRequiresExactlyFourPinnedArms(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*QualificationPreregistration)
	}{
		{name: "missing arm", mutate: func(p *QualificationPreregistration) { delete(p.Arms, ArmCodeRank) }},
		{name: "unknown arm", mutate: func(p *QualificationPreregistration) {
			p.Arms[QualificationArm("M4_grid_search")] = p.Arms[ArmCodeRank]
		}},
		{name: "wrong label", mutate: func(p *QualificationPreregistration) {
			pin := p.Arms[ArmCodeRank]
			pin.Label = "alternative"
			p.Arms[ArmCodeRank] = pin
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pre := qualificationPreregistrationFixture()
			tc.mutate(&pre)
			if err := ValidateQualificationPreregistration(pre); err == nil {
				t.Fatal("accepted invalid arm set")
			}
		})
	}
}

func TestValidateQualificationPreregistrationKeepsLexicalArmUnembedded(t *testing.T) {
	for _, field := range []string{"embedder", "fingerprint", "manifest", "admission"} {
		t.Run(field, func(t *testing.T) {
			pre := qualificationPreregistrationFixture()
			pin := pre.Arms[ArmLexical]
			switch field {
			case "embedder":
				pin.EmbedderID = "semantic"
			case "fingerprint":
				pin.FingerprintCanonical = "semantic"
			case "manifest":
				pin.ManifestSHA256 = strings.Repeat("a", 64)
			case "admission":
				pin.AdmissionSHA256 = strings.Repeat("a", 64)
			}
			pre.Arms[ArmLexical] = pin
			if err := ValidateQualificationPreregistration(pre); err == nil {
				t.Fatal("accepted embedded lexical arm")
			}
		})
	}
}

func TestValidateQualificationPreregistrationRequiresFullyPinnedSemanticArms(t *testing.T) {
	for _, arm := range []QualificationArm{ArmPotion512, ArmPotion8192, ArmCodeRank} {
		for _, field := range []string{"embedder", "fingerprint", "manifest", "admission"} {
			t.Run(string(arm)+"/"+field, func(t *testing.T) {
				pre := qualificationPreregistrationFixture()
				pin := pre.Arms[arm]
				switch field {
				case "embedder":
					pin.EmbedderID = " "
				case "fingerprint":
					pin.FingerprintCanonical = "not-a-full-fingerprint"
				case "manifest":
					pin.ManifestSHA256 = ""
				case "admission":
					pin.AdmissionSHA256 = strings.Repeat("A", 64)
				}
				pre.Arms[arm] = pin
				if err := ValidateQualificationPreregistration(pre); err == nil {
					t.Fatal("accepted incompletely pinned semantic arm")
				}
			})
		}
	}
}

func TestValidateQualificationPreregistrationEnforcesPotionArmSemantics(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*QualificationPreregistration)
	}{
		{name: "M1 chunker config invented", mutate: func(p *QualificationPreregistration) {
			pin := p.Arms[ArmPotion512]
			fingerprint := mustQualificationFingerprint(t, pin.FingerprintCanonical)
			fingerprint.ChunkerConfig = "admission/1"
			pin.FingerprintCanonical = fingerprint.Canonical()
			p.Arms[ArmPotion512] = pin
		}},
		{name: "M1 wrong admission digest", mutate: func(p *QualificationPreregistration) {
			pin := p.Arms[ArmPotion512]
			pin.AdmissionSHA256 = strings.Repeat("9", 64)
			p.Arms[ArmPotion512] = pin
		}},
		{name: "M2 reuses 512 profile", mutate: func(p *QualificationPreregistration) {
			pin := p.Arms[ArmPotion8192]
			pin.AdmissionSHA256 = p.Arms[ArmPotion512].AdmissionSHA256
			p.Arms[ArmPotion8192] = pin
		}},
		{name: "M2 reuses M1 fingerprint", mutate: func(p *QualificationPreregistration) {
			pin := p.Arms[ArmPotion8192]
			pin.EmbedderID = p.Arms[ArmPotion512].EmbedderID
			pin.FingerprintCanonical = p.Arms[ArmPotion512].FingerprintCanonical
			p.Arms[ArmPotion8192] = pin
		}},
		{name: "Potion arms use different manifests", mutate: func(p *QualificationPreregistration) {
			pin := p.Arms[ArmPotion8192]
			pin.ManifestSHA256 = strings.Repeat("9", 64)
			p.Arms[ArmPotion8192] = pin
		}},
		{name: "Potion arms use different weights", mutate: func(p *QualificationPreregistration) {
			pin := p.Arms[ArmPotion8192]
			fingerprint := mustQualificationFingerprint(t, pin.FingerprintCanonical)
			fingerprint.ModelSHA256 = strings.Repeat("9", 64)
			pin.FingerprintCanonical = fingerprint.Canonical()
			p.Arms[ArmPotion8192] = pin
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pre := qualificationPreregistrationFixture()
			tc.mutate(&pre)
			if err := ValidateQualificationPreregistration(pre); err == nil {
				t.Fatal("accepted invalid Potion arm relationship")
			}
		})
	}
}

func TestValidateQualificationPreregistrationEnforcesCodeRankArmSemantics(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*QualificationPreregistration)
	}{
		{name: "M3 reuses Potion identity", mutate: func(p *QualificationPreregistration) {
			pin := p.Arms[ArmCodeRank]
			pin.EmbedderID = p.Arms[ArmPotion8192].EmbedderID
			pin.FingerprintCanonical = p.Arms[ArmPotion8192].FingerprintCanonical
			p.Arms[ArmCodeRank] = pin
		}},
		{name: "M3 reuses Potion manifest", mutate: func(p *QualificationPreregistration) {
			pin := p.Arms[ArmCodeRank]
			pin.ManifestSHA256 = p.Arms[ArmPotion512].ManifestSHA256
			p.Arms[ArmCodeRank] = pin
		}},
		{name: "M3 admission digest not bound to profile", mutate: func(p *QualificationPreregistration) {
			pin := p.Arms[ArmCodeRank]
			pin.AdmissionSHA256 = strings.Repeat("9", 64)
			p.Arms[ArmCodeRank] = pin
		}},
		{name: "M3 endpoint enters durable profile", mutate: func(p *QualificationPreregistration) {
			pin := p.Arms[ArmCodeRank]
			fingerprint := mustQualificationFingerprint(t, pin.FingerprintCanonical)
			var manifest coderank.Manifest
			if err := json.Unmarshal([]byte(fingerprint.ChunkerConfig), &manifest); err != nil {
				t.Fatal(err)
			}
			manifest.Endpoint = "http://127.0.0.1:8765"
			profile, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			fingerprint.ChunkerConfig = string(profile)
			pin.FingerprintCanonical = fingerprint.Canonical()
			p.Arms[ArmCodeRank] = pin
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pre := qualificationPreregistrationFixture()
			tc.mutate(&pre)
			if err := ValidateQualificationPreregistration(pre); err == nil {
				t.Fatal("accepted invalid CodeRank arm relationship")
			}
		})
	}
}

func TestValidateQualificationPreregistrationRequiresExactPromotionThresholds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*QualificationThresholds)
	}{
		{name: "passes", mutate: func(v *QualificationThresholds) { v.MinPasses++ }},
		{name: "paired gain", mutate: func(v *QualificationThresholds) { v.MinPairedGain++ }},
		{name: "weak strata", mutate: func(v *QualificationThresholds) { v.MinWeakStrataWithPositiveGain++ }},
		{name: "confidence", mutate: func(v *QualificationThresholds) { v.BootstrapConfidence = .9 }},
		{name: "rss", mutate: func(v *QualificationThresholds) { v.MaxSidecarRSSBytes-- }},
		{name: "artifacts", mutate: func(v *QualificationThresholds) { v.MaxArtifactBytes-- }},
		{name: "query p95", mutate: func(v *QualificationThresholds) { v.MaxQueryP95Millis-- }},
		{name: "query samples", mutate: func(v *QualificationThresholds) { v.MinQuerySamples-- }},
		{name: "reindex", mutate: func(v *QualificationThresholds) { v.MaxReindexSeconds-- }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pre := qualificationPreregistrationFixture()
			tc.mutate(&pre.Thresholds)
			if err := ValidateQualificationPreregistration(pre); err == nil {
				t.Fatal("accepted changed promotion threshold")
			}
		})
	}
}

func TestValidateQualificationPreregistrationRequiresReferenceMachine(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*ReferenceMachine)
	}{
		{name: "os", mutate: func(v *ReferenceMachine) { v.OS = " " }},
		{name: "os version", mutate: func(v *ReferenceMachine) { v.OSVersion = "" }},
		{name: "cpu", mutate: func(v *ReferenceMachine) { v.CPU = "" }},
		{name: "physical cores", mutate: func(v *ReferenceMachine) { v.PhysicalCores = 0 }},
		{name: "runtime threads", mutate: func(v *ReferenceMachine) { v.RuntimeThreads = 0 }},
		{name: "background load", mutate: func(v *ReferenceMachine) { v.BackgroundLoad = "\t" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pre := qualificationPreregistrationFixture()
			tc.mutate(&pre.ReferenceMachine)
			if err := ValidateQualificationPreregistration(pre); err == nil {
				t.Fatal("accepted incomplete reference machine")
			}
		})
	}
}

func qualificationDatasetFixture() *Loaded {
	counts := []struct {
		stratum string
		count   int
	}{
		{StratumAmbiguous, 10},
		{StratumArchitectureFlow, 11},
		{StratumConfigDocs, 10},
		{StratumExactIdentifier, 11},
		{StratumExactPath, 11},
		{StratumNLBehaviour, 11},
	}
	dataset := &Dataset{
		SchemaVersion:    SchemaVersion,
		ID:               "cobra-embedded-model-qualification-dev-v1",
		Repo:             "cobra",
		RepoSHA:          strings.Repeat("1", 40),
		Language:         "en",
		EvidenceClass:    "independent-curator-annotated-and-reviewed",
		RelevantMinGrade: GradeMax,
	}
	for _, group := range counts {
		for i := 0; i < group.count; i++ {
			dataset.Queries = append(dataset.Queries, qualificationQuery(group.stratum+"-"+string(rune('a'+i)), group.stratum))
		}
	}
	loaded := &Loaded{
		Dataset: dataset,
		Path:    "/tmp/fresh-development.json",
	}
	sealQualificationDatasetFixture(loaded)
	return loaded
}

func sealQualificationDatasetFixture(loaded *Loaded) {
	raw, err := json.Marshal(loaded.Dataset)
	if err != nil {
		panic(err)
	}
	loaded.Raw = raw
	loaded.SHA256 = SHA256Hex(raw)
}

func qualificationQuery(id, stratum string) Query {
	return Query{
		ID:         id,
		Stratum:    stratum,
		Language:   "en",
		Split:      SplitDev,
		Text:       "question " + id,
		FamilyID:   "family-" + id,
		Provenance: "independent fresh development curation",
		Judgements: []Judgement{{
			Path:      "answer.go",
			StartLine: 1,
			EndLine:   1,
			Anchor:    "answer",
			Grade:     GradeMax,
			Reason:    "exact answer",
			Annotator: "curator",
			Reviewer:  "reviewer",
		}},
	}
}

func qualificationPreregistrationFixture() QualificationPreregistration {
	potionPin := func(arm QualificationArm, maxTokens int) ArmPin {
		profile := embed.AdmissionSpec{
			TokenizerID:      "model2vec-wordpiece",
			TokenizerSHA256:  static.PinnedSHA256[static.FileTokenizer],
			TokenizerVersion: "1.0",
			MaxTokens:        maxTokens,
			Reserve:          static.SpecialTokenReserve,
			Algorithm:        "first-n-tokens",
			AlgorithmVersion: "1",
		}
		admissionSHA := SHA256Hex([]byte(profile.String()))
		contract := "embedeach-f16-tree"
		if maxTokens != static.DefaultMaxLength {
			contract += "-eval-max-" + strconv.Itoa(maxTokens)
		}
		modelID := static.PinnedSelector + ":" + static.PinnedSHA256[static.FileSafetensors][:12] +
			":mean:" + strconv.FormatBool(static.PinnedNormalize) + ":" + static.PinnedSHA256[static.FileTokenizer][:12] +
			":" + static.PinnedSHA256[static.FileConfig][:12] + ":" + contract + ":" + admissionSHA[:12]
		fingerprint := embed.Fingerprint{
			ModelID:         modelID,
			Revision:        static.PinnedRevision,
			ModelSHA256:     static.PinnedSHA256[static.FileSafetensors],
			TokenizerSHA256: static.PinnedSHA256[static.FileTokenizer],
			Dim:             256,
			DocumentSchema:  embed.DocumentSchema,
			ChunkerConfig:   "",
			GraphGeneration: "graph-1",
		}
		return ArmPin{Label: string(arm), EmbedderID: modelID, FingerprintCanonical: fingerprint.Canonical(),
			ManifestSHA256: strings.Repeat("c", 64), AdmissionSHA256: admissionSHA}
	}
	instructionSHA := SHA256Hex([]byte(coderank.QueryInstruction))
	manifest := coderank.Manifest{
		SchemaVersion: 1,
		Protocol:      coderank.ProtocolVersion,
		Endpoint:      "http://127.0.0.1:8765",
		Model:         coderank.ArtifactPin{ID: "nomic-ai/CodeRankEmbed", Revision: "immutable-model-revision", SHA256: strings.Repeat("a", 64)},
		Tokenizer:     coderank.ArtifactPin{ID: "nomic-ai/CodeRankEmbed", Revision: "immutable-tokenizer-revision", SHA256: strings.Repeat("b", 64)},
		Runtime:       coderank.RuntimePin{Name: "sentence-transformers", Version: "pinned-runtime", SHA256: strings.Repeat("d", 64)},
		Dimension:     768,
		Precision:     "float32",
		Normalization: "l2",
		Compute:       "cpu",
		Admission:     coderank.AdmissionPin{MaxTokens: 8192, Reserve: 0, Algorithm: "first-n-tokens", AlgorithmVersion: "1"},
		Query: coderank.QueryProfilePin{ID: "coderank-code-search-query", Version: "1",
			Instruction: coderank.QueryInstruction, InstructionSHA256: instructionSHA},
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		panic(err)
	}
	manifest.Endpoint = ""
	durableProfile, err := json.Marshal(manifest)
	if err != nil {
		panic(err)
	}
	codeRankID := "coderank:" + manifest.Model.ID + "@" + manifest.Model.Revision + ":" + manifest.IdentityDigest()
	codeRankFingerprint := embed.Fingerprint{
		ModelID:         codeRankID,
		Revision:        manifest.Model.Revision,
		ModelSHA256:     manifest.Model.SHA256,
		TokenizerSHA256: manifest.Tokenizer.SHA256,
		Dim:             manifest.Dimension,
		DocumentSchema:  embed.DocumentSchema,
		ChunkerConfig:   string(durableProfile),
		GraphGeneration: "graph-1",
	}
	return QualificationPreregistration{
		SchemaVersion:       1,
		DatasetSHA256:       strings.Repeat("d", 64),
		SourceRepoSHA:       strings.Repeat("1", 40),
		CandidateSHA:        strings.Repeat("2", 40),
		CandidateDiffSHA256: SHA256Hex(nil),
		ReaderPromptSHA256:  strings.Repeat("4", 64),
		GraderPromptSHA256:  strings.Repeat("5", 64),
		Arms: map[QualificationArm]ArmPin{
			ArmLexical:    {Label: string(ArmLexical)},
			ArmPotion512:  potionPin(ArmPotion512, 512),
			ArmPotion8192: potionPin(ArmPotion8192, 8192),
			ArmCodeRank: {
				Label:                string(ArmCodeRank),
				EmbedderID:           codeRankID,
				FingerprintCanonical: codeRankFingerprint.Canonical(),
				ManifestSHA256:       SHA256Hex(manifestRaw),
				AdmissionSHA256:      SHA256Hex([]byte(manifest.AdmissionSpec().String())),
			},
		},
		CompactVersion:   "compact/17",
		TokenBudget:      1200,
		BootstrapSamples: 100000,
		BootstrapSeed:    8675309,
		Thresholds: QualificationThresholds{
			MinPasses:                     56,
			MinPairedGain:                 9,
			MinWeakStrataWithPositiveGain: 2,
			BootstrapConfidence:           .95,
			MaxSidecarRSSBytes:            2 << 30,
			MaxArtifactBytes:              1 << 30,
			MaxQueryP95Millis:             1000,
			MinQuerySamples:               100,
			MaxReindexSeconds:             600,
		},
		ReferenceMachine: ReferenceMachine{
			OS:             "linux",
			OSVersion:      "6.8",
			CPU:            "reference-cpu",
			PhysicalCores:  8,
			RuntimeThreads: 8,
			BackgroundLoad: "idle; no unrelated processes",
		},
	}
}

func mustQualificationFingerprint(t *testing.T, canonical string) embed.Fingerprint {
	t.Helper()
	fields, ok := decodeQualificationFingerprint(canonical)
	if !ok || len(fields) != 8 {
		t.Fatalf("bad fixture fingerprint %q", canonical)
	}
	dim, err := strconv.Atoi(fields[4])
	if err != nil {
		t.Fatal(err)
	}
	return embed.Fingerprint{
		ModelID:         fields[0],
		Revision:        fields[1],
		ModelSHA256:     fields[2],
		TokenizerSHA256: fields[3],
		Dim:             dim,
		DocumentSchema:  fields[5],
		ChunkerConfig:   fields[6],
		GraphGeneration: fields[7],
	}
}
