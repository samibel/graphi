package release

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/buildattest"
	"github.com/samibel/graphi/internal/canary"
)

func TestCanonicalBuildArgsPinsReleaseContract(t *testing.T) {
	if CanonicalBuildContract != "internal/release.CanonicalBuildArgs/v2" {
		t.Fatalf("unexpected canonical build contract %q", CanonicalBuildContract)
	}
	args := CanonicalBuildArgs(BuildConfig{
		Target:             "./cmd/graphi/",
		Version:            "bench",
		Tags:               []string{"grammar_subset", "grammar_subset_typescript"},
		privacyAttestation: "encoded-proof",
	}, "out/graphi")

	want := []string{
		"build",
		"-trimpath",
		"-buildvcs=true",
		"-tags",
		"grammar_subset grammar_subset_typescript",
		"-ldflags",
		"-X " + VersionVar + "=bench -X " + PrivacyAttestationVar + "=encoded-proof",
		"-o",
		"out/graphi",
		"./cmd/graphi/",
	}
	if !slices.Equal(args, want) {
		t.Fatalf("canonical build args = %q, want exact contract %q", args, want)
	}
}

func TestPrivacyAttestationCannotLaunderFailOrMissingEvidence(t *testing.T) {
	revision := strings.Repeat("a", 40)
	for _, tc := range []struct {
		name string
		res  canary.GateResult
	}{
		{name: "measured violation", res: canary.GateResult{Verdict: "fail", EvidenceDigest: strings.Repeat("b", 64)}},
		{name: "missing evidence", res: canary.GateResult{Verdict: "pass"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := privacyAttestationFromResult(tc.res, revision, false); err == nil {
				t.Fatal("canonical builder produced an attestation without a complete measured PASS")
			}
		})
	}
}

func TestPrivacyAttestationCarriesExactBuildGraph(t *testing.T) {
	res := canary.GateResult{
		Verdict:        "pass",
		EvidenceDigest: strings.Repeat("b", 64),
		BuildTags:      []string{"grammar_subset", "webui_embed"},
		GOOS:           "linux",
		GOARCH:         "arm64",
	}
	encoded, err := privacyAttestationFromResult(res, strings.Repeat("a", 40), false)
	if err != nil {
		t.Fatal(err)
	}
	att, err := buildattest.Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if att.GOOS != "linux" || att.GOARCH != "arm64" || !slices.Equal(att.BuildTags, res.BuildTags) {
		t.Fatalf("attestation lost the exact build graph: %+v", att)
	}
}

// TestDefaultGrammarSubsetTags_WiresSubsetEmbed asserts the shipped default
// build is subset-tagged (SW-053 AC#3): the umbrella `grammar_subset` tag
// (which switches OFF the all-206 default embed) plus exactly the registered
// languages' `grammar_subset_<lang>` opt-ins. This is a pure unit check — it
// does not compile — guarding the wiring contract against silent drift.
func TestDefaultGrammarSubsetTags_WiresSubsetEmbed(t *testing.T) {
	if !slices.Contains(DefaultGrammarSubsetTags, "grammar_subset") {
		t.Fatalf("DefaultGrammarSubsetTags %v missing the umbrella `grammar_subset` tag; "+
			"without it the all-206 default embed (+~24.5 MiB) is NOT switched off (AC#3 violation)",
			DefaultGrammarSubsetTags)
	}
	if !slices.Contains(DefaultGrammarSubsetTags, "grammar_subset_typescript") {
		t.Errorf("DefaultGrammarSubsetTags %v missing `grammar_subset_typescript` (the SW-053 registered blob)",
			DefaultGrammarSubsetTags)
	}
	// Every non-umbrella tag must be a grammar_subset_<lang> opt-in. A bare or
	// foreign tag here would either be a no-op or re-link unintended blobs.
	for _, tag := range DefaultGrammarSubsetTags {
		if tag == "grammar_subset" {
			continue
		}
		if len(tag) <= len("grammar_subset_") || tag[:len("grammar_subset_")] != "grammar_subset_" {
			t.Errorf("unexpected tag %q in DefaultGrammarSubsetTags: must be `grammar_subset_<lang>`", tag)
		}
	}
}

