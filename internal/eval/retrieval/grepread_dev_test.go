package retrieval

// This is a development-only impossibility check, NOT the equal-recall release
// scorer. Even a single emitted source line overlapping grade 3 is counted in
// the comparator's favour. Zero overlap therefore proves a miss for every
// permitted positive recall target; nonzero overlap does not prove sufficiency.
import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	evaltokenizer "github.com/samibel/graphi/internal/eval/tokenizer"
)

type grepReadDevSource struct {
	Sequence int    `json:"response_sequence"`
	Path     string `json:"path"`
	Start    int    `json:"start_line"`
	End      int    `json:"end_line"`
}

// Check preserved bytes against source, never manufacture or charge a read
// from its requested window. In particular grep-only hits count, and empty or
// error responses cannot earn credit from their requested coordinates.
// A nil repository performs offline payload/provenance validation only; live
// captures always provide the clean pinned checkout for exact source checking.
func grepReadDevSources(repository fs.FS, transcript GrepReadTranscript) ([]grepReadDevSource, error) {
	if err := transcript.Validate(); err != nil {
		return nil, err
	}
	linesFor := func(path string) ([][]byte, error) {
		raw, err := fs.ReadFile(repository, path)
		if err != nil {
			return nil, err
		}
		lines := bytes.SplitAfter(raw, []byte{'\n'})
		if len(lines[len(lines)-1]) == 0 {
			lines = lines[:len(lines)-1]
		}
		return lines, nil
	}
	var sources []grepReadDevSource
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
		if err != nil || column < 1 || column > len(fields[3]) {
			return nil, fmt.Errorf("invalid grep column %q", fields[2])
		}
		if repository != nil {
			lines, err := linesFor(fields[0])
			if err != nil || line > len(lines) {
				return nil, fmt.Errorf("grep source %s:%d missing: %v", fields[0], line, err)
			}
			want := bytes.TrimSuffix(bytes.TrimSuffix(lines[line-1], []byte{'\n'}), []byte{'\r'})
			if !bytes.Equal(want, []byte(fields[3])) {
				return nil, fmt.Errorf("grep bytes differ from %s:%d", fields[0], line)
			}
		}
		sources = append(sources, grepReadDevSource{grep.Sequence, fields[0], line, line})
	}
	for _, read := range transcript.Reads {
		payload := transcript.Ledger.Responses[read.ResponseSequence-1]
		if read.EndLine < read.StartLine {
			if len(payload.Bytes) != 0 && string(payload.Bytes) != "read:error:"+read.Path+":read_failed\n" && string(payload.Bytes) != "read:error:"+read.Path+":invalid_utf8\n" {
				return nil, fmt.Errorf("unexpected non-source read response %d", payload.Sequence)
			}
			continue
		}
		lineCount := bytes.Count(payload.Bytes, []byte{'\n'})
		if len(payload.Bytes) > 0 && payload.Bytes[len(payload.Bytes)-1] != '\n' {
			lineCount++
		}
		if lineCount != read.EndLine-read.StartLine+1 {
			return nil, fmt.Errorf("read response %d line count differs from provenance", payload.Sequence)
		}
		if repository != nil {
			lines, err := linesFor(read.Path)
			if err != nil || read.EndLine > len(lines) {
				return nil, fmt.Errorf("read source %s:%d-%d missing: %v", read.Path, read.StartLine, read.EndLine, err)
			}
			if !bytes.Equal(payload.Bytes, bytes.Join(lines[read.StartLine-1:read.EndLine], nil)) {
				return nil, fmt.Errorf("read bytes differ from %s:%d-%d", read.Path, read.StartLine, read.EndLine)
			}
		}
		sources = append(sources, grepReadDevSource{payload.Sequence, read.Path, read.StartLine, read.EndLine})
	}
	return sources, nil
}

func grepReadDevOverlap(sources []grepReadDevSource, judgements []Judgement) (total, overlap int) {
	for _, j := range judgements {
		if j.Grade != GradeMax {
			continue
		}
		total++
		for _, source := range sources {
			line := max(j.StartLine, source.Start)
			if line <= source.End && SpanMatches(source.Path, line, j) {
				overlap++
				break
			}
		}
	}
	return total, overlap
}

