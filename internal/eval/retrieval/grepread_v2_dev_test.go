package retrieval

// This file is an opt-in DEVELOPMENT-ONLY diagnostic. GrepReadV2 finishes its
// query-only transcript before this test consults any judgement.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	evaltokenizer "github.com/samibel/graphi/internal/eval/tokenizer"
)

type grepReadV2DevSource struct {
	Sequence int    `json:"response_sequence"`
	Path     string `json:"path"`
	Start    int    `json:"start_line"`
	End      int    `json:"end_line"`
}

func grepReadV2DevSources(repository fs.FS, transcript GrepReadV2Transcript) ([]grepReadV2DevSource, error) {
	if err := transcript.Validate(); err != nil {
		return nil, err
	}
	linesFor := func(name string) ([][]byte, error) {
		raw, err := fs.ReadFile(repository, name)
		if err != nil {
			return nil, err
		}
		lines := bytes.SplitAfter(raw, []byte{'\n'})
		if len(lines[len(lines)-1]) == 0 {
			lines = lines[:len(lines)-1]
		}
		return lines, nil
	}

	var sources []grepReadV2DevSource
	grep := transcript.Ledger.Responses[0]
	for _, row := range strings.Split(string(grep.Bytes), "\n") {
		if row == "" || strings.HasPrefix(row, "grep:error:") {
			continue
		}
		fields := strings.SplitN(row, ":", 4)
		if len(fields) != 4 {
			return nil, fmt.Errorf("malformed grep row %q", row)
		}
		line, err := strconv.Atoi(fields[1])
		if err != nil || line < 1 {
			return nil, fmt.Errorf("invalid grep line %q", fields[1])
		}
		column, err := strconv.Atoi(fields[2])
		if err != nil || column < 1 || column > max(1, len(fields[3])) {
			return nil, fmt.Errorf("invalid grep column %q", fields[2])
		}
		lines, err := linesFor(fields[0])
		if err != nil || line > len(lines) {
			return nil, fmt.Errorf("grep source %s:%d missing: %v", fields[0], line, err)
		}
		want := bytes.TrimSuffix(bytes.TrimSuffix(lines[line-1], []byte{'\n'}), []byte{'\r'})
		if !bytes.Equal(want, []byte(fields[3])) {
			return nil, fmt.Errorf("grep bytes differ from %s:%d", fields[0], line)
		}
		sources = append(sources, grepReadV2DevSource{grep.Sequence, fields[0], line, line})
	}
	for _, read := range transcript.Reads {
		payload := transcript.Ledger.Responses[read.ResponseSequence-1]
		if read.EndLine < read.StartLine {
			continue
		}
		lines, err := linesFor(read.Path)
		if err != nil || read.EndLine > len(lines) {
			return nil, fmt.Errorf("read source %s:%d-%d missing: %v", read.Path, read.StartLine, read.EndLine, err)
		}
		if !bytes.Equal(payload.Bytes, bytes.Join(lines[read.StartLine-1:read.EndLine], nil)) {
			return nil, fmt.Errorf("read bytes differ from %s:%d-%d", read.Path, read.StartLine, read.EndLine)
		}
		sources = append(sources, grepReadV2DevSource{payload.Sequence, read.Path, read.StartLine, read.EndLine})
	}
	return sources, nil
}

func grepReadV2DevCoverage(sources []grepReadV2DevSource, judgements []Judgement, maxSequence int) (total, overlap, complete int) {
	for _, judgement := range judgements {
		if judgement.Grade != GradeMax {
			continue
		}
		total++
		var intervals []grepReadV2DevSource
		for _, source := range sources {
			if source.Sequence > maxSequence || !SpanMatches(source.Path, max(source.Start, judgement.StartLine), judgement) {
				continue
			}
			if source.End >= judgement.StartLine && source.Start <= judgement.EndLine {
				intervals = append(intervals, source)
			}
		}
		if len(intervals) == 0 {
			continue
		}
		overlap++
		sort.Slice(intervals, func(i, j int) bool {
			if intervals[i].Start != intervals[j].Start {
				return intervals[i].Start < intervals[j].Start
			}
			return intervals[i].End < intervals[j].End
		})
		coveredThrough := judgement.StartLine - 1
		for _, interval := range intervals {
			if interval.Start > coveredThrough+1 {
				continue
			}
			coveredThrough = max(coveredThrough, interval.End)
		}
		if coveredThrough >= judgement.EndLine {
			complete++
		}
	}
	return total, overlap, complete
}

