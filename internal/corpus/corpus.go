// Package corpus is the real-repository smoke harness (roadmap Phase 3). It
// drives the BUILT graphi binary end-to-end — index → search → query →
// analyze → diagnose — against a manifest of pinned real-world repositories
// and fails on any crash, non-zero exit, panic marker, or empty result where
// the manifest promises one.
//
// Rationale: every post-release bug so far (.DS_Store, pnpm symlinks,
// malformed JSON fixtures) was a real-repo first-contact crash that no
// synthetic fixture exercised. This harness turns that bug class from "user
// report" into "CI red".
//
// It mirrors the internal/bench + internal/canary pattern: a thin cmd/corpus
// entrypoint, logic and tests here, and a dedicated workflow
// (.github/workflows/corpus.yml). Unlike the canary, the workflow needs the
// network (shallow clones), so it is a SEPARATE workflow and never part of
// the zero-egress posture; this package's own tests stay hermetic by using
// manifest entries with a local Path instead of a URL.
//
// Assertions live in the manifest, not in code: adding a repository is a data
// change. Results are shape-only (exit codes, valid JSON, non-emptiness) —
// deliberately NO golden snapshots of full query output, which would rot on
// every re-pin.
package corpus

import (
	"encoding/json"
	"fmt"
	"os"
)

// Search is one search assertion: run `graphi search <Query>` against the
// indexed repo and, when ExpectNonEmpty, require at least one match. The first
// match's node id seeds the query/analyze steps.
type Search struct {
	Query          string `json:"query"`
	ExpectNonEmpty bool   `json:"expect_nonempty"`
}

// ConfirmedEdge is one confirmed-tier assertion (the v0.2.0 typeresolve
// acceptance shape): resolve SymbolQuery to an anchor node — the first search
// match whose exact symbol name equals the query — run the structural query
// Operation over it, and require at least Min of the returned edges to carry
// the confirmed tier. This is how the corpus proves the go/types pass derives
// real proven edges on real repositories, not just on fixtures.
type ConfirmedEdge struct {
	SymbolQuery string `json:"symbol_query"`
	Operation   string `json:"operation"` // callers | callees | references
	Min         int    `json:"min"`
}

// FileCensus is the file count of one repository, MEASURED from a real clone
// at the pinned SHA — never estimated from repository metadata. It is the
// evidence behind corpus-size claims (FR-2's >=10,000-source-file stress
// target), so Method records the exact command that produced the numbers and
// MeasuredAt when, making the census reproducible by anyone re-cloning the pin.
type FileCensus struct {
	// GoFiles is the number of tracked *.go files at the pinned SHA. This is
	// the STRICTEST reading of "source files" — it counts no docs, YAML or
	// vendored assets — and is what the stress threshold is asserted on.
	GoFiles int `json:"go_files"`
	// TrackedFiles is every tracked file at the pinned SHA (context for
	// GoFiles, not a substitute for it).
	TrackedFiles int `json:"tracked_files"`
	// GoModules is the number of go.mod files (the multi-module property).
	GoModules int `json:"go_modules,omitempty"`
	// MeasuredAt is the ISO date the census was taken.
	MeasuredAt string `json:"measured_at"`
	// Method is the exact command sequence behind the numbers.
	Method string `json:"method"`
}

// StressMinGoFiles is the FR-2 stress-target threshold: an entry declaring
// Stress must MEASURE at least this many Go files, so "stress target" can
// never become a label an ordinary repository wears.
const StressMinGoFiles = 10000

// PropertyMapping maps one FR-2 stratification property to the repository that
// carries it. A property no selected repository covers is recorded as an
// explicit Gap with a reason — omitting it silently is what this type exists to
// prevent.
type PropertyMapping struct {
	Property string `json:"property"`
	// Repo is the entry name carrying the property; empty only when Gap.
	Repo string `json:"repo,omitempty"`
	// Gap marks the property as uncovered; Evidence must then say why.
	Gap bool `json:"gap,omitempty"`
	// Evidence is the measured justification ("11 go.mod files"), or, for a
	// gap, why no repository carries it.
	Evidence string `json:"evidence"`
}

