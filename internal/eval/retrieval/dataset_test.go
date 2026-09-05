package retrieval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/trust"
)

// validDataset is the smallest dataset that passes Validate; the table tests
// below each break exactly one rule of it.
func validDataset() Dataset {
	j := func(path string, start, end, grade int) Judgement {
		return Judgement{
			Path: path, StartLine: start, EndLine: end, Anchor: "func", Grade: grade,
			Reason: "because", Annotator: "claude-delegate (SW-258 build)", Reviewer: "orchestrator",
		}
	}
	return Dataset{
		SchemaVersion: SchemaVersion,
		ID:            "t-v1",
		Repo:          "fixture",
		Language:      "go",
		EvidenceClass: EvidenceClassAgentHumanReviewed,
		Queries: []Query{
			{ID: "q1", Stratum: StratumExactIdentifier, Language: "en", Split: SplitDev, Text: "ValidateToken",
				Judgements: []Judgement{j("auth/token.go", 40, 55, 3), j("auth/token.go", 27, 38, 1)}},
			{ID: "q2", Stratum: StratumNoHit, Language: "en", Split: SplitHoldout, Text: "kubernetes pod scheduler",
				Judgements: []Judgement{j("cmd/app/main.go", 1, 10, 0)}},
		},
	}
}

func TestDataset_ValidateAcceptsTheMinimalDataset(t *testing.T) {
	ds := validDataset()
	if err := ds.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
	if got := ds.MinGrade(); got != DefaultRelevantMinGrade {
		t.Errorf("MinGrade() = %d, want %d", got, DefaultRelevantMinGrade)
	}
}

func TestDataset_ValidateRejectsEachBrokenRule(t *testing.T) {
	cases := []struct {
		name  string
		mut   func(*Dataset)
		wants string
	}{
		{"wrong schema version", func(d *Dataset) { d.SchemaVersion = 99 }, "schema_version"},
		{"missing id", func(d *Dataset) { d.ID = "" }, "id"},
		{"missing repo", func(d *Dataset) { d.Repo = "" }, "repo"},
		{"missing language", func(d *Dataset) { d.Language = "" }, "language"},
		{"missing evidence class", func(d *Dataset) { d.EvidenceClass = "" }, "evidence_class"},
		{"no queries", func(d *Dataset) { d.Queries = nil }, "no queries"},
		{"duplicate query id", func(d *Dataset) { d.Queries[1].ID = "q1" }, "duplicate"},
		{"unknown stratum", func(d *Dataset) { d.Queries[0].Stratum = "vibes" }, "stratum"},
		{"unknown split", func(d *Dataset) { d.Queries[0].Split = "test" }, "split"},
		{"empty query text", func(d *Dataset) { d.Queries[0].Text = " " }, "query"},
		{"empty query language", func(d *Dataset) { d.Queries[0].Language = "" }, "language"},
		{"absolute path", func(d *Dataset) { d.Queries[0].Judgements[0].Path = "/etc/passwd" }, "path"},
		{"parent path", func(d *Dataset) { d.Queries[0].Judgements[0].Path = "../x.go" }, "path"},
		{"backslash path", func(d *Dataset) { d.Queries[0].Judgements[0].Path = `auth\token.go` }, "path"},
		{"path over the artifact bound", func(d *Dataset) {
			d.Queries[0].Judgements[0].Path = strings.Repeat("a", trust.MaxPathLength) + ".go"
		}, "bound"},
		{"path carrying the truncation marker", func(d *Dataset) {
			d.Queries[0].Judgements[0].Path = "auth/" + TruncationMarker + ".go"
		}, "marker"},
		{"start line zero", func(d *Dataset) { d.Queries[0].Judgements[0].StartLine = 0 }, "start_line"},
		{"end before start", func(d *Dataset) { d.Queries[0].Judgements[0].EndLine = 3 }, "end_line"},
		{"grade too high", func(d *Dataset) { d.Queries[0].Judgements[0].Grade = 4 }, "grade"},
		{"grade negative", func(d *Dataset) { d.Queries[0].Judgements[0].Grade = -1 }, "grade"},
		{"missing reason", func(d *Dataset) { d.Queries[0].Judgements[0].Reason = "" }, "reason"},
		{"missing reviewer", func(d *Dataset) { d.Queries[0].Judgements[0].Reviewer = "" }, "reviewer"},
		{"missing annotator", func(d *Dataset) { d.Queries[0].Judgements[0].Annotator = "" }, "annotator"},
		{"missing anchor", func(d *Dataset) { d.Queries[0].Judgements[0].Anchor = "" }, "anchor"},
		{"no relevant span on a hit query", func(d *Dataset) {
			d.Queries[0].Judgements[0].Grade = 1
		}, "relevant"},
		{"relevant span on a no_hit query", func(d *Dataset) {
			d.Queries[1].Judgements[0].Grade = 2
		}, "no_hit"},
		{"relevant min grade out of range", func(d *Dataset) { d.RelevantMinGrade = 4 }, "relevant_min_grade"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ds := validDataset()
			tc.mut(&ds)
			err := ds.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want an error mentioning %q", tc.wants)
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("Validate() = %q, want it to mention %q", err, tc.wants)
			}
		})
	}
}

