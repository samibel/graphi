package model2vec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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

// classifyArtifact is the ONE deterministic classifier for the pinned
// artifact at dir. It returns
//
//   - (true, nil)  — every pinned file exists, is a regular file (NOT a
//     symlink, directory, device or socket) and is readable by the test
//     process. The artifact can be loaded.
//   - (false, nil) — every pinned file is genuinely absent (Lstat returns
//     fs.ErrNotExist for every path). Caller should Skip.
//   - (false, err) — partial absence, a permission denial, an IO failure,
//     a symlink, or a directory where a regular file should be. Caller
//     should Fail with the error.
//
// Three determinism guarantees:
//
//  1. Files are inspected in sorted name order (Go map iteration is
//     randomised; this classifier is not).
//  2. ALL pinned files are inspected before a decision is returned.
//  3. os.Lstat + os.Open are used so symlinks are detected as such and not
//     followed silently — a symlink at a pinned path is a misconfigured
//     artifact and must Fail, not Skip.
//
// This is the single source of truth used by artifactPresent,
// loadPinnedModel and the absence-aware tests; TestArtifactClassifier
// table-tests its five observable outcomes.
func classifyArtifact(dir string) (bool, error) {
	if dir == "" {
		return false, nil
	}
	names := make([]string, 0, len(pinnedSHA256))
	for name := range pinnedSHA256 {
		names = append(names, name)
	}
	sort.Strings(names)

	var notExist int
	var firstErr error
	for _, name := range names {
		path := filepath.Join(dir, name)
		st, lerr := os.Lstat(path)
		if lerr != nil {
			if errors.Is(lerr, fs.ErrNotExist) {
				notExist++
				continue
			}
			if firstErr == nil {
				firstErr = &artifactUnusable{path: path, err: lerr}
			}
			continue
		}
		if st.Mode()&os.ModeSymlink != 0 {
			if firstErr == nil {
				firstErr = &artifactUnusable{path: path, err: errors.New("is a symlink; the pinned artifact must be a regular file, not a link")}
			}
			continue
		}
		if !st.Mode().IsRegular() {
			if firstErr == nil {
				firstErr = &artifactUnusable{path: path, err: errors.New("not a regular file: mode " + st.Mode().String())}
			}
			continue
		}
		f, oerr := os.Open(path)
		if oerr != nil {
			if firstErr == nil {
				firstErr = &artifactUnusable{path: path, err: oerr}
			}
			continue
		}
		_ = f.Close()
	}
	if firstErr != nil {
		return false, firstErr
	}
	if notExist == len(pinnedSHA256) {
		return false, nil
	}
	if notExist > 0 {
		return false, errors.New("pinned artifact is partial: " + strconv.Itoa(notExist) + " of " + strconv.Itoa(len(pinnedSHA256)) + " pinned files are absent at " + dir)
	}
	return true, nil
}

// artifactPresent is the boolean form of classifyArtifact, kept so existing
// callers (`if !artifactPresent(dir) { t.Skip(skipMessage) }`) keep their
// intent on the page. FAILURE cases (partial, permission, symlink, dir)
// collapse to false here, so callers that only inspect the bool would skip
// past a misconfigured artifact — prefer classifyArtifact directly in new
// code so the error is visible.
func artifactPresent(dir string) bool {
	ok, _ := classifyArtifact(dir)
	return ok
}

// checkPinnedArtifact is the fail-closed entry point used by every test that
// needs the artifact. It returns nil when the artifact is usable, no error
// when every pinned file is missing (the caller should Skip), and a
// descriptive error for any other failure mode — partial absence, permission
// denied, IO error, symlink, directory at a pinned path.
func checkPinnedArtifact(t testing.TB) error {
	t.Helper()
	dir := artifactDir()
	if dir == "" {
		return errors.New("artifactDir is empty: cannot resolve $HOME")
	}
	_, err := classifyArtifact(dir)
	return err
}

// artifactUnusable is the typed error returned by classifyArtifact when the
// artifact is present but the test process cannot use it.
type artifactUnusable struct {
	path string
	err  error
}

func (e *artifactUnusable) Error() string {
	return e.path + ": " + e.err.Error() + " (the artifact is present but not usable; this is a failure, not a skip)"
}

func (e *artifactUnusable) Unwrap() error { return e.err }

