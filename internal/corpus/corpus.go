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
	// SHA, when non-empty, must equal the checkout's HEAD (fail-closed pin).
	// Leave empty on first onboarding; copy the recorded head_sha from the
	// report artifact of the first green run to tighten the pin.
	SHA string `json:"sha,omitempty"`
	// Path is a local checkout used instead of cloning (hermetic tests).
	Path string `json:"path,omitempty"`
	// Searches are the per-repo assertions; at least one with ExpectNonEmpty
	// is required so the smoke run proves the index actually contains symbols.
	Searches []Search `json:"searches"`
	// Notes is free-form documentation (why this repo is in the corpus).
	Notes string `json:"notes,omitempty"`
}

// Manifest is the checked-in corpus definition (corpus/manifest.json).
type Manifest struct {
	Notes   string  `json:"notes,omitempty"`
	Entries []Entry `json:"entries"`
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
	}
	return m, nil
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