// family_id and provenance are optional at the schema level because cobra-v1
// predates them and is frozen. What is not optional is consistency: a dataset
// carries them for every query or for none, and no family may straddle the
// dev/holdout line. Both rules exist because the failure they prevent is
// silent - an unlabelled paraphrase, or a holdout answer already visible in
// dev - and neither would show up as a broken run.
func TestDataset_ValidateFamilyAndProvenance(t *testing.T) {
	label := func(d *Dataset, i int, family, provenance string) {
		d.Queries[i].FamilyID = family
		d.Queries[i].Provenance = provenance
	}

	t.Run("a dataset with no family_id at all is still valid", func(t *testing.T) {
		ds := validDataset()
		if err := ds.Validate(); err != nil {
			t.Fatalf("Validate() = %v, want nil", err)
		}
	})

	t.Run("every query labelled, families within one split", func(t *testing.T) {
		ds := validDataset()
		label(&ds, 0, "cobra-family-aaaaaaaaaaaaaaaa", "github:spf13/cobra#1")
		label(&ds, 1, "cobra-family-bbbbbbbbbbbbbbbb", "dataset:cobra-v1:cb-01")
		if err := ds.Validate(); err != nil {
			t.Fatalf("Validate() = %v, want nil", err)
		}
	})

	t.Run("provenance everywhere and family_id nowhere is refused", func(t *testing.T) {
		// The mirror image of the case below, and the direction validateFamilies
		// used to wave through: every row carries a provenance, none carries a
		// family, so nothing stops a paraphrase of a holdout question sitting in
		// dev unlabelled.
		ds := validDataset()
		for i := range ds.Queries {
			ds.Queries[i].Provenance = "github:spf13/cobra#1"
		}
		err := ds.Validate()
		if err == nil || !strings.Contains(err.Error(), "for every query or for none") {
			t.Fatalf("Validate() = %v, want an all-or-nothing family error", err)
		}
	})

	t.Run("provenance on one query and family_id nowhere is refused", func(t *testing.T) {
		ds := validDataset()
		ds.Queries[0].Provenance = "github:spf13/cobra#1"
		err := ds.Validate()
		if err == nil || !strings.Contains(err.Error(), "for every query or for none") {
			t.Fatalf("Validate() = %v, want an all-or-nothing family error", err)
		}
	})

	t.Run("some labelled and some not is refused", func(t *testing.T) {
		ds := validDataset()
		label(&ds, 0, "cobra-family-aaaaaaaaaaaaaaaa", "github:spf13/cobra#1")
		err := ds.Validate()
		if err == nil || !strings.Contains(err.Error(), "for every query or for none") {
			t.Fatalf("Validate() = %v, want an all-or-nothing family error", err)
		}
	})

	t.Run("family_id without provenance is refused", func(t *testing.T) {
		ds := validDataset()
		label(&ds, 0, "cobra-family-aaaaaaaaaaaaaaaa", "")
		label(&ds, 1, "cobra-family-bbbbbbbbbbbbbbbb", "dataset:cobra-v1:cb-01")
		err := ds.Validate()
		if err == nil || !strings.Contains(err.Error(), "provenance is empty") {
			t.Fatalf("Validate() = %v, want a missing-provenance error", err)
		}
	})

	t.Run("a family crossing dev and holdout is refused", func(t *testing.T) {
		ds := validDataset()
		// q1 is dev and q2 is holdout in the minimal dataset; putting them in
		// one family is exactly the leak family_id exists to stop.
		label(&ds, 0, "cobra-family-aaaaaaaaaaaaaaaa", "github:spf13/cobra#1")
		label(&ds, 1, "cobra-family-aaaaaaaaaaaaaaaa", "dataset:cobra-v1:cb-01")
		err := ds.Validate()
		if err == nil || !strings.Contains(err.Error(), "crosses splits") {
			t.Fatalf("Validate() = %v, want a cross-split family error", err)
		}
	})
}