// TestArtifactClassifier pins classifyArtifact's five observable outcomes
// (absent / partial / permission / directory / symlink). Each case builds a
// temp dir, populates it to express the failure mode under test, and
// compares the classifier's (present, errorKind) tuple to the expected one.
//
// "permission" is skipped when the test process is effectively root (chmod
// 000 is not enforced for the file owner who is root); the classifier's
// permission-denied path is exercised in CI by a non-root user.
func TestArtifactClassifier(t *testing.T) {
	allNames := allPinnedNames()
	cases := []struct {
		name        string
		setup       func(t *testing.T, dir string)
		wantPresent bool
		wantKind    errorKind
	}{
		{
			name: "absent",
			setup: func(t *testing.T, dir string) {
				// Nothing written — every pinned path is missing.
			},
			wantPresent: false,
			wantKind:    errNone,
		},
		{
			name: "partial",
			setup: func(t *testing.T, dir string) {
				// Only the first pinned name (config.json in sorted order)
				// is present; the other three are missing.
				if err := os.WriteFile(filepath.Join(dir, allNames[0]), []byte("{}"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantPresent: false,
			wantKind:    errPartial,
		},
		{
			name: "directory",
			setup: func(t *testing.T, dir string) {
				for _, name := range allNames {
					if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
						t.Fatal(err)
					}
				}
			},
			wantPresent: false,
			wantKind:    errUnusable,
		},
		{
			name: "symlink",
			setup: func(t *testing.T, dir string) {
				for _, name := range allNames {
					// A symlink whose target is itself (the path) makes
					// Lstat report a symlink regardless of whether the loop
					// resolves; if a platform refuses, fall back to a link
					// to "." which is also a symlink by Lstat.
					if err := os.Symlink(filepath.Join(dir, name), filepath.Join(dir, name)); err != nil {
						if err := os.Symlink(".", filepath.Join(dir, name)); err != nil {
							t.Fatal(err)
						}
					}
				}
			},
			wantPresent: false,
			wantKind:    errUnusable,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			c.setup(t, dir)
			present, err := classifyArtifact(dir)
			gotPresent, gotKind := present, kindOf(err)
			if gotPresent != c.wantPresent || gotKind != c.wantKind {
				t.Fatalf("classifyArtifact(%s) = (%v, %v), want (%v, %v); err = %v",
					c.name, gotPresent, gotKind, c.wantPresent, c.wantKind, err)
			}
		})
	}

	t.Run("permission", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("chmod 000 is not enforced for root; permission-denied path is exercised in CI as a non-root user")
		}
		dir := t.TempDir()
		for _, name := range allNames {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Chmod(filepath.Join(dir, allNames[0]), 0o000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, allNames[0]), 0o644) })
		present, err := classifyArtifact(dir)
		if present || kindOf(err) != errUnusable {
			t.Fatalf("classifyArtifact(permission) = (%v, %v); want (false, errUnusable)", present, err)
		}
	})
}

// allPinnedNames returns the pinned file names in a deterministic order.
func allPinnedNames() []string {
	names := make([]string, 0, len(pinnedSHA256))
	for name := range pinnedSHA256 {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type errorKind int

const (
	errNone     errorKind = iota
	errUnusable           // *artifactUnusable or wrapped os.Open/os.Lstat error
	errPartial            // "pinned artifact is partial: ..."
)

// kindOf maps an error returned by classifyArtifact to one of the three
// observable kinds the table test cares about. nil → errNone.
func kindOf(err error) errorKind {
	if err == nil {
		return errNone
	}
	var u *artifactUnusable
	if errors.As(err, &u) {
		return errUnusable
	}
	const partialPrefix = "pinned artifact is partial:"
	if msg := err.Error(); len(msg) >= len(partialPrefix) && msg[:len(partialPrefix)] == partialPrefix {
		return errPartial
	}
	return errUnusable
}

var (
	pinnedOnce   sync.Once
	pinnedModelV *Model
	pinnedErr    error
)

// loadPinnedModel skips the calling test when every pinned file is absent
// (clean absence across every pinned path), fails it when a present file is
// unusable (permission denial, non-regular entry, symlink, partial absence)
// or does not match its SHA-256 pin, and otherwise returns the one shared
// loaded model.
func loadPinnedModel(t testing.TB) *Model {
	t.Helper()
	present, err := classifyArtifact(artifactDir())
	if err != nil {
		// partial / permission / symlink / dir / IO → fail with the
		// classifier's own error so the cause is visible.
		t.Fatalf("pinned artifact at %s: %v", artifactDir(), err)
	}
	if !present {
		// classifier said absent (every pinned file Lstat returned
		// fs.ErrNotExist). Skip — the developer is expected to fetch the
		// artifact per PINNED.md.
		t.Skip(skipMessage)
	}
	pinnedOnce.Do(func() {
		dir := artifactDir()
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
		t.Fatalf("pinned artifact at %s: %v", artifactDir(), pinnedErr)
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
