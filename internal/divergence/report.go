package divergence

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

// Report is the merged content of every segment in one state directory. It is
// the raw reading; Assess turns it into the operator-facing verdict, because
// only the caller knows which operations are supposed to be on the seam.
type Report struct {
	// Directory is where the segments were read from.
	Directory string `json:"directory"`
	// Segments is how many segment files were read successfully.
	Segments int `json:"segments"`
	// Unreadable is how many files in the directory could not be parsed. They
	// are counted rather than skipped: a record that silently drops what it
	// cannot read under-reports divergence.
	Unreadable int `json:"unreadable_segments"`
	// Pruned is how many segments the writers have deleted between them to hold
	// the directory under the retention cap. Those segments' counts are NOT in
	// the totals below and cannot be recovered, so a non-zero value makes the
	// totals a lower bound — the same disclosure Unreadable earns, for the same
	// reason (see maxSegments for what pruning can and cannot take).
	Pruned int `json:"pruned_segments"`
	// Operations is the merged per-operation record, sorted by operation id.
	Operations []OperationRecord `json:"operations"`
}

// Read merges every segment under stateDir. A directory that does not exist is
// not an error — it is the honest "nothing has been observed" case, which
// Assess reports as UNKNOWN.
func Read(stateDir string) (Report, error) {
	dir := Dir(stateDir)
	rep := Report{Directory: dir}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return rep, nil
		}
		return rep, fmt.Errorf("divergence: read %s: %w", dir, err)
	}
	merged := map[string]*OperationRecord{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			rep.Unreadable++
			continue
		}
		var seg segment
		if err := json.Unmarshal(raw, &seg); err != nil || seg.Schema != Schema {
			rep.Unreadable++
			continue
		}
		rep.Segments++
		rep.Pruned += seg.Pruned
		for _, rec := range seg.Operations {
			mergeInto(merged, rec)
		}
	}
	rep.Operations = make([]OperationRecord, 0, len(merged))
	for _, rec := range merged {
		rep.Operations = append(rep.Operations, *rec)
	}
	sort.Slice(rep.Operations, func(i, j int) bool { return rep.Operations[i].Operation < rep.Operations[j].Operation })
	return rep, nil
}

// mergeInto sums one segment's row into the merged record.
func mergeInto(merged map[string]*OperationRecord, rec OperationRecord) {
	cur, ok := merged[rec.Operation]
	if !ok {
		copied := rec
		// The reason map is deep-copied so a later merge into this row cannot
		// write through into the segment value it came from.
		copied.SkipReasons = copySkipReasons(rec.SkipReasons)
		merged[rec.Operation] = &copied
		return
	}
	cur.Observations += rec.Observations
	cur.Mismatches += rec.Mismatches
	cur.Skipped += rec.Skipped
	for reason, n := range rec.SkipReasons {
		if cur.SkipReasons == nil {
			cur.SkipReasons = map[string]int{}
		}
		cur.SkipReasons[reason] += n
	}
	if rec.FirstSeen != nil && (cur.FirstSeen == nil || rec.FirstSeen.Before(*cur.FirstSeen)) {
		cur.FirstSeen = rec.FirstSeen
	}
	if rec.LastSeen != nil && (cur.LastSeen == nil || rec.LastSeen.After(*cur.LastSeen)) {
		cur.LastSeen = rec.LastSeen
	}
	if rec.LastMismatch != nil && (cur.LastMismatch == nil || rec.LastMismatch.Seen.After(cur.LastMismatch.Seen)) {
		cur.LastMismatch = rec.LastMismatch
	}
}

