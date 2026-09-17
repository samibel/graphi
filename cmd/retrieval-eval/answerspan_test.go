package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/eval/retrieval"
)

// TestAnswerSpanCeiling_AggregateNamesNothingDetailIsOptIn is the property a
// curator relies on: the report that comes back from a sealed split can be
// shared, because it carries counts only; the per-query file exists only when
// asked for by name.
func TestAnswerSpanCeiling_AggregateNamesNothingDetailIsOptIn(t *testing.T) {
	chdirRoot(t)
	loaded, err := retrieval.LoadDataset(filepath.FromSlash("internal/eval/retrieval/testdata/datasets/fixture-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "ceiling.json")
	var stdout, stderr bytes.Buffer
	code := run([]string{"-answer-span-ceiling", "-repo", FixtureRepoName,
		"-dataset", "internal/eval/retrieval/testdata/datasets/fixture-v1.json", "-out", out}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var ceiling retrieval.AnswerSpanCeiling
	if err := json.Unmarshal(raw, &ceiling); err != nil {
		t.Fatal(err)
	}
	if ceiling.DatasetSHA256 != loaded.SHA256 || ceiling.Overall.Answerable == 0 || len(ceiling.Splits) != 2 {
		t.Fatalf("ceiling = %+v", ceiling)
	}
	for _, q := range loaded.Dataset.Queries {
		if bytes.Contains(raw, []byte(q.ID)) || bytes.Contains(raw, []byte(q.Text)) {
			t.Fatalf("aggregate report leaks query %s", q.ID)
		}
		for _, j := range q.Judgements {
			if bytes.Contains(raw, []byte(j.Path)) {
				t.Fatalf("aggregate report leaks path %s", j.Path)
			}
		}
	}
	if entries, _ := os.ReadDir(filepath.Dir(out)); len(entries) != 1 {
		t.Fatalf("detail was written without being asked for: %v", entries)
	}
	if !strings.Contains(stderr.String(), "any_complete_feasible=") {
		t.Fatalf("no summary line on stderr: %s", stderr.String())
	}

	detail := filepath.Join(t.TempDir(), "detail.json")
	stderr.Reset()
	code = run([]string{"-answer-span-ceiling", "-repo", FixtureRepoName,
		"-dataset", "internal/eval/retrieval/testdata/datasets/fixture-v1.json", "-out", out, "-answer-span-detail", detail}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	detailRaw, err := os.ReadFile(detail)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(detailRaw, []byte(`"query_id"`)) || !bytes.Contains(detailRaw, []byte(`"start_line"`)) {
		t.Fatalf("detail file lacks per-query fields: %s", detailRaw)
	}
	if !strings.Contains(stderr.String(), "must not leave") {
		t.Fatalf("detail write was not warned about: %s", stderr.String())
	}
}

// TestAnswerSpanCeiling_RefusesAMispinnedCheckout: a dataset that cites a sha
// is priced only against a checkout at that sha. This repository is not the
// Cobra checkout, so pointing the dev dataset at it must be refused before any
// span is read.
func TestAnswerSpanCeiling_RefusesAMispinnedCheckout(t *testing.T) {
	chdirRoot(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"-answer-span-ceiling", "-checkout", ".",
		"-dataset", "docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/dataset.json"}, &stdout, &stderr)
	if code != exitUsage || !strings.Contains(stderr.String(), "cites sha") {
		t.Fatalf("exit %d, stderr %q; want a usage refusal naming the sha mismatch", code, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("a refused run wrote a report: %s", stdout.String())
	}
}
