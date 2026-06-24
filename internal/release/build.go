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
	"sort"
	"strings"
)

// VersionVar is the ldflags target for the release version string.
const VersionVar = "github.com/samibel/graphi/internal/version.Version"

// DefaultGrammarSubsetTags is the build-tag set the shipped default graphi
// binary is built with (EP-009, SW-053 AC#3). It is the single source of truth
// for the subset-tag default build and must stay in lock-step with the pure-Go
// tree-sitter grammars registered in core/parse.RegisterDefaults.
//
// The gotreesitter `grammars` package embeds ALL ~206 grammar blobs by default
// (a +~24.5 MiB bloat), gated `!grammar_subset`. The umbrella `grammar_subset`
// tag switches that all-grammars embed off; each `grammar_subset_<lang>` tag
// then opts exactly one blob back in via the upstream
// z_subset_blob_embed_<lang>.go embed. So the shipped default binary embeds ONLY
// the registered languages' blobs (TypeScript ≈ 119 KiB), never all 206.
//
// Adding a tier-1 language (SW-054) is two coordinated one-liners: the
// r.Register(NewXxxParser()) in RegisterDefaults and its "grammar_subset_xxx"
// entry here. Go's stdlib parsers (go/ast, JSON) carry no gotreesitter blob and
// therefore contribute no tag.
var DefaultGrammarSubsetTags = []string{
	"grammar_subset",            // umbrella: switch OFF the all-206 default embed
	"grammar_subset_typescript", // TypeScript (SW-053) — embeds only typescript.bin
	"grammar_subset_javascript", // JavaScript (SW-054)
	"grammar_subset_tsx",        // TSX (SW-054)
	"grammar_subset_python",     // Python (SW-054)
	"grammar_subset_java",       // Java (SW-054)
	"grammar_subset_c",          // C (SW-054)
	"grammar_subset_ruby",       // Ruby (SW-054)
	"grammar_subset_rust",       // Rust (SW-054)
	"grammar_subset_php",        // PHP (SW-054)
	"grammar_subset_c_sharp",    // C# (SW-054)
	"grammar_subset_kotlin",     // Kotlin (SW-054)
	"grammar_subset_cpp",        // C++ (SW-054)
	"grammar_subset_bash",       // Bash (SW-054)
	"grammar_subset_sql",        // SQL (SW-054)
	"grammar_subset_lua",        // Lua (SW-054)
	// HTML deferred to SW-056 (graphi-broad): see RegisterDefaults — its scanner core
	// is co-located with grammar_subset_blade upstream, so grammar_subset_html cannot
	// be built in isolation without embedding an unregistered blade.bin blob.
	"grammar_subset_css",      // CSS (SW-054)
	"grammar_subset_yaml",     // YAML (SW-054)
	"grammar_subset_toml",     // TOML (SW-054)
	"grammar_subset_markdown", // Markdown (SW-054)
	"grammar_subset_hcl",      // HCL / Terraform (SW-054)
}

// ExpectedGrammarBlobs derives, from DefaultGrammarSubsetTags (the single source
// of truth), the sorted set of gotreesitter grammar blob paths the shipped
// default binary must embed — one `grammar_blobs/<lang>.bin` per
// `grammar_subset_<lang>` tag. The umbrella `grammar_subset` tag carries no
// language and contributes no blob. This lets CI assert the embedded blob set
// against the source of truth instead of a hand-maintained list, so adding a
// tier-1 language (a new tag here) never silently drifts the release gate.
func ExpectedGrammarBlobs() []string {
	const prefix = "grammar_subset_"
	out := make([]string, 0, len(DefaultGrammarSubsetTags))
	for _, tag := range DefaultGrammarSubsetTags {
		lang := strings.TrimPrefix(tag, prefix)
		if lang == tag || lang == "" {
			continue // umbrella "grammar_subset" (no language) — no blob
		}
		out = append(out, "grammar_blobs/"+lang+".bin")
	}
	sort.Strings(out)
	return out
}

// Platform names one cross-compile target (GOOS/GOARCH pair).
type Platform struct {
	OS   string
	Arch string
}

// ReleaseTargets is the single source of truth for the cross-compile release
// matrix (EP-010 SW-064). It fixes both the set AND the order of shipped
// platform binaries; SHA256SUMS lines and BuildAll outputs follow this order
// for determinism. Mirror the DefaultGrammarSubsetTags source-of-truth style:
// adding a platform is a one-line edit here.
var ReleaseTargets = []Platform{
	{"linux", "amd64"},
	{"linux", "arm64"},
	{"darwin", "amd64"},
	{"darwin", "arm64"},
	{"windows", "amd64"},
}

// AssetName is the canonical release asset file name for a target:
// "graphi-<os>-<arch>", with a ".exe" suffix on windows.
func AssetName(p Platform) string {
	name := "graphi-" + p.OS + "-" + p.Arch
	if p.OS == "windows" {
		name += ".exe"
	}
	return name
}

// BuildConfig parameterizes a release build.
type BuildConfig struct {
	Target  string // default ./cmd/graphi/
	Version string // stamped via ldflags; default "dev"
	// Tags is the build-tag set linked into the binary. When nil it defaults to
	// DefaultGrammarSubsetTags so the shipped default build NEVER embeds all 206
	// gotreesitter grammars (SW-053 AC#3). Pass an explicit (possibly empty)
	// slice only to override the default tier's grammar selection.
	Tags []string
	// OS and Arch select the cross-compile target (GOOS/GOARCH). Both empty ⇒
	// the host platform, preserving the original (host-only) Build behavior.
	OS   string
	Arch string
}

func (c *BuildConfig) defaults() {
	if c.Target == "" {
		c.Target = "./cmd/graphi/"
	}
	if c.Version == "" {
		c.Version = "dev"
	}
	if c.Tags == nil {
		c.Tags = DefaultGrammarSubsetTags
	}
}

// Build produces the static binary at out under CGO_ENABLED=0 with trimpath,
// VCS stamping, and the version ldflag. It runs from the module root.
func Build(ctx context.Context, cfg BuildConfig, out string) error {
	cfg.defaults()
	ldflags := fmt.Sprintf("-X %s=%s", VersionVar, cfg.Version)
	args := []string{"build", "-trimpath", "-buildvcs=true"}
	if len(cfg.Tags) > 0 {
		// Space-joined tag list (the modern `-tags 'a b c'` form). This is the
		// load-bearing SW-053 AC#3 wiring: the shipped default build is
		// subset-tagged so only the registered grammar blobs are embedded.
		args = append(args, "-tags", strings.Join(cfg.Tags, " "))
	}
	args = append(args, "-ldflags", ldflags, "-o", out, cfg.Target)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Env = withCgo(os.Environ(), "0")
	// Cross-compile when a target is set; empty ⇒ host platform (unchanged).
	if cfg.OS != "" {
		cmd.Env = append(cmd.Env, "GOOS="+cfg.OS)
	}
	if cfg.Arch != "" {
		cmd.Env = append(cmd.Env, "GOARCH="+cfg.Arch)
	}
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
	Version     string
	VCSRevision string
	VCSTime     string
	CGOEnabled  string
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