// TestBuild_DefaultIsSubsetTagged_NotAll206 proves AC#3 end to end: the default
// release Build (subset-tagged) embeds ONLY the registered grammar blobs, so it
// is materially smaller than the prohibited all-206 default embed (no tags).
// Embedding all 206 blobs costs ~24.5 MiB on top of the per-blob cost; the
// subset build pays ~3.1 MiB (one-time runtime + the 119 KiB TS blob). A
// threshold well between the two deltas catches a regression to the all-206
// embed without coupling to an exact byte count.
func TestBuild_DefaultIsSubsetTagged_NotAll206(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping double release build in -short mode")
	}
	ctx := context.Background()
	dir := t.TempDir()

	subsetBin := filepath.Join(dir, "graphi-subset")
	if err := Build(ctx, BuildConfig{Version: "0.0.0-test"}, subsetBin); err != nil {
		t.Fatalf("default (subset-tagged) Build: %v", err)
	}
	// Empty (non-nil) Tags overrides the default → the prohibited all-206 embed.
	all206Bin := filepath.Join(dir, "graphi-all206")
	if err := Build(ctx, BuildConfig{Version: "0.0.0-test", Tags: []string{}}, all206Bin); err != nil {
		t.Fatalf("all-206 (no-tags) Build: %v", err)
	}

	subsetSize := mustSize(t, subsetBin)
	all206Size := mustSize(t, all206Bin)

	// The all-206 embed must be substantially larger; if it is NOT, the default
	// build is silently embedding all grammars (AC#3 regression).
	const minAll206Overhead = 15 << 20 // 15 MiB: comfortably below the measured ~22 MiB gap, above noise
	if all206Size-subsetSize < minAll206Overhead {
		t.Errorf("default build is not subset-tagged: subset=%d all206=%d (Δ=%d < %d). "+
			"The shipped default appears to embed all 206 grammars (AC#3 prohibits this).",
			subsetSize, all206Size, all206Size-subsetSize, minAll206Overhead)
	}
}

func mustSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fi.Size()
}

func TestBuild_ProducesCGoFreeVersionStampedBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping release build in -short mode")
	}
	bin := filepath.Join(t.TempDir(), "graphi")
	if err := Build(context.Background(), BuildConfig{Version: "0.0.0-test"}, bin); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("binary not produced: %v", err)
	}
	bi, err := ReadBuildInfo(context.Background(), bin)
	if err != nil {
		t.Fatalf("ReadBuildInfo: %v", err)
	}
	if bi.CGOEnabled != "0" {
		t.Errorf("CGO_ENABLED = %q, want 0 (static CGo-free binary)", bi.CGOEnabled)
	}
	if bi.Version != "0.0.0-test" {
		t.Errorf("Version = %q, want 0.0.0-test (ldflags stamping)", bi.Version)
	}

	// The static claims must remain verifiable when the shipped binary is run
	// from a directory with no go.mod and no source checkout. The live egress
	// check may still be UNVERIFIED on an unprivileged host, so inspect its
	// output independently of the aggregate exit code.
	cmd := exec.Command(bin, "privacy-audit")
	cmd.Dir = t.TempDir()
	out, _ := cmd.CombinedOutput()
	text := string(out)
	for _, want := range []string{
		"CGo-free build [PASS] (build-time attestation for this binary)",
		"No telemetry [PASS] (build-time attestation for this binary)",
		"binary_sha256=",
		"embedded build evidence is not an independent signature",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("privacy-audit outside the module missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "go list") || strings.Contains(text, "exit status") {
		t.Fatalf("packaged audit leaked a source-toolchain failure outside the module:\n%s", text)
	}
}

func TestVerifyReproducible_ByteIdentical(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping reproducible double-build in -short mode")
	}
	if os.Getenv("RELEASE_SKIP_REPRODUCIBLE") == "1" {
		t.Skip("RELEASE_SKIP_REPRODUCIBLE=1")
	}
	sha, ok, err := VerifyReproducible(context.Background(), BuildConfig{Version: "0.0.0-test"})
	if err != nil {
		t.Fatalf("VerifyReproducible: %v", err)
	}
	if !ok {
		t.Fatalf("release build NOT reproducible: two builds of the same revision differ (sha=%s)", sha)
	}
	if sha == "" {
		t.Error("empty sha256")
	}
}

func TestParseVersionOutput(t *testing.T) {
	bi := parseVersionOutput("graphi version=1.2.3 commit=abc123def date=2026-06-20T00:00:00Z\n")
	if bi.Version != "1.2.3" {
		t.Errorf("Version=%q", bi.Version)
	}
	if bi.VCSRevision != "abc123def" {
		t.Errorf("VCSRevision=%q", bi.VCSRevision)
	}
	if bi.VCSTime != "2026-06-20T00:00:00Z" {
		t.Errorf("VCSTime=%q", bi.VCSTime)
	}
}