func TestDataset_RelevantSpansHonourTheMinimumGrade(t *testing.T) {
	ds := validDataset()
	if got := ds.Queries[0].RelevantSpans(ds.MinGrade()); len(got) != 1 || got[0].Grade != 3 {
		t.Errorf("RelevantSpans(2) = %+v, want only the grade-3 span", got)
	}
	ds.RelevantMinGrade = 1
	if got := ds.Queries[0].RelevantSpans(ds.MinGrade()); len(got) != 2 {
		t.Errorf("RelevantSpans(1) = %d spans, want 2", len(got))
	}
}

func TestLoadDataset_ReadsValidatesAndDigests(t *testing.T) {
	ds := validDataset()
	raw, err := json.Marshal(ds)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ds.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadDataset(path)
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}
	if loaded.Dataset.ID != "t-v1" || len(loaded.SHA256) != 64 || loaded.Path != path {
		t.Errorf("loaded = %+v", loaded)
	}
	if loaded.SHA256 != SHA256Hex(raw) {
		t.Errorf("SHA256 = %s, want the digest of the file bytes", loaded.SHA256)
	}

	t.Run("an unreadable dataset is an error, never an empty run", func(t *testing.T) {
		if _, err := LoadDataset(filepath.Join(t.TempDir(), "missing.json")); err == nil {
			t.Error("LoadDataset(missing) = nil error")
		}
		bad := filepath.Join(t.TempDir(), "bad.json")
		if err := os.WriteFile(bad, []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadDataset(bad); err == nil {
			t.Error("LoadDataset(malformed) = nil error")
		}
	})
	t.Run("an invalid dataset is refused at load", func(t *testing.T) {
		ds := validDataset()
		ds.Queries[0].Stratum = "vibes"
		raw, _ := json.Marshal(ds)
		p := filepath.Join(t.TempDir(), "invalid.json")
		if err := os.WriteFile(p, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadDataset(p); err == nil || !strings.Contains(err.Error(), "stratum") {
			t.Errorf("LoadDataset(invalid) = %v, want a stratum validation error", err)
		}
	})
}

func TestDataset_CheckDevelopmentRequirements(t *testing.T) {
	ds := validDataset()
	err := ds.CheckDevelopmentRequirements(30, 3, 10)
	if err == nil {
		t.Fatal("two queries must not satisfy the 30/3/10 development requirement")
	}
	for _, want := range []string{"dev", "holdout", StratumExactPath} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if err := ds.CheckDevelopmentRequirements(1, 0, 1); err != nil {
		t.Errorf("relaxed requirement: %v", err)
	}
}
