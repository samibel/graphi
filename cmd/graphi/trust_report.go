package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/samibel/graphi/engine/trust"
	"github.com/samibel/graphi/internal/state"
	"github.com/samibel/graphi/surfaces/client"
)

// runTrustReport reports the trust surface of the repository's graph — the
// canonical contract §2 document over the persisted trust snapshot
// (docs/plan/2026-08-graphi-p1-trust-contract-v1.md). Usage:
//
//	graphi trust-report [--json] [--details] [--limit n] \
//	    [--target <symbol|path|package>] \
//	    [--policy exploratory-v1|review-v1|automated-change-v1] \
//	    [-root <repo>] [-db path] [-meta dir]
//
// Strictly read-only: like status it observes the auto-managed store mode=ro
// and never creates state; missing evidence composes the fail-closed
// UNAVAILABLE document instead of an error. --json emits the canonical bytes
// (byte-identical to the graph_health MCP tool for the same inputs).
//
// Exit codes (PRD v1.0 §6): 0 = policy PASS, or — without a policy — snapshot
// available and CURRENT; 1 = policy WARN; 2 = policy FAIL or UNVERIFIED, a
// snapshot that is not CURRENT when no policy was given, and every usage or
// operational error (not a repository, unknown policy, probe failure). Missing
// evidence never exits 0 (fail closed). See trustExitCode for why FAIL and
// usage errors share a code and how the two stay distinguishable.
func runTrustReport(args []string) int {
	return runTrustReportAt(getwd(), args, os.Stdout)
}

