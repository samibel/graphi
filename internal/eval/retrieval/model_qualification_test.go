package retrieval

import (
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/embed"
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
			if err := ValidateQualificationDataset(loaded); err == nil {
				t.Fatalf("accepted dataset with %s", tc.name)
			}
		})
	}
}

func TestValidateQualificationDatasetRequiresUniqueFamilies(t *testing.T) {
	loaded := qualificationDatasetFixture()
	loaded.Dataset.Queries[1].FamilyID = loaded.Dataset.Queries[0].FamilyID

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
			l.Path = "/tmp/renamed.json"
		}},
		{name: "spent id padded with whitespace", mutate: func(l *Loaded) {
			l.Dataset.ID = "\t" + spentID + " "
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
	return &Loaded{
		Dataset: dataset,
		Path:    "/tmp/fresh-development.json",
		SHA256:  strings.Repeat("e", 64),
	}
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
	semanticPin := func(arm QualificationArm, admissionByte string, dim int) ArmPin {
		fingerprint := embed.Fingerprint{
			ModelID:         "embedder-" + string(arm),
			Revision:        "revision-1",
			ModelSHA256:     strings.Repeat("a", 64),
			TokenizerSHA256: strings.Repeat("b", 64),
			Dim:             dim,
			DocumentSchema:  "v2",
			ChunkerConfig:   "admission/1",
			GraphGeneration: "graph-1",
		}
		return ArmPin{
			Label:                string(arm),
			EmbedderID:           fingerprint.ModelID,
			FingerprintCanonical: fingerprint.Canonical(),
			ManifestSHA256:       strings.Repeat("c", 64),
			AdmissionSHA256:      strings.Repeat(admissionByte, 64),
		}
	}
	return QualificationPreregistration{
		SchemaVersion:       1,
		DatasetSHA256:       strings.Repeat("d", 64),
		SourceRepoSHA:       strings.Repeat("1", 40),
		CandidateSHA:        strings.Repeat("2", 40),
		CandidateDiffSHA256: strings.Repeat("3", 64),
		ReaderPromptSHA256:  strings.Repeat("4", 64),
		GraderPromptSHA256:  strings.Repeat("5", 64),
		Arms: map[QualificationArm]ArmPin{
			ArmLexical:    {Label: string(ArmLexical)},
			ArmPotion512:  semanticPin(ArmPotion512, "6", 128),
			ArmPotion8192: semanticPin(ArmPotion8192, "7", 128),
			ArmCodeRank:   semanticPin(ArmCodeRank, "8", 768),
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
