// Package release produces the canonical static graphi binary reproducibly
// (story SW-013). It builds ./cmd/graphi with CGO_ENABLED=0, -trimpath, VCS
// stamping, and an ldflags-stamped version; commit SHA and commit (build) date
// are embedded by Go's VCS stamping (debug.ReadBuildInfo). A reproducibility
// check builds the same source twice and asserts the binaries are byte-for-byte
// identical (sha256 equal).
package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// VersionVar is the ldflags target for the release version string.
const VersionVar = "github.com/samibel/graphi/internal/version.Version"

// BuildConfig parameterizes a release build.
type BuildConfig struct {
	Target  string // default ./cmd/graphi/
	Version string // stamped via ldflags; default "dev"
}

func (c *BuildConfig) defaults() {
	if c.Target == "" {
		c.Target = "./cmd/graphi/"
	}
	if c.Version == "" {
		c.Version = "dev"
	}
}

// Build produces the static binary at out under CGO_ENABLED=0 with trimpath,
// VCS stamping, and the version ldflag. It runs from the module root.
func Build(ctx context.Context, cfg BuildConfig, out string) error {
	cfg.defaults()
	ldflags := fmt.Sprintf("-X %s=%s", VersionVar, cfg.Version)
	cmd := exec.CommandContext(ctx, "go", "build",
		"-trimpath", "-buildvcs=true",
		"-ldflags", ldflags,
		"-o", out, cfg.Target)
	cmd.Env = withCgo(os.Environ(), "0")
	cmd.Dir = moduleRootPath()
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// VerifyReproducible builds the same source twice and returns the shared sha256
// and whether the two builds are byte-for-byte identical.
func VerifyReproducible(ctx context.Context, cfg BuildConfig) (sha string, ok bool, err error) {
	cfg.defaults()
	tmp, err := os.MkdirTemp("", "graphi-release-repro-*")
	if err != nil {
		return "", false, err
	}
	defer os.RemoveAll(tmp)
	a := filepath.Join(tmp, "graphi-a")
	b := filepath.Join(tmp, "graphi-b")
	if err := Build(ctx, cfg, a); err != nil {
		return "", false, fmt.Errorf("build A: %w", err)
	}
	if err := Build(ctx, cfg, b); err != nil {
		return "", false, fmt.Errorf("build B: %w", err)
	}
	sa, err := sha256file(a)
	if err != nil {
		return "", false, err
	}
	sb, err := sha256file(b)
	if err != nil {
		return "", false, err
	}
	return sa, sa == sb, nil
}

// BuildInfo holds the VCS + version metadata read from a built binary.
type BuildInfo struct {
	Version    string
	VCSRevision string
	VCSTime    string
	CGOEnabled string
}

// ReadBuildInfo runs the binary's `version` subcommand and parses the embedded
// version + VCS metadata (commit/date come from Go VCS stamping via
// debug.ReadBuildInfo; version from the ldflags-stamped version.Version).
func ReadBuildInfo(ctx context.Context, bin string) (BuildInfo, error) {
	out, err := exec.CommandContext(ctx, bin, "version").Output()
	if err != nil {
		return BuildInfo{}, fmt.Errorf("run %s version: %w", bin, err)
	}
	bi := parseVersionOutput(string(out))
	// CGO setting comes from `go version -m` (the binary does not self-report it).
	if vm, verr := exec.CommandContext(ctx, "go", "version", "-m", bin).Output(); verr == nil {
		if m := reCGO.FindStringSubmatch(string(vm)); len(m) > 1 {
			bi.CGOEnabled = m[1]
		}
	}
	return bi, nil
}

var reVersionLine = regexp.MustCompile(`version=(\S+)\s+commit=(\S*)\s+date=(\S*)`)
var reCGO = regexp.MustCompile(`\sbuild\s+CGO_ENABLED=(\S+)`)

func parseVersionOutput(s string) BuildInfo {
	bi := BuildInfo{}
	if m := reVersionLine.FindStringSubmatch(s); len(m) > 3 {
		bi.Version = m[1]
		bi.VCSRevision = m[2]
		bi.VCSTime = m[3]
	}
	return bi
}

func sha256file(path string) (string, error) {
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

func withCgo(env []string, cgo string) []string {
	prefix := "CGO_ENABLED="
	out := make([]string, 0, len(env)+1)
	found := false
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			out = append(out, prefix+cgo)
			found = true
			continue
		}
		out = append(out, e)
	}
	if !found {
		out = append(out, prefix+cgo)
	}
	return out
}

// moduleRootPath resolves the module root once via `go env GOMOD`.
func moduleRootPath() string {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "."
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == "/dev/null" {
		return "."
	}
	return filepath.Dir(gomod)
}
