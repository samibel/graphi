package v9

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/samibel/graphi/engine/agenttools/contract"
)

type openCountingFS struct {
	fs.FS
	opens map[string]int
}

type cancelAfterContext struct {
	context.Context
	calls    int
	after    int
	done     chan struct{}
	canceled bool
}

func (c *cancelAfterContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *cancelAfterContext) Done() <-chan struct{}       { return c.done }
func (c *cancelAfterContext) Value(key any) any           { return c.Context.Value(key) }
func (c *cancelAfterContext) Err() error {
	if c.canceled {
		return context.Canceled
	}
	c.calls++
	if c.calls >= c.after {
		c.canceled = true
		close(c.done)
		return context.Canceled
	}
	return nil
}

type partialErrorFS struct {
	fs.FS
	target string
}

func (f partialErrorFS) Open(name string) (fs.File, error) {
	file, err := f.FS.Open(name)
	if err != nil || name != f.target {
		return file, err
	}
	return &partialErrorFile{File: file}, nil
}

type partialErrorFile struct {
	fs.File
	failed bool
}

type changingStatFS struct {
	fs.FS
	target string
}

func (f changingStatFS) Open(name string) (fs.File, error) {
	file, err := f.FS.Open(name)
	if err != nil || name != f.target {
		return file, err
	}
	return &changingStatFile{File: file}, nil
}

type changingStatFile struct {
	fs.File
	stats int
}

func (f *changingStatFile) Stat() (fs.FileInfo, error) {
	info, err := f.File.Stat()
	if err != nil {
		return nil, err
	}
	f.stats++
	if f.stats == 1 {
		return info, nil
	}
	return changedSizeInfo{FileInfo: info, size: info.Size() + 1}, nil
}

type changedSizeInfo struct {
	fs.FileInfo
	size int64
}

func (i changedSizeInfo) Size() int64 { return i.size }

func (f *partialErrorFile) Read(p []byte) (int, error) {
	if f.failed {
		return 0, io.ErrUnexpectedEOF
	}
	f.failed = true
	if len(p) > 2 {
		p = p[:2]
	}
	n, _ := f.File.Read(p)
	return n, io.ErrUnexpectedEOF
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

func TestGrepReadV2HonorsCancellationDuringSearch(t *testing.T) {
	var source strings.Builder
	source.WriteString("package p\n")
	for i := 0; i < 1024; i++ {
		source.WriteString("var needle = 1\n")
	}
	ctx := &cancelAfterContext{Context: context.Background(), after: 7, done: make(chan struct{})}
	_, err := GrepReadV2(ctx, fstest.MapFS{"answer.go": {Data: []byte(source.String())}}, "needle flow")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-search cancellation error = %v, want context.Canceled (checks=%d)", err, ctx.calls)
	}
}

func TestGrepReadV2SortHonorsCancellation(t *testing.T) {
	matches := make([]grepReadV2Match, 4096)
	for i := range matches {
		matches[i].Score = len(matches) - i
	}
	ctx := &cancelAfterContext{Context: context.Background(), after: 4, done: make(chan struct{})}
	if err := grepReadV2Sort(ctx, matches); !errors.Is(err, context.Canceled) {
		t.Fatalf("sort cancellation error = %v, want context.Canceled", err)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("canceled test context did not close Done")
	}
}

func TestEmptyDiscoverySnapshotCannotFallBackToRepositoryRead(t *testing.T) {
	repository := &openCountingFS{
		FS:    fstest.MapFS{"answer.go": {Data: []byte("package p\n")}},
		opens: make(map[string]int),
	}
	_, snapshot, err := grepReadV2WithFiles(context.Background(), repository, " \t ")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot == nil || len(snapshot.files) != 0 {
		t.Fatalf("empty query snapshot = %#v, want explicit empty snapshot", snapshot)
	}
	if _, err := readScannedSource(repository, snapshot, "answer.go"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("read outside empty snapshot error = %v, want fs.ErrNotExist", err)
	}
	if repository.opens["answer.go"] != 0 {
		t.Fatalf("empty snapshot reopened answer.go %d times", repository.opens["answer.go"])
	}
}

