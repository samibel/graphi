package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/eval/retrieval"
)

// TestDevGradingPackets is a development instrument, env-gated: it builds
// grader packets for a DEVELOPMENT grading run with the same packet builder
// the sealed holdout uses, from the questions, bundles and raw rater
// responses that TestDevGradingCapture and the rater script wrote. It grades
// nothing; the grader is run outside, exactly as in a holdout.
//
//	GRAPHI_DEV_GRADING_DIR    run directory with questions.json, bundles/, responses-raw/
//	GRAPHI_DEV_GRADING_RUBRIC path of the rubric to embed (the version-2 transcript rubric)
func TestDevGradingPackets(t *testing.T) {
	dir := os.Getenv("GRAPHI_DEV_GRADING_DIR")
	rubricPath := os.Getenv("GRAPHI_DEV_GRADING_RUBRIC")
	if dir == "" || rubricPath == "" {
		t.Skip("set GRAPHI_DEV_GRADING_DIR and GRAPHI_DEV_GRADING_RUBRIC")
	}
	rubric, err := os.ReadFile(rubricPath)
	if err != nil {
		t.Fatal(err)
	}
	rubricSHA := retrieval.SHA256Hex(rubric)
	type question struct {
		ID         string                `json:"id"`
		Stratum    string                `json:"stratum"`
		Text       string                `json:"text"`
		Judgements []retrieval.Judgement `json:"judgements"`
	}
	raw, err := os.ReadFile(filepath.Join(dir, "questions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var questions []question
	if err := json.Unmarshal(raw, &questions); err != nil {
		t.Fatal(err)
	}
	for _, q := range questions {
		if !strings.HasPrefix(q.ID, "cd-") {
			t.Fatalf("refusing %s: this instrument runs over the reviewed development split only", q.ID)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "grader-packets"), 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "responses-raw"))
	if err != nil {
		t.Fatal(err)
	}
	packets := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".txt") {
			continue
		}
		parts := strings.SplitN(strings.TrimSuffix(name, ".txt"), "--", 2)
		if len(parts) != 2 {
			t.Fatalf("raw response %s is not <query>--<rater>.txt", name)
		}
		queryID, raterID := parts[0], parts[1]
		var q *question
		for i := range questions {
			if questions[i].ID == queryID {
				q = &questions[i]
			}
		}
		if q == nil {
			t.Fatalf("raw response %s names an unknown query", name)
		}
		var bundle retrieval.CapturedCandidateBundle
		bundleRaw, err := os.ReadFile(filepath.Join(dir, "bundles", retrieval.BundleFileName(queryID)))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(bundleRaw, &bundle); err != nil {
			t.Fatal(err)
		}
		text, err := os.ReadFile(filepath.Join(dir, "responses-raw", name))
		if err != nil {
			t.Fatal(err)
		}
		response := retrieval.RaterResponse{QueryID: queryID, RaterID: raterID, Text: strings.TrimSpace(string(text)), SHA256: retrieval.SHA256Hex(text)}
		query := retrieval.Query{ID: q.ID, Text: q.Text, Stratum: q.Stratum, Judgements: q.Judgements}
		packet := buildGraderPacket(query, bundle, response, rubricPath, rubricSHA, rubric)
		if err := os.WriteFile(filepath.Join(dir, "grader-packets", name), []byte(packet), 0o644); err != nil {
			t.Fatal(err)
		}
		packets++
	}
	t.Logf("dev grading packets: %d written under %s with rubric %s", packets, dir, rubricSHA[:12])
}
