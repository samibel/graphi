package v9

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

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
