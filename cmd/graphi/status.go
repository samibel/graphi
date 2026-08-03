package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/samibel/graphi/internal/freshness"
	"github.com/samibel/graphi/internal/gitinfo"
	"github.com/samibel/graphi/internal/state"
)

// statusJSONSchemaVersion versions the `graphi status --json` document. Bump
// only on breaking changes to the shape or value domain.
const statusJSONSchemaVersion = 1

// statusReport is the stable `graphi status --json` document. Every field is
// always present; empty strings / zero values are used instead of omission.
type statusReport struct {
	SchemaVersion  int              `json:"schema_version"`
	Repo           string           `json:"repo"`
	Git            statusGit        `json:"git"`
	DBPath         string           `json:"db_path"`
	NodeCount      int              `json:"node_count"`
	Profile        string           `json:"profile"`
	LastSync       statusLastSync   `json:"last_sync"`
	Index          statusIndexState `json:"index"`
	Drift          statusDrift      `json:"drift"`
	Current        bool             `json:"current"`
	Recommendation string           `json:"recommendation"`
}

type statusGit struct {
	Present  bool   `json:"present"`
	Branch   string `json:"branch"`
	Commit   string `json:"commit"`
	Detached bool   `json:"detached"`
}

type statusLastSync struct {
	Recorded bool   `json:"recorded"`
	Time     string `json:"time"` // RFC3339 UTC, "" when never recorded
	Branch   string `json:"branch"`
	Commit   string `json:"commit"`
}

type statusIndexState struct {
	Exists        bool `json:"exists"`
	WarmStartable bool `json:"warm_startable"`
	FilesCached   int  `json:"files_cached"`
	// FullPassInProgress reports the sidecar's full-pass recovery marker;
	// LockHeld reports whether another process currently holds the
	// cross-process ingest lock. Together they split "index running right
	// now" from "previous index crashed". Additive under schema_version 1.
	FullPassInProgress bool `json:"full_pass_in_progress"`
	LockHeld           bool `json:"lock_held"`
}

type statusDrift struct {
	Added   int `json:"added"`
	Changed int `json:"changed"`
	Removed int `json:"removed"`
}

// runStatus reports whether the graph matches the checked-out code. Usage:
//
//	graphi status [--json] [-root <repo>] [-db path] [-meta dir]
//
// Strictly read-only: the store and sidecar are opened mode=ro, nothing is
// created, nothing is ingested. Exit codes: 0 = graph exists and is current;
// 1 = actionable (never indexed, stale, or a store needing rebuild) so
// `graphi status || graphi sync` scripts cleanly; 2 = not a repository or an
// operational error.
func runStatus(args []string) int {
	return runStatusAt(getwd(), args, os.Stdout)
}

func runStatusAt(cwd string, args []string, stdout io.Writer) int {
	asJSON := false
	rest := args[:0:0]
	for _, a := range args {
		if a == "--json" || a == "-json" {
			asJSON = true
			continue
		}
		rest = append(rest, a)
	}
	root, dbPath, metaDir, _ := parseIngestVerbFlags(rest)

	if root == "" {
		detected, ok := state.DetectRepo(cwd)
		if !ok {
			printNotARepo("status")
			return 2
		}
		root = detected
	}
	facts, err := freshness.Compute(context.Background(), root, dbPath, metaDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: status: %v\n", err)
		return 2
	}
	return emitStatus(stdout, statusReportOf(facts), asJSON)
}

// statusReportOf maps the shared freshness facts onto the versioned JSON
// document; the schema and all formatting stay at the cmd rank.
func statusReportOf(f freshness.Report) statusReport {
	r := statusReport{
		SchemaVersion: statusJSONSchemaVersion,
		Repo:          f.Repo,
		Git:           statusGit{Present: f.GitPresent, Branch: f.Git.Branch, Commit: f.Git.Commit, Detached: f.Git.Detached},
		DBPath:        f.DBPath,
		NodeCount:     f.NodeCount,
		Profile:       f.Profile,
		Index: statusIndexState{
			Exists:             f.Index.Exists,
			WarmStartable:      f.Index.WarmStartable,
			FilesCached:        f.Index.FilesCached,
			FullPassInProgress: f.Index.FullPassInProgress,
			LockHeld:           f.Index.LockHeld,
		},
		Drift:          statusDrift{Added: f.Drift.Added, Changed: f.Drift.Changed, Removed: f.Drift.Removed},
		Current:        f.Current,
		Recommendation: f.Recommendation,
	}
	if f.LastSync.Recorded {
		r.LastSync = statusLastSync{Recorded: true, Time: f.LastSync.Time.UTC().Format(time.RFC3339), Branch: f.LastSync.Branch, Commit: f.LastSync.Commit}
	}
	return r
}

