package model2vec

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// The pinned artifact (PINNED.md is the human copy of these constants).
const (
	pinnedModel    = "minishlab/potion-code-16M-v2"
	pinnedRevision = "e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b"

	// envModelDir overrides the artifact location; the default is the
	// $HOME/.cache path PINNED.md's fetch recipe writes to.
	envModelDir = "GRAPHI_SPIKE_MODEL_DIR"

	skipMessage = "SKIP: model artifact absent; see PINNED.md"
)

var pinnedSHA256 = map[string]string{
	FileConfig:      "148e5691a6fcc553437156859701fba017a1ba5d340b170f17e0f3668fb861a7",
	FileTokenizer:   "107bbdcbad4bff1d299b7a4c3a2fb17c52890688b7dd0e4c9deab79d3c4f3d45",
	FileSafetensors: "75cf7a6c2171b230ad19b1e7d8e0b1aee86da5a02af8e7cacedd9921d227623c",
	FileModules:     "a68dcbed0429dcdd5bfdca92b0b03cc30d09122c0a3fcf4758787d4b244e45b2",
}

// artifactDir resolves the model directory: $GRAPHI_SPIKE_MODEL_DIR first, then
// $HOME/.cache/graphi/models/<model>@<revision>.
func artifactDir() string {
	if d := os.Getenv(envModelDir); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cache", "graphi", "models", "potion-code-16M-v2@"+pinnedRevision)
}

// artifactPresent reports whether every pinned file exists in dir.
func artifactPresent(dir string) bool {
	if dir == "" {
		return false
	}
	for name := range pinnedSHA256 {
		if st, err := os.Stat(filepath.Join(dir, name)); err != nil || !st.Mode().IsRegular() {
			return false
		}
	}
	return true
}

var (
	pinnedOnce   sync.Once
	pinnedModelV *Model
	pinnedErr    error
)

// loadPinnedModel skips the calling test when the artifact is absent, fails it
// when a present file does not match its SHA-256 pin (the wrong artifact would
// make every fidelity number meaningless), and otherwise returns the one
// shared loaded model.
func loadPinnedModel(t testing.TB) *Model {
	t.Helper()
	dir := artifactDir()
	if !artifactPresent(dir) {
		t.Skip(skipMessage)
	}
	pinnedOnce.Do(func() {
		for name, want := range pinnedSHA256 {
			got, err := fileSHA256(filepath.Join(dir, name))
			if err != nil {
				pinnedErr = err
				return
			}
			if got != want {
				pinnedErr = &pinMismatch{file: name, want: want, got: got}
				return
			}
		}
		pinnedModelV, pinnedErr = LoadModel(dir)
	})
	if pinnedErr != nil {
		t.Fatalf("pinned artifact at %s: %v", dir, pinnedErr)
	}
	return pinnedModelV
}

type pinMismatch struct{ file, want, got string }

func (p *pinMismatch) Error() string {
	return p.file + ": sha256 " + p.got + " does not match the pin " + p.want + " (see PINNED.md)"
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
