package rootfile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReadFromRejectsFinalSymlinkReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on windows")
	}
	rootDir := t.TempDir()
	file := filepath.Join(rootDir, "owned.go")
	backup := filepath.Join(rootDir, "backup.go")
	if err := os.WriteFile(file, []byte("package owned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	var hookErr error
	_, err = readFromWithHooks(root, "owned.go", 1024, &readHooks{
		afterLstat: func() {
			if hookErr = os.Rename(file, backup); hookErr == nil {
				// The link resolves to the exact inode accepted by the first Lstat.
				hookErr = os.Symlink("backup.go", file)
			}
		},
	})
	if hookErr != nil {
		t.Fatalf("replace regular file with symlink: %v", hookErr)
	}
	if !errors.Is(err, ErrNotRegular) {
		t.Fatalf("final symlink replacement error = %v, want ErrNotRegular", err)
	}
}

func TestReadFromRejectsIntermediateSymlinkOutsideRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on windows")
	}
	rootDir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("must-not-enter"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(rootDir, "linked")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	data, err := ReadFrom(root, "linked/secret", 1024)
	if err == nil || len(data) != 0 {
		t.Fatalf("outside-root intermediate symlink was read: data=%q err=%v", data, err)
	}
}

func TestReadFromBoundsGrowthAfterStat(t *testing.T) {
	rootDir := t.TempDir()
	file := filepath.Join(rootDir, "grow.go")
	if err := os.WriteFile(file, []byte("tiny"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	var hookErr error
	data, err := readFromWithHooks(root, "grow.go", 8, &readHooks{
		afterOpenStat: func() {
			hookErr = os.WriteFile(file, []byte(strings.Repeat("x", 32)), 0o600)
		},
	})
	if hookErr != nil {
		t.Fatalf("grow file after descriptor stat: %v", hookErr)
	}
	var tooLarge *TooLargeError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("growth error = %v, want TooLargeError", err)
	}
	if tooLarge.Size < 9 {
		t.Fatalf("observed size = %d, want at least limit+1", tooLarge.Size)
	}
	if len(data) != 0 {
		t.Fatalf("oversize data retained: %d bytes", len(data))
	}
}

func TestReadMissingRetainsNotExist(t *testing.T) {
	_, err := Read(t.TempDir(), "missing", 1024)
	if !os.IsNotExist(err) {
		t.Fatalf("missing error = %v, want os.IsNotExist", err)
	}
}
