package release

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReleaseTargets_Exact pins the cross-compile matrix: the five platforms in
// fixed order (the source of truth that drives SHA256SUMS + BuildAll order).
func TestReleaseTargets_Exact(t *testing.T) {
	want := []Platform{
		{"linux", "amd64"},
		{"linux", "arm64"},
		{"darwin", "amd64"},
		{"darwin", "arm64"},
		{"windows", "amd64"},
	}
	if len(ReleaseTargets) != len(want) {
		t.Fatalf("ReleaseTargets len=%d, want %d: %v", len(ReleaseTargets), len(want), ReleaseTargets)
	}
	for i, p := range want {
		if ReleaseTargets[i] != p {
			t.Errorf("ReleaseTargets[%d]=%v, want %v", i, ReleaseTargets[i], p)
		}
	}
}

// TestAssetName_All checks the canonical asset name per target, with .exe only
// for windows.
func TestAssetName_All(t *testing.T) {
	cases := map[Platform]string{
		{"linux", "amd64"}:   "graphi-linux-amd64",
		{"linux", "arm64"}:   "graphi-linux-arm64",
		{"darwin", "amd64"}:  "graphi-darwin-amd64",
		{"darwin", "arm64"}:  "graphi-darwin-arm64",
		{"windows", "amd64"}: "graphi-windows-amd64.exe",
	}
	for p, want := range cases {
		if got := AssetName(p); got != want {
			t.Errorf("AssetName(%v)=%q, want %q", p, got, want)
		}
	}
}

// TestWriteSHA256SUMS_Format asserts canonical two-space `sha256sum -c` lines,
// order preserved, with digests matching recomputation.
func TestWriteSHA256SUMS_Format(t *testing.T) {
	dir := t.TempDir()
	names := []string{"b.bin", "a.bin", "c.bin"} // order intentionally non-sorted
	for i, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Repeat("x", i+1)), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := WriteSHA256SUMS(dir, names); err != nil {
		t.Fatalf("WriteSHA256SUMS: %v", err)
	}
	f, err := os.Open(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		t.Fatalf("open SHA256SUMS: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	var i int
	for sc.Scan() {
		if i >= len(names) {
			t.Fatalf("more lines than names")
		}
		line := sc.Text()
		wantSum, err := sha256file(filepath.Join(dir, names[i]))
		if err != nil {
			t.Fatalf("sha256file: %v", err)
		}
		wantLine := wantSum + "  " + names[i] // exactly two spaces
		if line != wantLine {
			t.Errorf("line %d = %q, want %q", i, line, wantLine)
		}
		i++
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if i != len(names) {
		t.Errorf("got %d lines, want %d (order/count)", i, len(names))
	}
}

// TestBuildAll_WritesAllTargetsAndSums proves BuildAll cross-compiles every
// target into dist (non-empty) and WriteSHA256SUMS digests match recomputation.
func TestBuildAll_WritesAllTargetsAndSums(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full cross-compile matrix build in -short mode")
	}
	ctx := context.Background()
	dir := t.TempDir()

	paths, err := BuildAll(ctx, BuildConfig{Version: "0.0.0-test"}, dir)
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}
	if len(paths) != len(ReleaseTargets) {
		t.Fatalf("BuildAll returned %d paths, want %d", len(paths), len(ReleaseTargets))
	}
	names := make([]string, len(paths))
	for i, p := range paths {
		want := filepath.Join(dir, AssetName(ReleaseTargets[i]))
		if p != want {
			t.Errorf("paths[%d]=%q, want %q (order)", i, p, want)
		}
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if fi.Size() == 0 {
			t.Errorf("asset %s is empty", p)
		}
		names[i] = filepath.Base(p)
	}
	if err := WriteSHA256SUMS(dir, names); err != nil {
		t.Fatalf("WriteSHA256SUMS: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		t.Fatalf("read SHA256SUMS: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != len(names) {
		t.Fatalf("SHA256SUMS has %d lines, want %d", len(lines), len(names))
	}
	for i, name := range names {
		wantSum, err := sha256file(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("sha256file %s: %v", name, err)
		}
		if lines[i] != wantSum+"  "+name {
			t.Errorf("line %d = %q, want %q", i, lines[i], wantSum+"  "+name)
		}
	}
}