// Entry is one corpus repository. Exactly one of URL or Path must be set:
// URL entries are shallow-cloned at Ref (a tag or branch) by the runner —
// the workflow context; Path entries point at an already-materialized local
// checkout — the hermetic test context.
type Entry struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
	// Ref is the tag (preferred) or branch to shallow-clone. Tags of released
	// versions are the pin; SHA tightens it further.
	Ref string `json:"ref,omitempty"`
	// SHA, when non-empty, must be a case-insensitive PREFIX of the checkout's
	// HEAD (fail-closed pin; >=12 hex chars enforced, standard git short-sha
	// practice). Leave empty on first onboarding; copy the recorded head_sha
	// from the report artifact (or the run log's HEAD column) of the first
	// green run to tighten the pin.
	SHA string `json:"sha,omitempty"`
	// Path is a local checkout used instead of cloning (hermetic tests).
	Path string `json:"path,omitempty"`
	// Searches are the per-repo assertions; at least one with ExpectNonEmpty
	// is required so the smoke run proves the index actually contains symbols.
	Searches []Search `json:"searches"`
	// ConfirmedEdges are optional confirmed-tier assertions (see ConfirmedEdge).
	ConfirmedEdges []ConfirmedEdge `json:"confirmed_edges,omitempty"`
	// Tier is the corpus tier: 1 = PR gate (local fixtures), 2 = pinned SHAs,
	// 3 = nightly/manual large repos, 4 = manual-only stress targets (never
	// scheduled: corpus.yml caps the nightly run at tier 3, so a tier-4 entry
	// runs only under an explicit `-tier 4` or through cmd/eval -full-run).
	// Defaults to 1 for backward compatibility.
	Tier int `json:"tier,omitempty"`
	// Language is the entry's primary language ("go", "python", …). It makes
	// the corpus's language composition auditable instead of inferred from
	// repository names.
	Language string `json:"language,omitempty"`
	// License is the SPDX identifier of the repository's license and
	// PermittedUse states why this corpus may use it (FR-2 requires both to be
	// documented). Required on URL entries — a repository whose terms are not
	// written down must not be cloned by an evaluation.
	License      string `json:"license,omitempty"`
	PermittedUse string `json:"permitted_use,omitempty"`
	// Properties are the FR-2 stratification properties this entry carries;
	// Manifest.Stratification is the authoritative property -> repo map and
	// this is the per-entry view of it.
	Properties []string `json:"properties,omitempty"`
	// Measured is the file census taken from a real clone at SHA.
	Measured *FileCensus `json:"measured,omitempty"`
	// Stress declares this entry the FR-2 stress target. It is fail-closed:
	// the declaration is only accepted with a census of at least
	// StressMinGoFiles Go files.
	Stress bool `json:"stress,omitempty"`
	// BudgetMS is the declared wall-clock budget for this entry in milliseconds.
	// It is surfaced in the report as warn-only metadata.
	BudgetMS int64 `json:"budget_ms,omitempty"`
	// ScenarioRef is a stable identifier reserved for scenario anchoring (C3).
	// It is defined here but left unexecuted by this story.
	ScenarioRef string `json:"scenario_ref,omitempty"`
}

// TierBudget is the per-tier budget metadata.
type TierBudget struct {
	Tier     int   `json:"tier"`
	BudgetMS int64 `json:"budget_ms"`
}

// Manifest is the checked-in corpus definition (corpus/manifest.json).
type Manifest struct {
	// Version is the manifest schema stamp (cmd/eval records it in every
	// report header so a measurement always names the corpus it ran against).
	Version int    `json:"version,omitempty"`
	Notes   string `json:"notes,omitempty"`
	// Stratification maps each required corpus property to the repository
	// carrying it, or records it as an explicit gap.
	Stratification []PropertyMapping `json:"stratification,omitempty"`
	Entries        []Entry           `json:"entries"`
	TierBudgets    []TierBudget      `json:"tier_budgets,omitempty"`
}