// copySkipReasons duplicates a reason map, or returns nil for an absent one so
// the JSON stays omitempty-clean.
func copySkipReasons(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// OperationState is one operation's verdict.
type OperationState string

const (
	// StateUnknown means NO dual-run observation was ever recorded for this
	// operation. It is not a statement that the two paths agree — see AC-3.
	StateUnknown OperationState = "UNKNOWN"
	// StateAgreed means the operation was observed and every observation found
	// the two paths identical.
	StateAgreed OperationState = "NO-DIVERGENCE-OBSERVED"
	// StateDiverged means at least one observation found them different.
	StateDiverged OperationState = "DIVERGED"
	// StatePartial is an overall-only verdict: some migrated operations were
	// observed and found identical, others were never observed at all. It
	// exists so a partial record cannot be read as a clean bill of health for
	// the whole seam.
	StatePartial OperationState = "PARTIAL-UNKNOWN"
)

// OperationView is one operation's record plus its verdict.
//
// Dispatches is derived, not stored: it is Observations + Skipped, i.e. every
// call that reached the shadow seam and was therefore a candidate for
// comparison. It is rendered beside Observations so a reader never has to
// subtract two numbers to discover that the coverage was partial (SW-245 AC-4).
type OperationView struct {
	OperationRecord
	State OperationState `json:"state"`
	// Dispatches is Observations + Skipped.
	Dispatches int `json:"dispatches"`
	// Coverage is the fraction of dispatches that were actually compared, in
	// [0,1]. It is 1 when nothing was skipped, and 0 when nothing was
	// dispatched at all — which is why the STATE, not this number, is what says
	// whether anything is known.
	Coverage float64 `json:"coverage"`
}

// Document is the operator-facing reading of the record: what the CLI renders
// and what the JSON form serializes.
type Document struct {
	Schema       string         `json:"schema"`
	State        OperationState `json:"state"`
	Directory    string         `json:"directory"`
	Segments     int            `json:"segments"`
	Unreadable   int            `json:"unreadable_segments"`
	Pruned       int            `json:"pruned_segments"`
	Observations int            `json:"observations"`
	Mismatches   int            `json:"mismatches"`
	// Dispatches is Observations + Skipped across every operation: how many
	// calls reached the shadow seam and were candidates for comparison.
	Dispatches int `json:"dispatches"`
	// Skipped is how many of those were NOT compared (SW-245 AC-4).
	Skipped int `json:"skipped"`
	// SkipReasons is the merged breakdown of Skipped by cause.
	SkipReasons map[string]int `json:"skip_reasons,omitempty"`
	// Coverage is Observations/Dispatches, or 1 when nothing was dispatched.
	Coverage   float64         `json:"coverage"`
	Operations []OperationView `json:"operations"`
}

// Assess joins a Report to the set of operations that are SUPPOSED to be on
// the seam, so an operation with no record at all is reported as UNKNOWN
// instead of being absent (an absent row reads as "fine" to a human, which is
// exactly the laundering AC-3 forbids).
//
// Operations found in the record but not in migrated are still reported: a
// record written by an older or newer binary is evidence too, and dropping it
// would hide a divergence.
func Assess(rep Report, migrated []string) Document {
	doc := Document{
		Schema:     Schema,
		Directory:  rep.Directory,
		Segments:   rep.Segments,
		Unreadable: rep.Unreadable,
		Pruned:     rep.Pruned,
	}
	byOp := map[string]OperationRecord{}
	for _, rec := range rep.Operations {
		byOp[rec.Operation] = rec
	}
	names := map[string]bool{}
	for _, op := range migrated {
		names[op] = true
	}
	for op := range byOp {
		names[op] = true
	}
	ordered := make([]string, 0, len(names))
	for op := range names {
		ordered = append(ordered, op)
	}
	sort.Strings(ordered)

	unknown, agreed, diverged := 0, 0, 0
	for _, op := range ordered {
		rec, ok := byOp[op]
		if !ok {
			rec = OperationRecord{Operation: op}
		}
		view := OperationView{OperationRecord: rec}
		view.Dispatches = rec.Observations + rec.Skipped
		view.Coverage = coverageOf(rec.Observations, view.Dispatches)
		switch {
		case rec.Mismatches > 0:
			view.State = StateDiverged
			diverged++
		case rec.Observations > 0:
			view.State = StateAgreed
			agreed++
		default:
			view.State = StateUnknown
			unknown++
		}
		doc.Observations += rec.Observations
		doc.Mismatches += rec.Mismatches
		doc.Skipped += rec.Skipped
		for reason, n := range rec.SkipReasons {
			if doc.SkipReasons == nil {
				doc.SkipReasons = map[string]int{}
			}
			doc.SkipReasons[reason] += n
		}
		doc.Operations = append(doc.Operations, view)
	}
	doc.Dispatches = doc.Observations + doc.Skipped
	doc.Coverage = coverageOf(doc.Observations, doc.Dispatches)
	switch {
	case diverged > 0:
		doc.State = StateDiverged
	case unknown > 0 && (agreed > 0 || rep.Segments > 0):
		doc.State = StatePartial
	case unknown > 0:
		doc.State = StateUnknown
	default:
		doc.State = StateAgreed
	}
	if doc.Observations == 0 {
		// Nothing was ever observed anywhere: the only honest overall verdict.
		doc.State = StateUnknown
	}
	return doc
}

// coverageOf is observations/dispatches with the empty case pinned.
//
// Nothing dispatched reads 1, not 0: coverage answers "of what reached the
// seam, how much was compared", and 0/0 is vacuously complete. That an
// operation is UNKNOWN because it was never called is the STATE's job to say,
// and saying it twice — once as a state and once as a 0 % coverage that means
// something else entirely — is how a reader learns to distrust both.
func coverageOf(observations, dispatches int) float64 {
	if dispatches <= 0 {
		return 1
	}
	return float64(observations) / float64(dispatches)
}

// RenderJSON writes the machine-readable form: one document, schema-tagged,
// stable field order, newline-terminated.
func RenderJSON(w io.Writer, doc Document) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("divergence: render json: %w", err)
	}
	return nil
}

