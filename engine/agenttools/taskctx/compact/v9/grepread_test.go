package v9

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/samibel/graphi/engine/agenttools/contract"
)

type openCountingFS struct {
	fs.FS
	opens map[string]int
}

func (f *openCountingFS) Open(name string) (fs.File, error) {
	f.opens[name]++
	return f.FS.Open(name)
}

func TestCompactRuntimeDoesNotImportEvaluationTooling(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(body, []byte(`"github.com/samibel/graphi/internal/eval`)) {
			t.Errorf("%s imports evaluation tooling; compact runtime dependencies must remain in engine/core", entry.Name())
		}
	}
}

func TestGrepReadV2HonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := GrepReadV2(ctx, fstest.MapFS{"answer.go": {Data: []byte("package p\n")}}, "answer")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled discovery error = %v, want context.Canceled", err)
	}
}

func TestGrepReadV2CapsRepositoryFileScan(t *testing.T) {
	repository := make(fstest.MapFS, GrepReadV2MaxFiles+1)
	for i := 0; i <= GrepReadV2MaxFiles; i++ {
		repository[fmt.Sprintf("f%05d.go", i)] = &fstest.MapFile{Data: []byte("package p\n")}
	}
	transcript, err := GrepReadV2(context.Background(), repository, "needle")
	if err != nil {
		t.Fatal(err)
	}
	if transcript.StopReason != SavingsStopScanLimit {
		t.Fatalf("stop reason = %q, want %q", transcript.StopReason, SavingsStopScanLimit)
	}
	if len(transcript.IncludedFiles) > GrepReadV2MaxFiles {
		t.Fatalf("included %d files, cap is %d", len(transcript.IncludedFiles), GrepReadV2MaxFiles)
	}
}

func TestGrepReadV2SkipsOversizedFilesWithoutReadingTheirBytes(t *testing.T) {
	repository := fstest.MapFS{
		"huge.go": {Data: make([]byte, GrepReadV2MaxFileSize+1)},
	}
	transcript, err := GrepReadV2(context.Background(), repository, "needle")
	if err != nil {
		t.Fatal(err)
	}
	if len(transcript.Ledger.Responses) == 0 || !errors.Is(transcript.Validate(), nil) {
		t.Fatalf("bounded transcript is invalid: %v", transcript.Validate())
	}
	if got := string(transcript.Ledger.Responses[0].Bytes); got != "grep:error:huge.go:file_too_large\n" {
		t.Fatalf("grep response = %q", got)
	}
}

func TestGrepReadV2ChargesInvalidUTF8AgainstAggregateReadLimit(t *testing.T) {
	repository := fstest.MapFS{
		"a.go": {Data: []byte{0xff, 0xff, 0xff, 0xff}},
		"b.go": {Data: []byte{0xfe, 0xfe, 0xfe, 0xfe}},
	}
	files, limited, err := grepReadV2FilesWithLimits(context.Background(), repository, grepReadV2Limits{
		maxFiles: 10, maxFileSize: 4, maxBytes: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !limited {
		t.Fatal("invalid UTF-8 bytes bypassed the aggregate read limit")
	}
	if len(files) != 2 || files[0].ErrorKind != "scan_limit" || files[1].ErrorKind != "invalid_utf8" {
		t.Fatalf("bounded files = %#v", files)
	}
}

func TestReferenceHydrationReusesBoundedDiscoverySnapshot(t *testing.T) {
	query := "where is ParseFlags called before execute"
	evidence := []contract.Evidence{{RefID: "seed", Path: "seed.go", Span: "1-1", Snippet: "func ParseFlags()"}}
	items := []contract.Item{{RefID: "item", Reason: "primary: func ParseFlags (seed.go:1)", EvidenceRefIDs: []string{"seed"}}}
	scanned := []grepReadFile{{Path: "flow.go", Bytes: []byte("package p\nfunc execute() { ParseFlags() }\nfunc ParseFlags() {}\n")}}

	hydrated, _, err := compactTaskContextHydrateReferences(context.Background(), nil, scanned, query, evidence, items)
	if err != nil {
		t.Fatal(err)
	}
	if len(hydrated) == 0 {
		t.Fatal("reference hydration did not consume the bounded discovery snapshot")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := compactTaskContextHydrateReferences(ctx, nil, scanned, query, evidence, items); !errors.Is(err, context.Canceled) {
		t.Fatalf("reference hydration cancellation error = %v, want context.Canceled", err)
	}
}

func TestGrepReadFollowupReadsReuseSingleFileSnapshot(t *testing.T) {
	repository := &openCountingFS{
		FS:    fstest.MapFS{"answer.go": {Data: []byte("package p\nfunc ParseFlags() {}\nfunc execute() { ParseFlags() }\n")}},
		opens: make(map[string]int),
	}
	transcript, scanned, err := grepReadV2WithFiles(context.Background(), repository, "ParseFlags")
	if err != nil {
		t.Fatal(err)
	}
	if len(transcript.Reads) == 0 || len(scanned) != 1 {
		t.Fatalf("discovery did not exercise a follow-up read: reads=%d files=%d", len(transcript.Reads), len(scanned))
	}
	if repository.opens["answer.go"] != 1 {
		t.Fatalf("answer.go opened %d times, want one bounded snapshot read", repository.opens["answer.go"])
	}
}