// LoadManifest reads and validates the manifest at path.
func LoadManifest(path string) (Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("corpus: read manifest %q: %w", path, err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Manifest{}, fmt.Errorf("corpus: parse manifest %q: %w", path, err)
	}
	if len(m.Entries) == 0 {
		return Manifest{}, fmt.Errorf("corpus: manifest %q has no entries", path)
	}
	for i, e := range m.Entries {
		if e.Name == "" {
			return Manifest{}, fmt.Errorf("corpus: entry %d has no name", i)
		}
		if (e.URL == "") == (e.Path == "") {
			return Manifest{}, fmt.Errorf("corpus: entry %q must set exactly one of url or path", e.Name)
		}
		if e.URL != "" && e.Ref == "" {
			return Manifest{}, fmt.Errorf("corpus: entry %q has a url but no ref (pin a release tag)", e.Name)
		}
		if e.Tier != 0 && (e.Tier < 1 || e.Tier > 4) {
			return Manifest{}, fmt.Errorf("corpus: entry %q has invalid tier %d (must be 1, 2, 3, or 4)", e.Name, e.Tier)
		}
		if e.Tier == 0 {
			// Default to tier 1 for backward compatibility. Mutate by index:
			// e is a copy, so writing e.Tier would not normalize the manifest.
			m.Entries[i].Tier = 1
		}
		if e.URL != "" && e.Tier >= 2 && e.SHA == "" {
			return Manifest{}, fmt.Errorf("corpus: entry %q tier %d URL entry requires an exact SHA pin", e.Name, e.Tier)
		}
		if e.BudgetMS < 0 {
			return Manifest{}, fmt.Errorf("corpus: entry %q has negative budget_ms", e.Name)
		}
		if e.SHA != "" && !validShortSHA(e.SHA) {
			return Manifest{}, fmt.Errorf("corpus: entry %q sha %q must be >=12 hex chars (a git sha prefix)", e.Name, e.SHA)
		}
		if e.URL != "" && e.License == "" {
			return Manifest{}, fmt.Errorf("corpus: entry %q has a url but no license (a repository whose terms are undocumented must not be cloned)", e.Name)
		}
		if e.URL != "" && e.Language == "" {
			return Manifest{}, fmt.Errorf("corpus: entry %q has a url but no language (the corpus composition must be auditable)", e.Name)
		}
		if err := validateCensus(e); err != nil {
			return Manifest{}, err
		}
		if e.Stress {
			if e.Measured == nil {
				return Manifest{}, fmt.Errorf("corpus: entry %q declares stress without a measured census (the size claim needs evidence)", e.Name)
			}
			if e.Measured.GoFiles < StressMinGoFiles {
				return Manifest{}, fmt.Errorf("corpus: entry %q declares stress with %d measured go files, need >= %d", e.Name, e.Measured.GoFiles, StressMinGoFiles)
			}
		}
		nonEmpty := false
		for _, s := range e.Searches {
			if s.Query == "" {
				return Manifest{}, fmt.Errorf("corpus: entry %q has a search with an empty query", e.Name)
			}
			nonEmpty = nonEmpty || s.ExpectNonEmpty
		}
		if !nonEmpty {
			return Manifest{}, fmt.Errorf("corpus: entry %q needs at least one expect_nonempty search (a smoke run must prove the index is non-trivial)", e.Name)
		}
		for _, ce := range e.ConfirmedEdges {
			if ce.SymbolQuery == "" {
				return Manifest{}, fmt.Errorf("corpus: entry %q has a confirmed_edges assertion with an empty symbol_query", e.Name)
			}
			switch ce.Operation {
			case "callers", "callees", "references":
			default:
				return Manifest{}, fmt.Errorf("corpus: entry %q confirmed_edges operation %q must be callers, callees, or references", e.Name, ce.Operation)
			}
			if ce.Min < 1 {
				return Manifest{}, fmt.Errorf("corpus: entry %q confirmed_edges min %d must be >= 1 (a zero-minimum assertion is vacuous)", e.Name, ce.Min)
			}
		}
	}
	if err := validateStratification(m); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// validateCensus keeps a recorded file count honest: counts must be positive
// and carry the date and command that produced them, so the number can be
// re-derived from the pin instead of trusted.
func validateCensus(e Entry) error {
	c := e.Measured
	if c == nil {
		return nil
	}
	if c.GoFiles < 0 || c.TrackedFiles <= 0 {
		return fmt.Errorf("corpus: entry %q measured census must count at least one tracked file", e.Name)
	}
	if c.GoFiles > c.TrackedFiles {
		return fmt.Errorf("corpus: entry %q measured %d go files of %d tracked files (impossible)", e.Name, c.GoFiles, c.TrackedFiles)
	}
	if c.MeasuredAt == "" || c.Method == "" {
		return fmt.Errorf("corpus: entry %q measured census needs measured_at and method (an unattributed count is an estimate)", e.Name)
	}
	return nil
}

// validateStratification pins the property map to reality: every mapping names
// a property, and points either at an entry that exists in this manifest or at
// an explicitly justified gap. A typo'd repository name would otherwise read as
// coverage the corpus does not have.
func validateStratification(m Manifest) error {
	known := make(map[string]bool, len(m.Entries))
	for _, e := range m.Entries {
		known[e.Name] = true
	}
	seen := make(map[string]bool, len(m.Stratification))
	for i, p := range m.Stratification {
		if p.Property == "" {
			return fmt.Errorf("corpus: stratification %d has no property name", i)
		}
		if seen[p.Property] {
			return fmt.Errorf("corpus: stratification property %q is mapped twice", p.Property)
		}
		seen[p.Property] = true
		if p.Evidence == "" {
			return fmt.Errorf("corpus: stratification property %q has no evidence", p.Property)
		}
		if p.Gap {
			if p.Repo != "" {
				return fmt.Errorf("corpus: stratification property %q is marked as a gap but names repo %q", p.Property, p.Repo)
			}
			continue
		}
		if p.Repo == "" {
			return fmt.Errorf("corpus: stratification property %q names no repo and is not marked as a gap", p.Property)
		}
		if !known[p.Repo] {
			return fmt.Errorf("corpus: stratification property %q names unknown repo %q", p.Property, p.Repo)
		}
	}
	return nil
}

// validShortSHA reports whether s is a plausible git sha prefix: >=12 and
// <=40 hex characters. 12 is git's conventional unambiguous short length;
// anything shorter would make the prefix pin vacuous.
func validShortSHA(s string) bool {
	if len(s) < 12 || len(s) > 40 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// StepResult is one executed step of an entry run.
type StepResult struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// EntryReport is the per-repository outcome.
type EntryReport struct {
	Name       string       `json:"name"`
	URL        string       `json:"url,omitempty"`
	Ref        string       `json:"ref,omitempty"`
	HeadSHA    string       `json:"head_sha,omitempty"`
	Tier       int          `json:"tier,omitempty"`
	BudgetMS   int64        `json:"budget_ms,omitempty"`
	Pass       bool         `json:"pass"`
	DurationMS int64        `json:"duration_ms"`
	Steps      []StepResult `json:"steps"`
}

// Report is the machine-readable harness outcome (uploaded as a CI artifact).
type Report struct {
	Pass    bool          `json:"pass"`
	Entries []EntryReport `json:"entries"`
}

// WriteReport writes the report as indented JSON to path.
func WriteReport(r Report, path string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("corpus: marshal report: %w", err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("corpus: write report %q: %w", path, err)
	}
	return nil
}
