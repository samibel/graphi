package staticfetch_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/cmd/graphi/staticfetch"
	evaltokenizer "github.com/samibel/graphi/internal/eval/tokenizer"
)

func TestStaticfetch_TokenizerDownloadIsHTTPSPinnedAndAtomic(t *testing.T) {
	body := []byte("real artifact stand-in")
	sum := sha256.Sum256(body)
	want := hex.EncodeToString(sum[:])
	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.String() != "https://example.invalid/cl100k_base.tiktoken" {
			t.Errorf("request URL = %s", r.URL)
		}
		return staticResponse(http.StatusOK, body, int64(len(body))), nil
	})}
	dest := filepath.Join(t.TempDir(), "tokenizers", "cl100k")
	if err := staticfetch.DownloadTokenizerForTest(client, "https://example.invalid/cl100k_base.tiktoken", dest, want); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, evaltokenizer.PinnedVocabularyFile))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("installed vocabulary = %q, want %q", got, body)
	}
	if notice, err := os.ReadFile(filepath.Join(dest, "NOTICE")); err != nil || !bytes.Contains(notice, []byte(evaltokenizer.TokenizerID)) {
		t.Fatalf("NOTICE missing tokenizer identity: %q err=%v", notice, err)
	}
}

func TestStaticfetch_TokenizerDownloadRejectsHashMismatchWithoutPartialInstall(t *testing.T) {
	body := []byte("corrupt")
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return staticResponse(http.StatusOK, body, int64(len(body))), nil
	})}
	dest := filepath.Join(t.TempDir(), "tokenizer")
	err := staticfetch.DownloadTokenizerForTest(client, "https://example.invalid/cl100k_base.tiktoken", dest, strings.Repeat("0", 64))
	if err == nil {
		t.Fatal("download accepted a hash mismatch")
	}
	actual := sha256.Sum256(body)
	for _, want := range []string{evaltokenizer.PinnedVocabularyFile, strings.Repeat("0", 64), hex.EncodeToString(actual[:])} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("diagnostic does not contain %q: %v", want, err)
		}
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("destination exists after refused download: %v", statErr)
	}
}

func TestStaticfetch_TokenizerDownloadRejectsNonHTTPS(t *testing.T) {
	err := staticfetch.DownloadTokenizerForTest(&http.Client{}, "http://example.invalid/cl100k_base.tiktoken", filepath.Join(t.TempDir(), "tokenizer"), strings.Repeat("0", 64))
	if err == nil || !strings.Contains(err.Error(), "refusing non-HTTPS URL") {
		t.Fatalf("non-HTTPS download error = %v", err)
	}
}