func TestGrepReadDevSources_PreservedBytesAndOptimisticOverlap(t *testing.T) {
	repository := fstest.MapFS{"a.go": {Data: []byte("// needle\r\npackage sample\r\n// answer")}}
	transcript := GrepRead(repository, "needle")
	sources, err := grepReadDevSources(repository, transcript)
	if err != nil {
		t.Fatal(err)
	}
	judgements := []Judgement{
		{Path: "a.go", StartLine: 1, EndLine: 1, Grade: 3},
		{Path: "a.go", StartLine: 3, EndLine: 3, Grade: 3},
		{Path: "a.go", StartLine: 4, EndLine: 5, Grade: 3},
		{Path: "a.go", StartLine: 1, EndLine: 1, Grade: 2},
	}
	if total, overlap := grepReadDevOverlap(sources, judgements); total != 3 || overlap != 2 {
		t.Fatalf("total/overlap=%d/%d, want 3/2", total, overlap)
	}
	// A read may fail AFTER grep succeeded. Keep credit for the emitted grep
	// line, not the absent answer or hypothetical 40-line requested window.
	transcript.Reads[0].EndLine = 0
	transcript.Ledger.Responses = transcript.Ledger.Responses[:1]
	transcript.Ledger.capture(PayloadBoundaryGrepRead, PayloadOperationRead, []byte("read:error:a.go:read_failed\n"))
	sources, err = grepReadDevSources(repository, transcript)
	if err != nil {
		t.Fatal(err)
	}
	if _, overlap := grepReadDevOverlap(sources, judgements); overlap != 1 {
		t.Fatalf("grep-only overlap=%d, want 1", overlap)
	}
	// Matching coordinates and a self-consistent digest cannot excuse source
	// bytes which were never returned. This must fail even if the ledger passes.
	transcript = GrepRead(repository, "needle")
	transcript.Ledger.Responses = transcript.Ledger.Responses[:1]
	transcript.Ledger.capture(PayloadBoundaryGrepRead, PayloadOperationRead, []byte("fabricated"))
	if _, err := grepReadDevSources(repository, transcript); err == nil {
		t.Fatal("credited coordinates with fabricated source bytes")
	}
}

func TestGrepReadDevSources_SearchCapCanHideExactDefinition(t *testing.T) {
	// Minimal instance of the measured cb-01 failure: early uses fill the
	// frozen global grep cap before a later file's exact declaration is seen.
	repository := fstest.MapFS{
		"a_test.go": {Data: []byte(strings.Repeat("Needle()\n", GrepReadSearchLimit))},
		"z.go":      {Data: []byte("func Needle() {}\n")},
	}
	transcript := GrepRead(repository, "Needle")
	sources, err := grepReadDevSources(repository, transcript)
	if err != nil {
		t.Fatal(err)
	}
	judgements := []Judgement{{Path: "z.go", StartLine: 1, EndLine: 1, Grade: 3}}
	if total, overlap := grepReadDevOverlap(sources, judgements); total != 1 || overlap != 0 {
		t.Fatalf("total/overlap=%d/%d, want 1/0", total, overlap)
	}
	if len(transcript.Reads) != 1 || transcript.Reads[0].Path != "a_test.go" {
		t.Fatal("baseline did not reproduce the global-cap failure")
	}
}

