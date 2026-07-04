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
	// 3 = nightly/manual large repos. Defaults to 1 for backward compatibility.
	Tier int `json:"tier,omitempty"`
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
	Notes       string       `json:"notes,omitempty"`
	Entries     []Entry      `json:"entries"`
	TierBudgets []TierBudget `json:"tier_budgets,omitempty"`
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
		if e.Tier != 0 && (e.Tier < 1 || e.Tier > 3) {
			return Manifest{}, fmt.Errorf("corpus: entry %q has invalid tier %d (must be 1, 2, or 3)", e.Name, e.Tier)
		}
		if e.Tier == 0 {
			// Default to tier 1 for backward compatibility.
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
	return m, nil
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
