// Package seamreach is the SW-248 (AC-5) reachability gate for graphi's
// executor seam.
//
// # The question nothing asked
//
// SW-228 migrated ten operations onto the seam, correctly choosing Labs ones
// because the Stable-12 are frozen contract. SW-244 made `shadow` the shipped
// position, correctly, because the divergence record had become readable. Both
// reviews passed. Nobody asked whether a client bound to the profile that
// actually ships could reach any of the ten — and it cannot: they are Labs, the
// default MCP profile advertises the eleven Stable tools, and so a released
// binary dual-runs ten operations that a stock client can never call. The gap
// was between two correct stories, which is exactly the kind nothing catches
// until it is found on a released binary.
//
// This package is the question, asked mechanically, at merge time.
//
// # The two rules, and why they are two
//
//	INVARIANT   Every operation NOT in `legacy` mode must be reachable through
//	            at least one shipped profile. An operation dual-running with no
//	            surface that can call it is a migration with no way to exercise
//	            it: it pays the comparison's cost forever and can never produce
//	            the evidence that cost buys.
//
//	DECLARATION The live matrix must equal the checked-in one. The invariant
//	            alone would NOT have caught the defect that motivated this
//	            package, and pretending otherwise would be the second false
//	            green in the same area: `graphi mcp -labs` reaches all ten, so
//	            the invariant holds today and held on the day the defect
//	            shipped. What was missing was VISIBILITY — a reviewer of the
//	            SW-244 diff seeing the summary line flip to "0 reachable
//	            through `graphi mcp`". A golden the change has to update puts
//	            that sentence in the diff.
//
// Two rules because they fail for different reasons and want different fixes:
// the invariant is a defect, the declaration is a change that needs a human to
// look at it.
//
// # What is judged
//
// The COMPILED-IN position, not the operator's. Check never applies the
// GRAPHI_CANARY_* environment, so a runner with a variable set judges the same
// binary as a runner without one — a gate on what ships must not be silenceable
// by an export.
package seamreach

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/mcp"
)

// declaration is the checked-in reachability matrix. Regenerate it with
// `go run ./cmd/seamreach -generate`; a diff to it is the reviewable record of
// a change to what the seam runs or to what a profile advertises.
//
//go:embed reachability.txt
var declaration string

// Declaration returns the checked-in matrix.
func Declaration() string { return declaration }

// Operation is one operation on the executor seam.
type Operation struct {
	// ID is the operation id, which is also its MCP tool name.
	ID string
	// Mode is the compiled-in kill-switch position: legacy, shadow or active.
	Mode string
}

// Profile is one shipped surface binding and the seam operations it reaches.
type Profile struct {
	ID         string
	Invocation string
	Default    bool
	Reaches    map[string]bool
}

// Seam is the gate's whole input: what is on the seam, and what can reach it.
// It is a value so the gate can be run against a hypothetical — which is how
// the gate proves it FAILS, the only way to know that a green run means
// anything.
type Seam struct {
	Operations []Operation
	Profiles   []Profile
}

// Live builds the Seam from the binary being checked: surfaces/client owns what
// is migrated and in which position, surfaces/mcp owns what each shipped
// profile advertises. Neither list is restated here — a copy would be a second
// source of truth for the exact property the gate exists to check.
func Live() Seam {
	migrated := client.MigratedOperations()
	seam := Seam{Operations: make([]Operation, 0, len(migrated))}
	for _, op := range migrated {
		seam.Operations = append(seam.Operations, Operation{
			ID:   op,
			Mode: string(client.CanaryModeFor(op)),
		})
	}
	for _, p := range mcp.ShippedProfiles() {
		reaches := map[string]bool{}
		for _, op := range migrated {
			if p.Reaches(op) {
				reaches[op] = true
			}
		}
		seam.Profiles = append(seam.Profiles, Profile{
			ID: p.ID, Invocation: p.Invocation, Default: p.Default, Reaches: reaches,
		})
	}
	return seam
}

// WithUnreachable returns a copy of the seam with one extra operation in
// `shadow` that no profile reaches.
//
// It exists for the gate's own proof. A check nobody has watched fail is a
// claim, so `go run ./cmd/seamreach -inject-unreachable <op>` introduces
// exactly the violation the invariant is written for and the gate must exit
// non-zero on it. It mutates nothing outside the returned value.
func (s Seam) WithUnreachable(id string) Seam {
	out := Seam{
		Operations: append(append([]Operation(nil), s.Operations...), Operation{ID: id, Mode: "shadow"}),
		Profiles:   s.Profiles,
	}
	return out
}

// Row is one operation's answer.
type Row struct {
	Operation   string
	Mode        string
	InDefault   bool
	Invocations []string
}

// Report is the gate's outcome.
type Report struct {
	Rows []Row
	// DefaultProfile is the invocation of the profile a stock install binds.
	DefaultProfile string
	// DualRun is how many operations are not in `legacy`; ReachableInDefault
	// how many of those the default profile reaches.
	DualRun, ReachableInDefault int
	// Unreachable names the dual-running operations no shipped profile reaches
	// — the invariant's violations.
	Unreachable []string
	// Profiles summarises each shipped profile's coverage of the seam.
	Profiles []ProfileSummary
}

// ProfileSummary is one profile's line in the matrix header.
type ProfileSummary struct {
	ID         string
	Invocation string
	Default    bool
	Reaches    int
}