// RenderHuman writes the operator-readable form. Every line that could be
// mistaken for a clean bill of health is qualified in place: UNKNOWN says what
// it means, and the footer repeats it, because the reader who most needs that
// sentence is the one skimming the table.
func RenderHuman(w io.Writer, doc Document) error {
	if _, err := fmt.Fprintf(w, "executor-seam divergence record (%s)\n", doc.Schema); err != nil {
		return err
	}
	fmt.Fprintf(w, "  state:      %s — %s\n", doc.State, stateProse(doc.State))
	fmt.Fprintf(w, "  directory:  %s\n", doc.Directory)
	fmt.Fprintf(w, "  segments:   %d recorded, %d unreadable, %d pruned\n", doc.Segments, doc.Unreadable, doc.Pruned)
	fmt.Fprintf(w, "  totals:     %d observation(s), %d mismatch(es)\n", doc.Observations, doc.Mismatches)
	fmt.Fprintf(w, "  coverage:   %s\n\n", coverageLine(doc.Observations, doc.Dispatches, doc.Skipped, doc.SkipReasons))

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "OPERATION\tDISPATCHES\tOBSERVATIONS\tSKIPPED\tMISMATCHES\tSTATE\tFIRST SEEN\tLAST SEEN")
	for _, op := range doc.Operations {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%s\t%s\t%s\n",
			op.Operation, op.Dispatches, op.Observations, op.Skipped, op.Mismatches, op.State,
			stamp(op.FirstSeen), stamp(op.LastSeen))
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("divergence: render human: %w", err)
	}
	for _, op := range doc.Operations {
		if op.LastMismatch == nil {
			continue
		}
		fmt.Fprintf(w, "\nlast divergence on %s (%s, %s):\n  legacy:   %s\n  executor: %s\n",
			op.Operation, op.LastMismatch.Kind, stamp(&op.LastMismatch.Seen),
			op.LastMismatch.Legacy, op.LastMismatch.Executor)
	}
	if doc.Unreadable > 0 {
		fmt.Fprintf(w, "\n%d segment file(s) in the directory are unreadable and are NOT counted above;\n"+
			"the totals are therefore a lower bound.\n", doc.Unreadable)
	}
	if doc.Skipped > 0 {
		fmt.Fprintf(w, "\n%d dispatch(es) reached the seam and were NOT compared (%s), so the\n"+
			"observation counts above cover %s of what the seam saw. A call that was not\n"+
			"compared is NOT evidence that the two paths agree — it is a coverage gap, and it\n"+
			"is counted here rather than left to be inferred from a number that looks whole.\n"+
			"See docs/executor-seam-rollback.md §5.\n",
			doc.Skipped, renderSkipReasons(doc.SkipReasons), coveragePercent(doc.Coverage))
	}
	if doc.Pruned > 0 {
		fmt.Fprintf(w, "\n%d segment file(s) have been pruned to hold the directory under its retention\n"+
			"cap; their observations are NOT counted above and cannot be recovered, so the totals\n"+
			"are a lower bound. Pruning is by age alone — a still-running but quiet writer's segment\n"+
			"can be among them. See docs/executor-seam-rollback.md.\n", doc.Pruned)
	}
	// SW-244 moved the shipped position from `legacy` to `shadow`, and this
	// footer had to move with it: the old wording explained an all-UNKNOWN
	// record as the expected state of a normal install, which would now
	// mis-explain a real coverage gap as normality. The honesty rule it exists
	// to enforce is unchanged — UNKNOWN is the absence of evidence and never a
	// statement of agreement — so only the second sentence, the one that says
	// WHY an operation might be unobserved, is different.
	fmt.Fprintf(w, "\nUNKNOWN means no dual-run observation was recorded for that operation. It is NOT\n"+
		"a statement that the two paths agree. The seam only observes in `shadow`, which is\n"+
		"the shipped position, so an operation still reading UNKNOWN has not been called on\n"+
		"this install — or the seam was rolled back. See docs/executor-seam-rollback.md.\n")
	return nil
}

