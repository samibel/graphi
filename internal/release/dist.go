package release

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BuildAll cross-compiles every ReleaseTargets platform into distDir, reusing
// the supplied cfg (its OS/Arch are overridden per target). It returns the
// written file paths in ReleaseTargets order. distDir is created if absent.
func BuildAll(ctx context.Context, cfg BuildConfig, distDir string) ([]string, error) {
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir dist dir %s: %w", distDir, err)
	}
	paths := make([]string, 0, len(ReleaseTargets))
	for _, p := range ReleaseTargets {
		tc := cfg
		tc.OS = p.OS
		tc.Arch = p.Arch
		out := filepath.Join(distDir, AssetName(p))
		if err := Build(ctx, tc, out); err != nil {
			return nil, fmt.Errorf("build %s/%s: %w", p.OS, p.Arch, err)
		}
		paths = append(paths, out)
	}
	return paths, nil
}

// WriteSHA256SUMS computes the sha256 of each named file under distDir (in the
// given order) and writes distDir/SHA256SUMS with canonical `sha256sum -c`
// lines: "<hex><two spaces><name>\n". names are relative file names.
func WriteSHA256SUMS(distDir string, names []string) error {
	var b strings.Builder
	for _, name := range names {
		sum, err := sha256file(filepath.Join(distDir, name))
		if err != nil {
			return fmt.Errorf("sha256 %s: %w", name, err)
		}
		fmt.Fprintf(&b, "%s  %s\n", sum, name)
	}
	out := filepath.Join(distDir, "SHA256SUMS")
	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", out, err)
	}
	return nil
}
