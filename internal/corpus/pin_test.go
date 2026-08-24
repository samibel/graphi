package corpus

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// crossFileHeuristicResidualLangs is the closed set of nine cross-file-
// heuristic residual languages (SW-184's audit output). For each, the v3
// standard demands either a complete pin (release-tag ref, full 40-character
// sha, tier, measured block, language-specific stratification) OR an honest
// abstention (no_pin: true with a named no_pin_reason). A missing field
// fails the test with the language and field named — silence is the defect
// class SW-196 exists to prevent.
//
// The values match cmd/eval/sourcefamily.go's sourceFamilies.name strings
// (the corpus's language-metadata convention). The engine linker registers
// the csharp resolver under `c_sharp` (the LINK-001 / ADR 0011 convention)
// and the evidence-index uses `c_sharp` for the GA-LANG-c_sharp-G<n> row
// ids — the corpus metadata is a documentation surface and may differ from
// the linker name when the audit picks the legacy spelling.
var crossFileHeuristicResidualLangs = []string{
	"bash",
	"c",
	"csharp",
	"cpp",
	"lua",
	"php",
	"ruby",
	"rust",
	"sql",
}

// v3PinnedLangs names the residual languages SW-196 lifts to a v3 pin at the
// measured standard (one real open-source repository at the pin tier, with
// the four v3 fields ref / 40-char sha / tier / measured + a per-language
// stratification list of at least 8 properties).
var v3PinnedLangs = map[string]string{
	"c":      "cjson",
	"csharp": "Newtonsoft.Json",
	"cpp":    "nlohmann/json",
	"lua":    "lua-resty-core",
	"php":    "composer",
	"ruby":   "sinatra",
	"rust":   "serde",
}

// v3AbstentionLangs names the residual languages with NO representative
// open-source repository at the pin tier — SW-196's honest abstentions.
// The runner records an abstention step that passes by name, the G5 row
// stays UNKNOWN with the no_pin_reason named in evidence-index.yaml, and
// no v3 pin is silently substituted (SW-196 AC-2). Each value lists key
// audit-named phrases that MUST appear in the entry's no_pin_reason, so
// the gap's substance (not its exact wording) is what the test pins.
var v3AbstentionLangs = map[string][]string{
	"bash": {
		"bash",
		"no representative",
		"pin tier",
	},
	"sql": {
		"sql",
		"no representative",
		"pin tier",
		"adr-w1",
	},
}

// TestCheckedInManifest_V3CorpusPins asserts the SW-196 v3 standard for the
// cross-file-heuristic residual: every language in the closed nine-language
// set has either a v3 pin (ref + 40-char sha + tier + measured + stratification)
// or an honest no_pin abstention (no_pin + named no_pin_reason). A missing
// field fails the test with language + field named, so the discipline is
// testable rather than asserted-on-faith.
func TestCheckedInManifest_V3CorpusPins(t *testing.T) {
	m := loadCheckedInManifest(t)

	// Index entries by name and language for the test.
	byLang := map[string]Entry{}
	for _, e := range m.Entries {
		byLang[e.Language] = e
	}

	for _, lang := range crossFileHeuristicResidualLangs {
		e, ok := byLang[lang]
		if !ok {
			t.Errorf("language %q has NO entry in corpus/manifest.json (neither v3 pin nor honest abstention)", lang)
			continue
		}

		// Honesty branch: no_pin entries must declare a reason and carry
		// NO URL/Path (a silent substitution would be the defect class).
		if e.NoPin {
			if e.NoPinReason == "" {
				t.Errorf("language %q: no_pin entry has no no_pin_reason (the gap must be named)", lang)
			}
			if e.URL != "" || e.Path != "" {
				t.Errorf("language %q: no_pin entry must not also set url/path (silent substitution)", lang)
			}
			if wants, ok := v3AbstentionLangs[lang]; ok {
				for _, want := range wants {
					if !strings.Contains(strings.ToLower(e.NoPinReason), want) {
						t.Errorf("language %q: no_pin_reason missing audit phrase %q (got %q)", lang, want, e.NoPinReason)
					}
				}
			}
			continue
		}

		// V3 pin branch: every required field must be present.
		if e.URL == "" {
			t.Errorf("language %q: v3 pin entry has no url", lang)
		}
		if e.Ref == "" {
			t.Errorf("language %q: v3 pin entry has no ref (release-tag)", lang)
		}
		if len(e.SHA) != 40 {
			t.Errorf("language %q: v3 pin entry sha %q must be a full 40-character commit sha (FR-2 v3 standard)", lang, e.SHA)
		}
		if e.Tier < 1 || e.Tier > 4 {
			t.Errorf("language %q: v3 pin entry tier %d is invalid (must be 1..4)", lang, e.Tier)
		}
		if e.Measured == nil {
			t.Errorf("language %q: v3 pin entry has no measured census (block count taken from a real clone at the pinned sha is required)", lang)
		} else {
			if e.Measured.SourceFiles <= 0 {
				t.Errorf("language %q: v3 pin entry measured.source_files must be > 0 (taken from a real clone at the pinned sha)", lang)
			}
			if e.Measured.TrackedFiles <= 0 {
				t.Errorf("language %q: v3 pin entry measured.tracked_files must be > 0", lang)
			}
			if e.Measured.MeasuredAt == "" {
				t.Errorf("language %q: v3 pin entry measured.measured_at is required (the date the census was taken)", lang)
			}
			if e.Measured.Method == "" {
				t.Errorf("language %q: v3 pin entry measured.method is required (the exact command that produced the numbers)", lang)
			}
		}
		if len(e.Properties) < 8 {
			t.Errorf("language %q: v3 pin entry has %d properties, want >= 8 (language-specific stratification, the v3 standard)", lang, len(e.Properties))
		}
		if e.License == "" {
			t.Errorf("language %q: v3 pin entry has no license (documented terms required for any URL entry)", lang)
		}
		if want, ok := v3PinnedLangs[lang]; ok && !strings.Contains(strings.ToLower(e.URL), strings.ToLower(want)) {
			t.Errorf("language %q: v3 pin entry url %q does not point at the audit-named pin %q", lang, e.URL, want)
		}
	}
}