// emitStatus renders the report (human or JSON) and maps it to the exit code
// contract: 0 current, 1 actionable.
func emitStatus(w io.Writer, r statusReport, asJSON bool) int {
	if asJSON {
		if err := renderStatusJSON(w, r); err != nil {
			fmt.Fprintf(os.Stderr, "graphi: status: %v\n", err)
			return 2
		}
	} else {
		renderStatusHuman(w, r)
	}
	if r.Current {
		return 0
	}
	return 1
}

// renderStatusJSON mirrors the doctor JSON conventions: ordered struct keys,
// no HTML escaping, exactly one trailing newline.
func renderStatusJSON(w io.Writer, r statusReport) error {
	buf := &bytes.Buffer{}
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return err
	}
	b := bytes.TrimRight(buf.Bytes(), "\n")
	_, err := w.Write(append(b, '\n'))
	return err
}

func renderStatusHuman(w io.Writer, r statusReport) {
	fmt.Fprintf(w, "repo:    %s\n", r.Repo)
	if r.Git.Present {
		info := gitinfo.Info{Branch: r.Git.Branch, Commit: r.Git.Commit, Detached: r.Git.Detached}
		fmt.Fprintf(w, "branch:  %s\n", info.Short())
	}

	if !r.Index.Exists {
		fmt.Fprintln(w, "graph:   not built yet")
	} else {
		graphLine := fmt.Sprintf("%s — %d nodes", r.DBPath, r.NodeCount)
		if r.Profile != "" {
			graphLine += fmt.Sprintf(" (profile %s)", r.Profile)
		}
		fmt.Fprintf(w, "graph:   %s\n", graphLine)
	}
	if r.LastSync.Recorded {
		syncedLine := r.LastSync.Time
		if t, err := time.Parse(time.RFC3339, r.LastSync.Time); err == nil {
			syncedLine = t.UTC().Format("2006-01-02 15:04 MST")
		}
		if short := lastSyncShort(r.LastSync.Branch, r.LastSync.Commit); short != "" {
			syncedLine += " on " + short
		}
		fmt.Fprintf(w, "synced:  %s\n", syncedLine)
	}

	switch {
	case r.Current:
		fmt.Fprintln(w, "status:  current")
	case !r.Index.Exists:
		fmt.Fprintln(w, "status:  no graph for this repository yet")
	case !r.Index.WarmStartable && r.Index.FullPassInProgress && r.Index.LockHeld:
		fmt.Fprintln(w, "status:  indexing in progress — another graphi process is building this index")
	case !r.Index.WarmStartable && r.Index.FullPassInProgress:
		fmt.Fprintln(w, "status:  previous index did not complete — the next sync will re-run the full pass")
	case !r.Index.WarmStartable:
		fmt.Fprintln(w, "status:  index needs a rebuild (incomplete, or built by another graphi version)")
	default:
		fmt.Fprintf(w, "status:  stale — %d added, %d changed, %d removed since last sync\n",
			r.Drift.Added, r.Drift.Changed, r.Drift.Removed)
	}
	if r.Git.Present && r.LastSync.Recorded && r.LastSync.Branch != "" &&
		r.Git.Branch != "" && r.Git.Branch != r.LastSync.Branch {
		fmt.Fprintf(w, "note:    branch changed since last sync: %s → %s\n", r.LastSync.Branch, r.Git.Branch)
	}
	if r.Recommendation != "" {
		fmt.Fprintf(w, "hint:    %s\n", r.Recommendation)
	}
}