// Check evaluates the seam. It computes and never enforces: the exit policy
// lives in cmd/seamreach, so the report can be rendered by a test that expects
// a violation without that test having to catch an os.Exit.
func Check(s Seam) Report {
	rep := Report{}
	byID := map[string]Profile{}
	for _, p := range s.Profiles {
		if p.Default {
			rep.DefaultProfile = p.Invocation
		}
		byID[p.ID] = p
		rep.Profiles = append(rep.Profiles, ProfileSummary{
			ID: p.ID, Invocation: p.Invocation, Default: p.Default, Reaches: len(p.Reaches),
		})
	}
	ops := append([]Operation(nil), s.Operations...)
	sort.Slice(ops, func(i, j int) bool { return ops[i].ID < ops[j].ID })
	for _, op := range ops {
		row := Row{Operation: op.ID, Mode: op.Mode}
		for _, p := range s.Profiles {
			if !p.Reaches[op.ID] {
				continue
			}
			row.Invocations = append(row.Invocations, p.Invocation)
			if p.Default {
				row.InDefault = true
			}
		}
		if op.Mode != "legacy" {
			rep.DualRun++
			if row.InDefault {
				rep.ReachableInDefault++
			}
			if len(row.Invocations) == 0 {
				rep.Unreachable = append(rep.Unreachable, op.ID)
			}
		}
		rep.Rows = append(rep.Rows, row)
	}
	return rep
}

// Pass reports whether the INVARIANT holds. The declaration comparison is a
// separate verdict and is made by the caller, because the two failures want
// different words: one is a defect, the other is a change to look at.
func (r Report) Pass() bool { return len(r.Unreachable) == 0 }

// Matrix renders the deterministic, diffable form — the thing the declaration
// file holds and `-check` compares against.
//
// The summary line is last and is the point of the whole file: it is the
// sentence that would have appeared, changed, in the SW-244 diff.
func (r Report) Matrix() string {
	var b strings.Builder
	b.WriteString("graphi executor-seam reachability\n")
	b.WriteString("\n")
	b.WriteString("Generated by `go run ./cmd/seamreach -generate`; checked by `-check` (SW-248 AC-5).\n")
	b.WriteString("A change here means the seam's population, its shipped positions, or what a\n")
	b.WriteString("profile advertises has moved. Read the summary line before approving it.\n")
	b.WriteString("\n")
	b.WriteString("PROFILES\n")
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  ID\tINVOCATION\tDEFAULT\tREACHES")
	for _, p := range r.Profiles {
		def := "no"
		if p.Default {
			def = "yes"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%d of %d seam operation(s)\n",
			p.ID, p.Invocation, def, p.Reaches, len(r.Rows))
	}
	_ = tw.Flush()
	b.WriteString("\nOPERATIONS\n")
	tw = tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  OPERATION\tMODE\tIN DEFAULT PROFILE\tREACHABLE VIA")
	for _, row := range r.Rows {
		in := "no"
		if row.InDefault {
			in = "yes"
		}
		via := "NONE"
		if len(row.Invocations) > 0 {
			via = strings.Join(row.Invocations, " | ")
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", row.Operation, row.Mode, in, via)
	}
	_ = tw.Flush()
	fmt.Fprintf(&b, "\nSUMMARY\n"+
		"  %d operation(s) on the seam, %d dual-running (shadow or active).\n"+
		"  %d of the %d dual-running operation(s) are reachable through `%s`,\n"+
		"  the profile a stock install binds.\n",
		len(r.Rows), r.DualRun, r.ReachableInDefault, r.DualRun, r.DefaultProfile)
	if r.DualRun > 0 && r.ReachableInDefault == 0 {
		b.WriteString("  So a client bound to the default profile records NO dual-run evidence at all:\n" +
			"  the divergence record's emptiness is a fact about the profile, not about the\n" +
			"  two paths. `graphi doctor` says so (SW-248); this file is where it becomes\n" +
			"  visible in a diff.\n")
	}
	return b.String()
}

// Format renders the gate's console output: the matrix, then the verdict.
func (r Report) Format(declared string) string {
	var b strings.Builder
	b.WriteString(r.Matrix())
	b.WriteString("\n")
	if !r.Pass() {
		fmt.Fprintf(&b, "seam-reachability check FAILED — %d operation(s) dual-run on the executor seam "+
			"with NO shipped profile that can reach them:\n", len(r.Unreachable))
		for _, op := range r.Unreachable {
			fmt.Fprintf(&b, "  %s\n", op)
		}
		b.WriteString("An operation on the seam that nothing can call pays the dual run's cost forever\n" +
			"and can never produce the evidence that cost buys. Either add it to a shipped\n" +
			"profile or take it off the seam (GRAPHI_CANARY_<OP> is a runtime switch, not a fix).\n")
		return b.String()
	}
	if declared != "" && declared != r.Matrix() {
		b.WriteString("seam-reachability check FAILED — the live matrix differs from the checked-in\n" +
			"declaration (internal/seamreach/reachability.txt). Something changed what the seam\n" +
			"runs or what a profile advertises. Review the difference, then regenerate it with\n" +
			"`go run ./cmd/seamreach -generate` and commit it with the change that caused it.\n")
		return b.String()
	}
	b.WriteString("seam-reachability check PASS — every dual-running operation is reachable through\n" +
		"at least one shipped profile, and the live matrix matches the declaration.\n")
	return b.String()
}

// MatchesDeclaration reports whether the live matrix equals the checked-in one.
func (r Report) MatchesDeclaration(declared string) bool { return r.Matrix() == declared }