// TestCheckedInManifest_V3CorpusPins_HonestNoSubstitution is a second guard
// against silent substitution: a NoPin entry MUST NOT carry a measured census,
// a ref, a sha, or a tier higher than 3 — those fields would be the beginning
// of a low-quality pin pretending to be a gap. The test pins the shape of an
// honest abstention so future maintainers cannot tighten it into a fake pin
// by accident.
func TestCheckedInManifest_V3CorpusPins_HonestNoSubstitution(t *testing.T) {
	m := loadCheckedInManifest(t)

	for _, e := range m.Entries {
		if !e.NoPin {
			continue
		}
		if e.Measured != nil {
			t.Errorf("no_pin entry %q carries a measured census (silent substitution of a real pin)", e.Name)
		}
		if e.Ref != "" {
			t.Errorf("no_pin entry %q carries a ref (silent substitution of a real pin)", e.Name)
		}
		if e.SHA != "" {
			t.Errorf("no_pin entry %q carries a sha (silent substitution of a real pin)", e.Name)
		}
		if e.Tier != 0 && e.Tier != 3 {
			t.Errorf("no_pin entry %q carries tier %d (only tier 3 — nightly/manual — is honest for an abstention)", e.Name, e.Tier)
		}
	}
}

// loadCheckedInManifestFromDir mirrors the helper in corpus_test.go but lives
// in this file so the v3 standard can be tested without depending on the
// schema-guard file's helpers.
func loadCheckedInManifestFromDir(t *testing.T, dir string) Manifest {
	t.Helper()
	m, err := LoadManifest(filepath.Join(dir, "corpus", "manifest.json"))
	if err != nil {
		t.Fatalf("checked-in manifest invalid: %v", err)
	}
	return m
}

// TestCheckedInManifest_AbstentionsReachLoad is the loader-level round-trip:
// a manifest with two no_pin entries parses, satisfies LoadManifest, and the
// runner's abstention path returns Pass=true. The runner binary is the same
// one every other entry uses — no separate harness.
func TestCheckedInManifest_AbstentionsReachLoad(t *testing.T) {
	root, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Skipf("go env GOMOD unavailable: %v", err)
	}
	dir := filepath.Dir(strings.TrimSpace(string(root)))
	m, err := LoadManifest(filepath.Join(dir, "corpus", "manifest.json"))
	if err != nil {
		t.Fatalf("checked-in manifest invalid: %v", err)
	}

	abstentions := 0
	for _, e := range m.Entries {
		if !e.NoPin {
			continue
		}
		abstentions++
		if e.NoPinReason == "" {
			t.Errorf("no_pin entry %q has no named reason", e.Name)
		}
	}
	if abstentions != len(v3AbstentionLangs) {
		t.Errorf("expected %d honest no_pin abstentions in the manifest, got %d", len(v3AbstentionLangs), abstentions)
	}
}
