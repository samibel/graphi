package evidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AC-12: -record-citations rewrites the stale sha, prints what it touched, and
// leaves everything it was not asked to touch byte-for-byte alone.
func TestAC12_RecordCitationsTouchesOnlyTheStaleRow(t *testing.T) {
	r := newFixtureRepo(t)
	r.write("engine/stale.go", "package engine\n")
	r.write("engine/fresh.go", "package engine\n\n// fresh\n")
	r.commit("v1")
	staleSHA := r.blobSHA("engine/stale.go")
	freshSHA := r.blobSHA("engine/fresh.go")

	r.write("engine/stale.go", "package engine\n\n// reformatted\n")
	r.commit("v2")
	wantSHA := r.blobSHA("engine/stale.go")

	yaml := "" +
		"candidate:\n" +
		"  source: docs/decisions/freeze.md\n" +
		"  sha: " + wantSHA + "\n" +
		"  release_digest: UNKNOWN\n" +
		"gates:\n" +
		"  - id: STALE-ROW\n" +
		"    gate: A   # a trailing comment must survive\n" +
		"    section: plan §1\n" +
		"    status: PASS\n" +
		"    evidence_uri: engine/stale.go\n" +
		"    sha: " + staleSHA + "\n" +
		"  - id: FRESH-ROW\n" +
		"    gate: B\n" +
		"    section: plan §1\n" +
		"    status: PASS\n" +
		"    evidence_uri: engine/fresh.go\n" +
		"    sha: " + freshSHA + "\n"
	r.write(EvidenceYAMLPath, yaml)
	r.commit("index")

	touched, err := RecordCitations(r.root, false)
	if err != nil {
		t.Fatalf("RecordCitations: %v", err)
	}
	if len(touched) != 1 {
		t.Fatalf("want exactly the stale row touched, got %+v", touched)
	}
	if touched[0].GateID != "STALE-ROW" || touched[0].Old != staleSHA || touched[0].New != wantSHA {
		t.Fatalf("wrong re-record: %+v", touched[0])
	}

	got, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(EvidenceYAMLPath)))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(yaml, staleSHA, wantSHA, 1)
	if string(got) != want {
		t.Fatalf("record-citations changed more than the stale sha.\n got: %q\nwant: %q", got, want)
	}
	if !strings.Contains(string(got), "# a trailing comment must survive") {
		t.Fatal("the textual rewrite must preserve comments")
	}

	// The report is the audit trail (AC-12): old -> new, by row.
	out := FormatRerecords(touched)
	for _, want := range []string{"STALE-ROW", staleSHA, wantSHA} {
		if !strings.Contains(out, want) {
			t.Fatalf("the report must name %q:\n%s", want, out)
		}
	}

	// Running it again is a no-op — everything already matches HEAD.
	again, err := RecordCitations(r.root, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("a second run must touch nothing, got %+v", again)
	}
}

// AC-12: a commit sha names a CANDIDATE, not bytes. -record-citations must not
// re-bless it into a blob sha behind the author's back.
func TestAC12_CommitSHAIsNotRerecorded(t *testing.T) {
	r := newFixtureRepo(t)
	r.write("engine/x.go", "package engine\n")
	r.commit("v1")
	commit := r.git("rev-parse", "HEAD")

	yaml := "candidate:\n  source: s\n  sha: " + commit + "\n  release_digest: UNKNOWN\n" +
		"gates:\n  - id: ROW\n    gate: A\n    section: plan §1\n    status: PASS\n    evidence_uri: engine/x.go\n    sha: " + commit + "\n"
	r.write(EvidenceYAMLPath, yaml)
	r.commit("index")

	touched, err := RecordCitations(r.root, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(touched) != 0 {
		t.Fatalf("a commit sha must be left alone, got %+v", touched)
	}
}

// AC-12: -dry-run computes the plan and writes nothing.
func TestAC12_DryRunWritesNothing(t *testing.T) {
	r := newFixtureRepo(t)
	r.write("engine/x.go", "package engine\n")
	r.commit("v1")
	stale := r.blobSHA("engine/x.go")
	r.write("engine/x.go", "package engine\n\n// v2\n")
	r.commit("v2")

	yaml := "candidate:\n  source: s\n  sha: " + stale + "\n  release_digest: UNKNOWN\n" +
		"gates:\n  - id: ROW\n    gate: A\n    section: plan §1\n    status: PASS\n    evidence_uri: engine/x.go\n    sha: " + stale + "\n"
	r.write(EvidenceYAMLPath, yaml)
	r.commit("index")

	touched, err := RecordCitations(r.root, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(touched) != 1 {
		t.Fatalf("the plan must still be computed, got %+v", touched)
	}
	got, _ := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(EvidenceYAMLPath)))
	if string(got) != yaml {
		t.Fatal("-dry-run must not write")
	}
}

// AC-12: the provenance `<path> @ blob <sha>` binding is re-recorded too — that is
// the field SW-194 used to carry the shas the single sha: key has no room for.
func TestAC12_ProvenanceBindingIsRerecorded(t *testing.T) {
	r := newFixtureRepo(t)
	r.write("docs/rc/parity-classes-bash.yaml", "rows: 8\n")
	r.commit("v1")
	stale := r.blobSHA("docs/rc/parity-classes-bash.yaml")
	r.write("docs/rc/parity-classes-bash.yaml", "rows: 8\nnote: reformatted\n")
	r.commit("v2")
	want := r.blobSHA("docs/rc/parity-classes-bash.yaml")

	yaml := "candidate:\n  source: s\n  sha: " + want + "\n  release_digest: UNKNOWN\n" +
		"gates:\n  - id: ROW\n    gate: A\n    section: plan §1\n    status: UNKNOWN\n" +
		"    provenance: \"the class YAML docs/rc/parity-classes-bash.yaml (8 rows PROVEN) @ blob " + stale + " is the pin\"\n"
	r.write(EvidenceYAMLPath, yaml)
	r.commit("index")

	touched, err := RecordCitations(r.root, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(touched) != 1 || touched[0].Field != "provenance" || touched[0].New != want {
		t.Fatalf("want the provenance binding re-recorded, got %+v", touched)
	}
	got, _ := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(EvidenceYAMLPath)))
	if !strings.Contains(string(got), "@ blob "+want) {
		t.Fatalf("the provenance binding was not rewritten:\n%s", got)
	}
}

// A stale provenance binding is a violation of AC-1 in its own right.
func TestAC1_StaleProvenanceBindingFails(t *testing.T) {
	r := newFixtureRepo(t)
	r.write("docs/rc/parity-classes-bash.yaml", "rows: 8\n")
	r.commit("v1")
	stale := r.blobSHA("docs/rc/parity-classes-bash.yaml")
	r.write("docs/rc/parity-classes-bash.yaml", "rows: 8\nnote: reformatted\n")
	r.commit("v2")

	idx := Index{Gates: []Gate{{
		ID: "ROW", Gate: "A", Section: "plan §1", Status: StatusUnknown,
		Provenance: "docs/rc/parity-classes-bash.yaml (8 rows PROVEN) @ blob " + stale,
	}}}
	rep := r.check(idx, Grandfather{})
	if !hasViolation(rep, RuleSHAMismatch, "parity-classes-bash.yaml") {
		t.Fatalf("a stale provenance blob binding must fail:\n%s", rep.FormatCitations())
	}
}