func runTrustReportAt(cwd string, args []string, stdout io.Writer) int {
	asJSON := false
	details := false
	target := ""
	policy := ""
	limit := 0
	rest := args[:0:0]
	for i := 0; i < len(args); i++ {
		a := args[i]
		// takeVal matches --name/-name in space form ("--name v") and equals
		// form ("--name=v"/"-name=v"). missing marks a flag given WITHOUT its
		// value: an input error (exit 2), never a silent drop — dropping e.g. a
		// trailing "--policy" would launder the requested policy gate into the
		// friendlier no-policy exit code.
		takeVal := func(name string) (val string, ok, missing bool) {
			if a == name || a == name[1:] {
				if i+1 < len(args) {
					i++
					return args[i], true, false
				}
				return "", false, true
			}
			if len(a) > len(name)+1 && a[:len(name)+1] == name+"=" {
				return a[len(name)+1:], true, false
			}
			short := name[1:]
			if len(a) > len(short)+1 && a[:len(short)+1] == short+"=" {
				return a[len(short)+1:], true, false
			}
			return "", false, false
		}
		switch {
		case a == "--json" || a == "-json":
			asJSON = true
		case a == "--details" || a == "-details":
			details = true
		default:
			if v, ok, miss := takeVal("--target"); ok || miss {
				if miss {
					fmt.Fprintln(os.Stderr, "graphi: trust-report: --target requires a value")
					return 2
				}
				target = v
				continue
			}
			if v, ok, miss := takeVal("--policy"); ok || miss {
				if miss {
					fmt.Fprintln(os.Stderr, "graphi: trust-report: --policy requires a value")
					return 2
				}
				policy = v
				continue
			}
			if v, ok, miss := takeVal("--limit"); ok || miss {
				if miss {
					fmt.Fprintln(os.Stderr, "graphi: trust-report: --limit requires a value")
					return 2
				}
				// strconv.Atoi, not fmt.Sscanf: Sscanf accepts trailing garbage
				// ("3abc" → 3, "0x10" → 0 = uncapped), silently laundering a
				// malformed limit into a different evidence bound.
				n, err := strconv.Atoi(v)
				if err != nil || n < 0 {
					fmt.Fprintf(os.Stderr, "graphi: trust-report: invalid --limit %q\n", v)
					return 2
				}
				limit = n
				continue
			}
			rest = append(rest, a)
		}
	}
	root, dbPath, metaDir, _ := parseIngestVerbFlags(rest)

	if root == "" {
		detected, ok := state.DetectRepo(cwd)
		if !ok {
			printNotARepo("trust-report")
			return 2
		}
		root = detected
	}

	doc, verdict, st, err := client.TrustReport(context.Background(), client.TrustReportOptions{
		Root:    root,
		DBPath:  dbPath,
		MetaDir: metaDir,
		Target:  target,
		Policy:  policy,
		Details: details,
		Limit:   limit,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: trust-report: %v\n", err)
		return 2
	}
	if asJSON {
		if _, werr := stdout.Write(append(doc, '\n')); werr != nil {
			fmt.Fprintf(os.Stderr, "graphi: trust-report: %v\n", werr)
			return 2
		}
	} else {
		renderTrustHuman(stdout, doc)
	}
	return trustExitCode(policy != "", verdict, st)
}

// trustExitCode maps the composition's verdict and snapshot state onto the
// documented exit contract. Pure; anything outside the closed verdict set
// falls to 2 — missing or unclassifiable evidence never reads as success.
//
// PRD v1.0 §6 collapsed the five-way table v0.8.0 shipped (0/1/2/3/4) into
// three codes, with FAIL and UNVERIFIED sharing 2 (delta doc §A3). That costs
// the exit code its ability to separate "the policy blocked me" from "you
// passed a bad flag", since usage errors are also 2 by this repository's CLI
// convention. The distinction is preserved on the other channel instead, and
// callers that need it must read there: a usage or operational failure writes
// to stderr and emits NO document, whereas FAIL and UNVERIFIED always emit the
// canonical document carrying the `policy.verdict` field. The concern was
// raised before the change and the owner chose PRD v1.0; this comment is the
// record so a later reader does not read the collapse as an oversight.
func trustExitCode(policyGiven bool, v trust.Verdict, st trust.State) int {
	if policyGiven {
		switch v {
		case trust.VerdictPass:
			return 0
		case trust.VerdictWarn:
			return 1
		default:
			// FAIL and UNVERIFIED alike. The default arm also absorbs any
			// value outside the closed set, which must block rather than pass.
			return 2
		}
	}
	if st == trust.StateCurrent {
		return 0
	}
	return 2
}

// trustHumanDoc mirrors the contract §2 wire fields the human renderer needs.
// The wire document is the seam: the renderer decodes the same bytes --json
// emits, so human and JSON output can never disagree about a fact.
type trustHumanDoc struct {
	SnapshotState string `json:"snapshot_state"`
	Generation    struct {
		ID           string `json:"id"`
		SourceCommit string `json:"source_commit"`
		Profile      string `json:"profile"`
	} `json:"graph_generation"`
	Freshness struct {
		Current bool `json:"current"`
		Drift   struct {
			Added   int `json:"added"`
			Changed int `json:"changed"`
			Removed int `json:"removed"`
		} `json:"drift"`
	} `json:"freshness"`
	Scope struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	} `json:"scope"`
	Coverage struct {
		FilesDiscovered  int `json:"files_discovered"`
		FilesIndexed     int `json:"files_indexed"`
		FilesSkipped     int `json:"files_skipped"`
		PackagesTotal    int `json:"packages_total"`
		PackagesDegraded int `json:"packages_degraded"`
	} `json:"coverage"`
	EdgeEvidence struct {
		Confirmed int `json:"confirmed"`
		Derived   int `json:"derived"`
		Heuristic int `json:"heuristic"`
	} `json:"edge_evidence"`
	Resolution struct {
		ResolvedExternal int `json:"resolved_external"`
		Skipped          int `json:"skipped"`
		Ambiguous        int `json:"ambiguous"`
		DroppedIntents   int `json:"dropped_intents"`
	} `json:"resolution"`
	Boundaries []struct {
		Code  string `json:"code"`
		Count int    `json:"count"`
	} `json:"boundaries"`
	Policy struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Version int    `json:"version"`
		Verdict string `json:"verdict"`
	} `json:"policy"`
	Limitations []struct {
		Code     string `json:"code"`
		Severity string `json:"severity"`
		Count    int    `json:"count"`
		Action   string `json:"action"`
	} `json:"limitations"`
	Findings []struct {
		Code     string `json:"code"`
		Severity string `json:"severity"`
		Message  string `json:"message"`
	} `json:"findings"`
}