func TestGrepReadDevCapture(t *testing.T) {
	out := os.Getenv("GRAPHI_GREPREAD_DEV_OUT")
	if out == "" {
		t.Skip("set GRAPHI_GREPREAD_DEV_OUT and GRAPHI_RECOVERY_COBRA for the development-only comparator preflight")
	}
	root := os.Getenv("GRAPHI_RECOVERY_COBRA")
	if root == "" {
		t.Fatal("GRAPHI_RECOVERY_COBRA is required")
	}
	ds, err := LoadDataset(filepath.Join(taskContextModuleRoot(t), "docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/dataset.json"))
	if err != nil {
		t.Fatal(err)
	}
	if ds.SHA256 != "2d05e3bb015a1447e0c31a9a855712e6aae6f4281adbf7acd72e86c923a43d6c" || len(ds.Dataset.Queries) != 44 {
		t.Fatal("frozen development population changed")
	}
	for _, q := range ds.Dataset.Queries {
		if q.Split != SplitDev {
			t.Fatal("refusing non-development query", q.ID)
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
	counter := loadEmbeddedRealPayloadCounterForTest(t)
	type observation struct {
		QueryID       string              `json:"query_id"`
		Stratum       string              `json:"stratum"`
		Grade3        int                 `json:"grade3_spans"`
		Overlap       int                 `json:"overlap_upper_bound_grade3_spans"`
		Transcript    GrepReadTranscript  `json:"transcript"`
		TranscriptSHA string              `json:"transcript_sha256"`
		Sources       []grepReadDevSource `json:"verified_emitted_source"`
		Payloads      []PreservedPayload  `json:"preserved_payloads"`
	}
	report := struct {
		Status     string              `json:"status"`
		Note       string              `json:"note"`
		DatasetSHA string              `json:"dataset_sha256"`
		RepoSHA    string              `json:"repo_sha"`
		InputSHA   map[string]string   `json:"implementation_and_contract_sha256"`
		Contract   MeasurementContract `json:"measurement_contract"`
		Population int                 `json:"grade3_answerable_dev_queries"`
		Misses     []string            `json:"zero_overlap_query_ids"`
		Excluded   []string            `json:"no_grade3_query_ids"`
		Identical  int                 `json:"identical_transcripts"`
		Rows       []observation       `json:"queries"`
	}{Status: "development_diagnostic_not_release", DatasetSHA: ds.SHA256, RepoSHA: head, Contract: FrozenMeasurementContract(), Note: "All 44 dev records retained; grade-3 diagnostics cover all 40 answerable dev queries. GrepRead runs to completion with query text only before qrels are consulted. Overlap is an optimistic upper bound, not sufficiency or the equal-recall release scorer. Any zero proves a miss at every permitted positive target and rules out the frozen full-population savings claim, independently of candidate quality. Nonzero overlap does not authorize a claim. No holdout access, no savings aggregate."}
	report.InputSHA = map[string]string{}
	for _, name := range []string{
		"internal/eval/retrieval/baseline.go",
		"internal/eval/retrieval/payload.go",
		"internal/eval/retrieval/metrics.go",
		"internal/eval/retrieval/grepread_dev_test.go",
		"docs/eval/retrieval/methodology.md",
		"docs/eval/retrieval-targets.json",
	} {
		raw, err := os.ReadFile(filepath.Join(taskContextModuleRoot(t), name))
		if err != nil {
			t.Fatal(err)
		}
		report.InputSHA[name] = SHA256Hex(raw)
	}
	for _, q := range ds.Dataset.Queries {
		// The complete judgement-blind transcript exists before any qrel scoring.
		transcript := GrepRead(os.DirFS(root), q.Text)
		repeated := GrepRead(os.DirFS(root), q.Text)
		if !reflect.DeepEqual(transcript, repeated) || transcript.DigestSHA256() != repeated.DigestSHA256() {
			t.Fatal("non-deterministic comparator", q.ID)
		}
		report.Identical++
		sources, err := grepReadDevSources(os.DirFS(root), transcript)
		if err != nil {
			t.Fatalf("%s: %v", q.ID, err)
		}
		payloads, err := transcript.Ledger.PreservedPayloads(counter)
		if err != nil {
			t.Fatal(err)
		}
		total, overlap := grepReadDevOverlap(sources, q.Judgements)
		if total > 0 {
			report.Population++
			if overlap == 0 {
				report.Misses = append(report.Misses, q.ID)
			}
		} else {
			report.Excluded = append(report.Excluded, q.ID)
		}
		report.Rows = append(report.Rows, observation{q.ID, q.Stratum, total, overlap, transcript, transcript.DigestSHA256(), sources, payloads})
	}
	if report.Population != 40 || len(report.Rows) != 44 {
		t.Fatal("incomplete development population")
	}
	if len(report.Misses) > 0 {
		report.Status = "frozen_savings_claim_blocked_by_comparator_misses"
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("GrepRead/1 zero source overlap: %d/%d grade-3 dev queries; identical transcripts: %d/44; excluded (no grade 3): %v", len(report.Misses), report.Population, report.Identical, report.Excluded)
	t.Logf("zero-overlap IDs: %v", report.Misses)
	if os.Getenv("GRAPHI_GREPREAD_REQUIRE_ALL") == "1" && len(report.Misses) > 0 {
		t.Fatal("frozen savings claim unavailable: comparator misses cannot be repaired by candidate changes")
	}
}

// Ordinary offline tests verify population completeness and recompute all
// captured response digests/token counts. The opt-in capture above additionally
// re-executes GrepRead and checks each source byte against the pinned checkout.
func TestGrepReadDevArtifact(t *testing.T) {
	module := taskContextModuleRoot(t)
	ds, err := LoadDataset(filepath.Join(module, "docs/eval/retrieval/runs/2026-09-06-sw282-gate-local/dataset.json"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(module, "docs/eval/retrieval/runs/2026-09-06-release-preflight-dev/grepread.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Status     string   `json:"status"`
		DatasetSHA string   `json:"dataset_sha256"`
		Population int      `json:"grade3_answerable_dev_queries"`
		Misses     []string `json:"zero_overlap_query_ids"`
		Excluded   []string `json:"no_grade3_query_ids"`
		Rows       []struct {
			QueryID       string              `json:"query_id"`
			Grade3        int                 `json:"grade3_spans"`
			Overlap       int                 `json:"overlap_upper_bound_grade3_spans"`
			Transcript    GrepReadTranscript  `json:"transcript"`
			TranscriptSHA string              `json:"transcript_sha256"`
			Sources       []grepReadDevSource `json:"verified_emitted_source"`
			Payloads      []PreservedPayload  `json:"preserved_payloads"`
		} `json:"queries"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if report.DatasetSHA != ds.SHA256 || len(report.Rows) != len(ds.Dataset.Queries) {
		t.Fatal("wrong or incomplete development population")
	}
	tok, err := evaltokenizer.Load("../tokenizer/testdata/artifact")
	if err != nil {
		t.Fatal(err)
	}
	counter, err := NewPinnedRealPayloadCounter(tok)
	if err != nil {
		t.Fatal(err)
	}
	var misses, excluded []string
	population := 0
	for i, row := range report.Rows {
		q := ds.Dataset.Queries[i]
		if q.Split != SplitDev || row.QueryID != q.ID || row.Transcript.Query != q.Text {
			t.Fatal("wrong, duplicate or non-development query")
		}
		if err := row.Transcript.Validate(); err != nil {
			t.Fatal(err)
		}
		if row.TranscriptSHA != row.Transcript.DigestSHA256() {
			t.Fatal("transcript digest drift", q.ID)
		}
		want, err := row.Transcript.Ledger.PreservedPayloads(counter)
		if err != nil || !reflect.DeepEqual(row.Payloads, want) {
			t.Fatalf("%s payload count/digest drift: %v", q.ID, err)
		}
		sources, err := grepReadDevSources(nil, row.Transcript)
		if err != nil || !reflect.DeepEqual(sources, row.Sources) {
			t.Fatalf("%s emitted-source provenance drift: %v", q.ID, err)
		}
		total, overlap := grepReadDevOverlap(sources, q.Judgements)
		if total != row.Grade3 || overlap != row.Overlap {
			t.Fatal("overlap diagnostic drift", q.ID)
		}
		if total > 0 {
			population++
			if overlap == 0 {
				misses = append(misses, q.ID)
			}
		} else {
			excluded = append(excluded, q.ID)
		}
	}
	if population != 40 || population != report.Population || !reflect.DeepEqual(misses, report.Misses) || !reflect.DeepEqual(excluded, report.Excluded) {
		t.Fatal("population or miss counts drifted")
	}
	if len(misses) > 0 && report.Status != "frozen_savings_claim_blocked_by_comparator_misses" {
		t.Fatal("comparator miss did not block the frozen savings claim")
	}
}