func TestDiscoverySnapshotIncludesMarkdownWithoutSearchingIt(t *testing.T) {
	repository := fstest.MapFS{
		"answer.go": {Data: []byte("package p\n")},
		"FLOW.md":   {Data: []byte("# needle flow\nimportant architecture\n")},
	}
	transcript, snapshot, err := grepReadV2WithFiles(context.Background(), repository, "needle flow")
	if err != nil {
		t.Fatal(err)
	}
	if len(transcript.IncludedFiles) != 1 || transcript.IncludedFiles[0] != "answer.go" {
		t.Fatalf("searchable files = %v, want Go files only", transcript.IncludedFiles)
	}
	if got := string(transcript.Ledger.Responses[0].Bytes); strings.Contains(got, "FLOW.md") {
		t.Fatalf("Markdown leaked into grep response: %q", got)
	}
	raw, err := readScannedSource(repository, snapshot, "FLOW.md")
	if err != nil || !strings.Contains(string(raw), "important architecture") {
		t.Fatalf("Markdown snapshot hydration = %q, %v", raw, err)
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

func TestGrepReadV2ChargesPartialBytesReturnedWithReadError(t *testing.T) {
	repository := partialErrorFS{
		FS: fstest.MapFS{
			"a.go": {Data: []byte("four")},
			"b.go": {Data: []byte("four")},
		},
		target: "a.go",
	}
	files, limited, err := grepReadV2FilesWithLimits(context.Background(), repository, grepReadV2Limits{
		maxFiles: 10, maxFileSize: 4, maxBytes: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !limited {
		t.Fatal("partial bytes returned with an error bypassed the aggregate read limit")
	}
	kinds := make(map[string]string)
	for _, file := range files {
		kinds[file.Path] = file.ErrorKind
	}
	if kinds["a.go"] != "read_failed" || kinds["."] != "scan_limit" {
		t.Fatalf("bounded files = %#v", files)
	}
}

func TestReadSourceFileLimitRejectsGrowthAfterInitialStat(t *testing.T) {
	repository := changingStatFS{
		FS:     fstest.MapFS{"answer.go": {Data: []byte("four")}},
		target: "answer.go",
	}
	raw, readBytes, err := readSourceFileLimit(repository, "answer.go", 4)
	if !errors.Is(err, errSourceFileChanged) {
		t.Fatalf("growing file error = %v, want errSourceFileChanged", err)
	}
	if string(raw) != "four" || readBytes != 4 {
		t.Fatalf("growing file accounting = %q/%d, want four/4", raw, readBytes)
	}
}

func TestReferenceHydrationReusesBoundedDiscoverySnapshot(t *testing.T) {
	query := "where is ParseFlags called before execute"
	evidence := []contract.Evidence{{RefID: "seed", Path: "seed.go", Span: "1-1", Snippet: "func ParseFlags()"}}
	items := []contract.Item{{RefID: "item", Reason: "primary: func ParseFlags (seed.go:1)", EvidenceRefIDs: []string{"seed"}}}
	snapshot := &grepReadSnapshot{files: []grepReadFile{{Path: "flow.go", Bytes: []byte("package p\nfunc execute() { ParseFlags() }\nfunc ParseFlags() {}\n")}}}

	hydrated, _, err := compactTaskContextHydrateReferences(context.Background(), nil, snapshot, query, evidence, items)
	if err != nil {
		t.Fatal(err)
	}
	if len(hydrated) == 0 {
		t.Fatal("reference hydration did not consume the bounded discovery snapshot")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := compactTaskContextHydrateReferences(ctx, nil, snapshot, query, evidence, items); !errors.Is(err, context.Canceled) {
		t.Fatalf("reference hydration cancellation error = %v, want context.Canceled", err)
	}
}

func TestGrepReadFollowupReadsReuseSingleFileSnapshot(t *testing.T) {
	repository := &openCountingFS{
		FS:    fstest.MapFS{"answer.go": {Data: []byte("package p\nfunc ParseFlags() {}\nfunc execute() { ParseFlags() }\n")}},
		opens: make(map[string]int),
	}
	transcript, snapshot, err := grepReadV2WithFiles(context.Background(), repository, "ParseFlags")
	if err != nil {
		t.Fatal(err)
	}
	if len(transcript.Reads) == 0 || len(snapshot.files) != 1 {
		t.Fatalf("discovery did not exercise a follow-up read: reads=%d files=%d", len(transcript.Reads), len(snapshot.files))
	}
	if repository.opens["answer.go"] != 1 {
		t.Fatalf("answer.go opened %d times, want one bounded snapshot read", repository.opens["answer.go"])
	}
}