// renderTrustHuman renders the compact human report (PRD §15 shape) from the
// canonical document bytes. The snapshot state leads and a non-CURRENT state
// carries an explicit unmissable note — UNAVAILABLE/STALE/INCOMPLETE must
// never read as healthy.
func renderTrustHuman(w io.Writer, doc []byte) {
	var d trustHumanDoc
	if err := json.Unmarshal(doc, &d); err != nil {
		// The composition produced these bytes; a decode failure is a bug, but
		// the facts must still reach the user.
		fmt.Fprintln(w, string(doc))
		return
	}

	fmt.Fprintln(w, "Graphi trust report")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "graph:\n")
	fmt.Fprintf(w, "  state:      %s\n", d.SnapshotState)
	if d.Generation.ID != "" {
		fmt.Fprintf(w, "  generation: %s\n", d.Generation.ID)
	}
	if d.Generation.SourceCommit != "" {
		fmt.Fprintf(w, "  source:     %s\n", d.Generation.SourceCommit)
	}
	if d.Generation.Profile != "" {
		fmt.Fprintf(w, "  profile:    %s\n", d.Generation.Profile)
	}
	if d.SnapshotState != string(trust.StateCurrent) {
		fmt.Fprintf(w, "  note:       trust evidence is %s — graph answers are unverified for the current source\n", d.SnapshotState)
	}

	if d.Scope.Kind != "" && d.Scope.Kind != "repository" {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "scope:\n")
		fmt.Fprintf(w, "  kind: %s\n", d.Scope.Kind)
		if d.Scope.ID != "" {
			fmt.Fprintf(w, "  id:   %s\n", d.Scope.ID)
		} else {
			fmt.Fprintf(w, "  id:   (unresolved)\n")
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "coverage:\n")
	fmt.Fprintf(w, "  files:          %d / %d indexed\n", d.Coverage.FilesIndexed, d.Coverage.FilesDiscovered)
	fmt.Fprintf(w, "  parse skipped:  %d\n", d.Coverage.FilesSkipped)
	fmt.Fprintf(w, "  packages:       %d\n", d.Coverage.PackagesTotal)
	fmt.Fprintf(w, "  degraded:       %d\n", d.Coverage.PackagesDegraded)

	fmt.Fprintln(w)
	fmt.Fprintf(w, "edge evidence:\n")
	fmt.Fprintf(w, "  confirmed:      %d\n", d.EdgeEvidence.Confirmed)
	fmt.Fprintf(w, "  derived:        %d\n", d.EdgeEvidence.Derived)
	fmt.Fprintf(w, "  heuristic:      %d\n", d.EdgeEvidence.Heuristic)

	fmt.Fprintln(w)
	fmt.Fprintf(w, "resolution:\n")
	fmt.Fprintf(w, "  external:       %d\n", d.Resolution.ResolvedExternal)
	fmt.Fprintf(w, "  unresolved:     %d\n", d.Resolution.Skipped)
	fmt.Fprintf(w, "  ambiguous:      %d\n", d.Resolution.Ambiguous)
	fmt.Fprintf(w, "  dropped intents: %d\n", d.Resolution.DroppedIntents)

	if len(d.Boundaries) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "boundaries:\n")
		for _, b := range d.Boundaries {
			if b.Count > 0 {
				fmt.Fprintf(w, "  %s (%d)\n", b.Code, b.Count)
			} else {
				fmt.Fprintf(w, "  %s\n", b.Code)
			}
		}
	}

	if d.Policy.ID != "" {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "policy:\n")
		// The canonical versioned id, not the bare name: what a reader sees
		// here is exactly what they pass back to --policy.
		fmt.Fprintf(w, "  %s: %s\n", d.Policy.ID, d.Policy.Verdict)
	}

	warnings := 0
	for _, f := range d.Findings {
		if f.Severity == "warning" || f.Severity == "error" {
			if warnings == 0 {
				fmt.Fprintln(w)
				fmt.Fprintf(w, "warnings:\n")
			}
			fmt.Fprintf(w, "  - %s\n", f.Message)
			warnings++
		}
	}
	for _, l := range d.Limitations {
		if (l.Severity == "warning" || l.Severity == "error") && l.Action != "" {
			if warnings == 0 {
				fmt.Fprintln(w)
				fmt.Fprintf(w, "warnings:\n")
			}
			fmt.Fprintf(w, "  - %s (%d): %s\n", l.Code, l.Count, l.Action)
			warnings++
		}
	}
}
