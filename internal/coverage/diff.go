package coverage

import (
	"fmt"
	"sort"
	"strings"
)

// Report is the drift-guard outcome: it compares the LIVE capability set against
// the checked-in coverage matrix and buckets every disagreement so CI can fail
// with a precise, human-readable diff. A Report with all buckets empty is a PASS.
type Report struct {
	// MissingFromMatrix lists live capabilities (code-derived categories) that
	// have no row in the matrix — the matrix omits something real.
	MissingFromMatrix []Capability
	// PhantomShipped lists matrix rows in a code-derived category marked
	// shipped/partial that are NOT live — the matrix claims something that does
	// not exist in code.
	PhantomShipped []Capability
	// MislabeledPlanned lists matrix rows marked planned that ARE actually live —
	// the matrix understates reality.
	MislabeledPlanned []Capability
}

// Pass reports whether the matrix matches reality (no drift).
func (r Report) Pass() bool {
	return len(r.MissingFromMatrix) == 0 &&
		len(r.PhantomShipped) == 0 &&
		len(r.MislabeledPlanned) == 0
}

// Check compares the live capability set against the loaded matrix and returns a
// bucketed Report. Only the four code-derived categories are cross-checked;
// informational rows (epics, feature-units, …) are ignored by the live diff.
// The check is symmetric: it catches omissions (live∉matrix), phantoms
// (matrix shipped/partial ∉ live), and mislabels (matrix planned ∈ live).
func Check(live LiveSet, matrix []Capability) Report {
	liveByKey := map[string]Capability{}
	for _, c := range live.IDs() {
		liveByKey[c.Key()] = c
	}
	matrixByKey := map[string]Capability{}
	for _, c := range matrix {
		matrixByKey[c.Key()] = c
	}

	var rep Report

	// 1. Every live capability must have a matrix row.
	for _, c := range live.IDs() {
		if _, ok := matrixByKey[c.Key()]; !ok {
			rep.MissingFromMatrix = append(rep.MissingFromMatrix, c)
		}
	}

	// 2. Every code-derived matrix row must be consistent with reality.
	for _, c := range matrix {
		if !codeDerivedCategories[c.Category] {
			continue // informational row — not machine-checked
		}
		_, isLive := liveByKey[c.Key()]
		switch {
		case c.IsLiveStatus() && !isLive:
			rep.PhantomShipped = append(rep.PhantomShipped, c)
		case c.Status == StatusPlanned && isLive:
			rep.MislabeledPlanned = append(rep.MislabeledPlanned, c)
		}
	}

	sortCapabilities(rep.MissingFromMatrix)
	sortCapabilities(rep.PhantomShipped)
	sortCapabilities(rep.MislabeledPlanned)
	return rep
}

// Format renders a human-readable, deterministic report suitable for CI logs.
func (r Report) Format() string {
	if r.Pass() {
		return "coverage-matrix check PASS — checked-in matrix matches the live capability set.\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "coverage-matrix check FAILED — the checked-in matrix has drifted from the live registries:\n")
	writeBucket(&b, "live capabilities MISSING from docs/coverage-matrix.yaml (add a row)", r.MissingFromMatrix,
		func(c Capability) string { return fmt.Sprintf("%s %q", c.Category, c.ID) })
	writeBucket(&b, "matrix rows marked shipped/partial but NOT live (PHANTOM — remove or mark planned)", r.PhantomShipped,
		func(c Capability) string { return fmt.Sprintf("%s %q (status %s)", c.Category, c.ID, c.Status) })
	writeBucket(&b, "matrix rows marked planned but actually LIVE (mislabeled — flip to shipped/partial)", r.MislabeledPlanned,
		func(c Capability) string { return fmt.Sprintf("%s %q", c.Category, c.ID) })
	b.WriteString("\nRegenerate with: go run ./cmd/coverage -generate   (then update statuses in docs/coverage-matrix.yaml)\n")
	return b.String()
}

func writeBucket(b *strings.Builder, title string, caps []Capability, render func(Capability) string) {
	if len(caps) == 0 {
		return
	}
	fmt.Fprintf(b, "\n  %s:\n", title)
	lines := make([]string, 0, len(caps))
	for _, c := range caps {
		lines = append(lines, "    - "+render(c))
	}
	sort.Strings(lines)
	b.WriteString(strings.Join(lines, "\n"))
	b.WriteByte('\n')
}