// coverageLine is the header's one-line coverage statement (SW-245 AC-4).
//
// It is printed on EVERY document, not only when something was skipped. A line
// that appears only on the bad case teaches a reader that its absence means
// nothing was measured, when in fact the absence would mean full coverage — so
// the good case says so out loud and the bad case is a different sentence in the
// same place, not a new one somewhere else.
func coverageLine(observations, dispatches, skipped int, reasons map[string]int) string {
	if dispatches == 0 {
		return "no dispatch has reached the seam in this record"
	}
	if skipped == 0 {
		return fmt.Sprintf("%d of %d dispatch(es) compared (100%%) — no sampling, nothing dropped",
			observations, dispatches)
	}
	return fmt.Sprintf("%d of %d dispatch(es) compared (%s) — %d NOT compared (%s)",
		observations, dispatches, coveragePercent(coverageOf(observations, dispatches)),
		skipped, renderSkipReasons(reasons))
}

// coveragePercent renders a fraction the way an operator reads one, and never
// rounds a partial coverage up to 100 %: 9 999 of 10 000 prints 99.9 %, and
// anything short of whole that would round to 100 prints "<100%".
func coveragePercent(fraction float64) string {
	switch {
	case fraction >= 1:
		return "100%"
	case fraction*100 >= 99.95:
		return "<100%"
	default:
		return fmt.Sprintf("%.1f%%", fraction*100)
	}
}

// renderSkipReasons renders the reason breakdown in a stable order.
func renderSkipReasons(reasons map[string]int) string {
	if len(reasons) == 0 {
		return "reason not recorded"
	}
	keys := make([]string, 0, len(reasons))
	for k := range reasons {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, reasons[k]))
	}
	return strings.Join(parts, ", ")
}

// stateProse is the one-line meaning of a verdict, kept beside the constants so
// the wording cannot drift from the state it explains.
func stateProse(s OperationState) string {
	switch s {
	case StateUnknown:
		return "no dual-run observation has been recorded"
	case StateAgreed:
		return "every migrated operation was observed and none diverged"
	case StatePartial:
		return "some migrated operations have never been observed"
	case StateDiverged:
		return "at least one observation found the two paths different"
	default:
		return "unrecognised state"
	}
}

// stamp renders an optional timestamp; an absent one is a dash, never a zero
// time dressed up as a real one.
func stamp(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}