func TestGrepReadV2DevCapture(t *testing.T) {
	out := os.Getenv("GRAPHI_GREPREAD_V2_DEV_OUT")
	if out == "" {
		t.Skip("set GRAPHI_GREPREAD_V2_DEV_OUT and GRAPHI_RECOVERY_COBRA for the development-only /2 diagnostic")
	}
	root := os.Getenv("GRAPHI_RECOVERY_COBRA")
	if root == "" {
		t.Fatal("GRAPHI_RECOVERY_COBRA is required")
	}
	module := grepReadV2ModuleRoot(t)
	ds, err := LoadDataset(filepath.Join(module, "docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/dataset.json"))
	if err != nil {
		t.Fatal(err)
	}
	if ds.SHA256 != "2d05e3bb015a1447e0c31a9a855712e6aae6f4281adbf7acd72e86c923a43d6c" || len(ds.Dataset.Queries) != 44 {
		t.Fatal("development dataset changed")
	}
	for _, query := range ds.Dataset.Queries {
		if query.Split != SplitDev {
			t.Fatal("refusing non-development query", query.ID)
		}
	}
	head, err := CheckoutHEAD(t.Context(), root)
	if err != nil || head != ds.Dataset.RepoSHA {
		t.Fatalf("checkout: %s %v", head, err)
	}
	clean, err := GitRepoProbe().WorktreeClean(t.Context(), root)
	if err != nil || !clean {
		t.Fatalf("checkout must be clean: %v", err)
	}
	if err := CheckSpanCoverage(root, ds.Dataset); err != nil {
		t.Fatal(err)
	}
	tok, err := evaltokenizer.Load("../tokenizer/testdata/artifact")
	if err != nil {
		t.Fatal(err)
	}
	counter, err := NewPinnedRealPayloadCounter(tok)
	if err != nil {
		t.Fatal(err)
	}

	type observation struct {
		QueryID               string                `json:"query_id"`
		Stratum               string                `json:"stratum"`
		Grade3                int                   `json:"grade3_spans"`
		Overlap               int                   `json:"overlap_grade3_spans"`
		Complete              int                   `json:"strictly_contained_grade3_spans"`
		FirstOverlapResponse  int                   `json:"first_overlap_response,omitempty"`
		TokensToFirstOverlap  *int                  `json:"tokens_to_first_overlap,omitempty"`
		FirstCompleteResponse int                   `json:"first_strict_containment_response,omitempty"`
		TokensToFirstComplete *int                  `json:"tokens_to_first_strict_containment,omitempty"`
		CompleteTokens        int                   `json:"complete_transcript_tokens"`
		CompleteBytes         int                   `json:"complete_transcript_bytes"`
		Transcript            GrepReadV2Transcript  `json:"transcript"`
		TranscriptSHA         string                `json:"transcript_sha256"`
		Sources               []grepReadV2DevSource `json:"verified_emitted_source"`
		Payloads              []PreservedPayload    `json:"preserved_payloads"`
	}
	report := struct {
		Status                  string        `json:"status"`
		Note                    string        `json:"note"`
		DatasetSHA              string        `json:"dataset_sha256"`
		RepoSHA                 string        `json:"repo_sha"`
		Population              int           `json:"grade3_answerable_dev_queries"`
		OverlapQueries          int           `json:"queries_reaching_equal_recall_overlap"`
		EqualRecallMisses       []string      `json:"zero_overlap_query_ids"`
		CompleteQueries         int           `json:"queries_with_strict_span_containment"`
		StrictContainmentMisses []string      `json:"zero_strict_containment_query_ids"`
		Excluded                []string      `json:"no_grade3_query_ids"`
		Identical               int           `json:"identical_transcripts"`
		TotalTokensToOverlap    int           `json:"total_tokens_to_first_overlap"`
		MedianTokensToOverlap   int           `json:"median_tokens_to_first_overlap"`
		TotalCompleteTokens     int           `json:"total_complete_transcript_tokens"`
		MedianCompleteTokens    int           `json:"median_complete_transcript_tokens"`
		Rows                    []observation `json:"queries"`
	}{
		Status: "development_diagnostic_not_release", DatasetSHA: ds.SHA256, RepoSHA: head,
		Note:                    "GrepRead/2 prototype only. All transcripts finish from repository plus query text before grade-3 judgements are consulted. The frozen exact-span target credits any source-verified overlap with one atomic reviewed span; strict full-span containment is reported separately as a harder diagnostic. No holdout access and no release/savings claim.",
		EqualRecallMisses:       []string{},
		StrictContainmentMisses: []string{},
		Excluded:                []string{},
	}
	var completeTokenCounts, overlapTokenCounts []int
	for _, query := range ds.Dataset.Queries {
		transcript := GrepReadV2(os.DirFS(root), query.Text)
		repeated := GrepReadV2(os.DirFS(root), query.Text)
		if !reflect.DeepEqual(transcript, repeated) || transcript.DigestSHA256() != repeated.DigestSHA256() {
			t.Fatal("non-deterministic comparator", query.ID)
		}
		report.Identical++
		sources, err := grepReadV2DevSources(os.DirFS(root), transcript)
		if err != nil {
			t.Fatalf("%s: %v", query.ID, err)
		}
		payloads, err := transcript.Ledger.PreservedPayloads(counter)
		if err != nil {
			t.Fatal(err)
		}
		tokens := 0
		for _, payload := range payloads {
			for _, count := range payload.TokenCounts {
				if count.TokenizerID == counter.TokenizerID {
					tokens += count.Tokens
				}
			}
		}
		total, overlap, complete := grepReadV2DevCoverage(sources, query.Judgements, len(payloads))
		row := observation{
			QueryID: query.ID, Stratum: query.Stratum, Grade3: total, Overlap: overlap, Complete: complete,
			CompleteTokens: tokens, CompleteBytes: transcript.Ledger.TotalByteCount(), Transcript: transcript,
			TranscriptSHA: transcript.DigestSHA256(), Sources: sources, Payloads: payloads,
		}
		if total == 0 {
			report.Excluded = append(report.Excluded, query.ID)
		} else {
			report.Population++
			report.TotalCompleteTokens += tokens
			completeTokenCounts = append(completeTokenCounts, tokens)
			if overlap > 0 {
				report.OverlapQueries++
				for sequence := 1; sequence <= len(payloads); sequence++ {
					_, prefixOverlap, _ := grepReadV2DevCoverage(sources, query.Judgements, sequence)
					if prefixOverlap == 0 {
						continue
					}
					prefixTokens := grepReadV2PayloadTokens(payloads[:sequence], counter.TokenizerID)
					row.FirstOverlapResponse = sequence
					row.TokensToFirstOverlap = &prefixTokens
					report.TotalTokensToOverlap += prefixTokens
					overlapTokenCounts = append(overlapTokenCounts, prefixTokens)
					break
				}
			} else {
				report.EqualRecallMisses = append(report.EqualRecallMisses, query.ID)
			}
			if complete > 0 {
				report.CompleteQueries++
				for sequence := 1; sequence <= len(payloads); sequence++ {
					_, _, prefixComplete := grepReadV2DevCoverage(sources, query.Judgements, sequence)
					if prefixComplete == 0 {
						continue
					}
					prefixTokens := grepReadV2PayloadTokens(payloads[:sequence], counter.TokenizerID)
					row.FirstCompleteResponse = sequence
					row.TokensToFirstComplete = &prefixTokens
					break
				}
			} else {
				report.StrictContainmentMisses = append(report.StrictContainmentMisses, query.ID)
			}
		}
		report.Rows = append(report.Rows, row)
	}
	if report.Population != 40 || len(report.Rows) != 44 || report.Identical != 44 {
		t.Fatal("incomplete development run")
	}
	sort.Ints(completeTokenCounts)
	sort.Ints(overlapTokenCounts)
	report.MedianCompleteTokens = (completeTokenCounts[19] + completeTokenCounts[20]) / 2
	if len(overlapTokenCounts) == 40 {
		report.MedianTokensToOverlap = (overlapTokenCounts[19] + overlapTokenCounts[20]) / 2
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("GrepRead/2 dev: equal-recall overlap %d/40, misses %v; strict containment %d/40", report.OverlapQueries, report.EqualRecallMisses, report.CompleteQueries)
	if report.OverlapQueries == 40 {
		t.Logf("tokens to first equal-recall overlap: mean %.1f, median %d", float64(report.TotalTokensToOverlap)/40, report.MedianTokensToOverlap)
	}
	t.Logf("complete transcript cl100k: mean %.1f, median %d", float64(report.TotalCompleteTokens)/40, report.MedianCompleteTokens)
}

func grepReadV2PayloadTokens(payloads []PreservedPayload, tokenizerID string) int {
	total := 0
	for _, payload := range payloads {
		for _, count := range payload.TokenCounts {
			if count.TokenizerID == tokenizerID {
				total += count.Tokens
			}
		}
	}
	return total
}

func grepReadV2ModuleRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve GrepRead/2 test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "../../.."))
}
