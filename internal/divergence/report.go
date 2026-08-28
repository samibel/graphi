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
	// StateUnobservable is an overall-only verdict and the SW-248 addition: the
	// record is empty AND no operation on the seam is reachable through the
	// default shipped profile, so nothing a client bound to it does can ever
	// fill the record.
	//
	// It replaces StateUnknown in exactly that case rather than sitting beside
	// it, because the two are read for the same purpose and only one of them is
	// true at a time. An empty record that COULD fill is waiting for a call; an
	// empty record that CANNOT fill is waiting for nothing, and reporting both
	// as UNKNOWN was the defect this story closes.
	StateUnobservable OperationState = "UNKNOWN-AND-UNOBSERVABLE"
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
	// Reach is the SW-248 second axis: whether a client could reach this
	// operation at all. It is orthogonal to State — an UNKNOWN row means
	// something different depending on it, which is the whole point.
	Reach ReachState `json:"reach"`
	// ReachedBy lists the invocations of the shipped profiles that can reach
	// the operation, in profile order. Empty for ReachNone and for
	// ReachUnevaluated.
	ReachedBy []string `json:"reached_by,omitempty"`
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
	Coverage float64 `json:"coverage"`
	// Profiles is the shipped surface-profile picture the reachability axis was
	// computed from, echoed into the document so a reader (or a CI leg) can
	// check the conclusion against its inputs instead of trusting it.
	Profiles []Profile `json:"profiles,omitempty"`
	// ReachEvaluated reports whether a profile picture was supplied at all.
	// False means every row reads REACHABILITY-NOT-EVALUATED, which is an
	// absence of information and never an assertion of reachability.
	ReachEvaluated bool `json:"reach_evaluated"`
	// DefaultProfile is the invocation of the reference profile — the one a
	// stock install binds. The reachability sentences are all about THIS
	// profile, and naming it is what keeps them from being a claim about the
	// reader's unknown configuration.
	DefaultProfile string `json:"default_profile,omitempty"`
	// ReachableInDefault is how many of the operations below the default
	// profile reaches; Unobservable names the ones it does not, and
	// Unreachable the ones no shipped profile reaches at all.
	ReachableInDefault int             `json:"reachable_in_default"`
	Unobservable       []string        `json:"unobservable_in_default,omitempty"`
	Unreachable        []string        `json:"unreachable_anywhere,omitempty"`
	Operations         []OperationView `json:"operations"`
}

// Assess joins a Report to the set of operations that are SUPPOSED to be on
// the seam, so an operation with no record at all is reported as UNKNOWN
// instead of being absent (an absent row reads as "fine" to a human, which is
// exactly the laundering AC-3 forbids).
//
// Operations found in the record but not in migrated are still reported: a
// record written by an older or newer binary is evidence too, and dropping it
// would hide a divergence.
//
// SW-248 adds the profiles argument: the shipped surface bindings a client can
// be pointed at, so the document can say whether an UNKNOWN row is waiting for
// a call or waiting for nothing. It is a REQUIRED parameter and not an optional
// enrichment because a caller that has no picture must say so at the call site;
// passing nil yields REACHABILITY-NOT-EVALUATED on every row, which is an
// absence of information and is rendered as one.
func Assess(rep Report, migrated []string, profiles []Profile) Document {
	reach := newReachIndex(profiles)
	doc := Document{
		Schema:         Schema,
		Directory:      rep.Directory,
		Segments:       rep.Segments,
		Unreadable:     rep.Unreadable,
		Pruned:         rep.Pruned,
		Profiles:       sortedProfiles(profiles),
		ReachEvaluated: reach.evaluated,
		DefaultProfile: reach.defaultInvocation,
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
		view.Reach, view.ReachedBy = reach.stateOf(op)
		switch view.Reach {
		case ReachDefault:
			doc.ReachableInDefault++
		case ReachOptIn:
			doc.Unobservable = append(doc.Unobservable, op)
		case ReachNone:
			doc.Unobservable = append(doc.Unobservable, op)
			doc.Unreachable = append(doc.Unreachable, op)
		}
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
	// SW-248 AC-4. An empty record has two causes and they are not the same
	// finding. If the default shipped profile reaches NOTHING on the seam, the
	// record is not merely empty — it cannot fill for a client bound to that
	// profile, and reporting "no dual-run observation has been recorded" would
	// invite the reader to wait for one that will never arrive.
	//
	// The condition is deliberately narrow. It needs a profile picture (an
	// unevaluated document asserts nothing), it needs the record to be empty
	// (an observation from an opt-in profile is real evidence and outranks a
	// statement about the default), and it needs at least one operation on the
	// seam (an empty seam is a different, already-handled case).
	if reach.evaluated && doc.Observations == 0 && len(doc.Operations) > 0 && doc.ReachableInDefault == 0 {
		doc.State = StateUnobservable
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
	fmt.Fprintf(w, "  state:      %s — %s\n", doc.State, stateProse(doc.State, doc.DefaultProfile))
	fmt.Fprintf(w, "  directory:  %s\n", doc.Directory)
	fmt.Fprintf(w, "  segments:   %d recorded, %d unreadable, %d pruned\n", doc.Segments, doc.Unreadable, doc.Pruned)
	fmt.Fprintf(w, "  totals:     %d observation(s), %d mismatch(es)\n", doc.Observations, doc.Mismatches)
	fmt.Fprintf(w, "  coverage:   %s\n", coverageLine(doc.Observations, doc.Dispatches, doc.Skipped, doc.SkipReasons))
	fmt.Fprintf(w, "  reachable:  %s\n\n", reachLine(doc))

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "OPERATION\tDISPATCHES\tOBSERVATIONS\tSKIPPED\tMISMATCHES\tSTATE\tREACHABLE VIA\tFIRST SEEN\tLAST SEEN")
	for _, op := range doc.Operations {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%s\t%s\t%s\t%s\n",
			op.Operation, op.Dispatches, op.Observations, op.Skipped, op.Mismatches, op.State,
			reachColumn(op), stamp(op.FirstSeen), stamp(op.LastSeen))
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
	//
	// SW-248 splits the second sentence again, because "has not been called on
	// this install" was itself a half-truth on the shipped binary: for an
	// operation the bound profile does not advertise, it cannot BE called, and
	// telling a reader it merely has not been is how "not possible" gets filed
	// as "not yet". The rule is unchanged a second time; only the explanation
	// forks, and it forks per reachability rather than for the whole document.
	fmt.Fprintf(w, "\nUNKNOWN means no dual-run observation was recorded for that operation. It is NOT\n"+
		"a statement that the two paths agree. The seam only observes in `shadow`, which is\n"+
		"the shipped position. WHY an operation is UNKNOWN depends on whether anything can\n"+
		"reach it, so the reasons are separated below rather than left to be assumed.\n")
	writeReachFooter(w, doc)
	fmt.Fprint(w, "See docs/executor-seam-rollback.md.\n")
	return nil
}

// reachLine is the header's one-line reachability statement, the counterpart to
// coverageLine and printed for the same reason: on EVERY document, so its
// absence can never be read as an answer.
func reachLine(doc Document) string {
	if !doc.ReachEvaluated {
		return "not evaluated — this document says NOTHING about whether the seam can be reached"
	}
	total := len(doc.Operations)
	profile := quoteProfile(doc.DefaultProfile)
	switch {
	case total == 0:
		return "no operation is on the seam"
	case doc.ReachableInDefault == 0:
		return fmt.Sprintf("NONE of the %d operation(s) on the seam is reachable through %s "+
			"(the profile a stock install binds)", total, profile)
	case doc.ReachableInDefault == total:
		return fmt.Sprintf("all %d operation(s) on the seam are reachable through %s", total, profile)
	}
	return fmt.Sprintf("%d of %d operation(s) on the seam are reachable through %s; %d are not",
		doc.ReachableInDefault, total, profile, total-doc.ReachableInDefault)
}

// writeReachFooter separates the reasons an operation is UNKNOWN. Three
// conditions that a single "never observed" line rendered identically get three
// paragraphs here, and they are worded so that reading one tells you which of
// the three you are in without comparing it to the others.
func writeReachFooter(w io.Writer, doc Document) {
	if !doc.ReachEvaluated {
		fmt.Fprintf(w, "\nReachability was NOT evaluated for this document, so an UNKNOWN row below cannot\n"+
			"be told apart from an operation nothing could have called. Treat every UNKNOWN as\n"+
			"unexplained rather than as \"not yet\".\n")
		return
	}
	var waiting, optIn, nowhere []string
	for _, op := range doc.Operations {
		if op.State != StateUnknown {
			continue
		}
		switch op.Reach {
		case ReachDefault:
			waiting = append(waiting, op.Operation)
		case ReachOptIn:
			optIn = append(optIn, op.Operation)
		case ReachNone:
			nowhere = append(nowhere, op.Operation)
		}
	}
	profile := quoteProfile(doc.DefaultProfile)
	if len(waiting) > 0 {
		fmt.Fprintf(w, "\nNOT YET OBSERVED, but reachable: %s.\n"+
			"  %s advertises these, so a call through a stock install reaches the seam and,\n"+
			"  on the shipped `shadow` position, records an observation. Their UNKNOWN means\n"+
			"  \"not yet\" and a call will change it.\n",
			strings.Join(waiting, ", "), profile)
	}
	if len(optIn) > 0 {
		fmt.Fprintf(w, "\nNOT OBSERVABLE through %s: %s.\n"+
			"  %s does not advertise these, so a client bound to it cannot call them and no\n"+
			"  amount of use will record an observation. Their UNKNOWN means \"not possible\n"+
			"  here\", NOT \"not yet\" — and it is still not a statement that the two paths\n"+
			"  agree. Reaching them needs a profile that advertises them; the REACHABLE VIA\n"+
			"  column above names it per operation.\n",
			profile, strings.Join(optIn, ", "), profile)
	}
	if len(nowhere) > 0 {
		fmt.Fprintf(w, "\nNOT REACHABLE THROUGH ANY SHIPPED PROFILE: %s.\n"+
			"  No profile this binary can be started in advertises these, so nothing can ever\n"+
			"  observe them. That is a defect in the migration — an operation was put on the\n"+
			"  seam with no way to exercise it — and not a state an operator can act on.\n",
			strings.Join(nowhere, ", "))
	}
	if doc.State == StateUnobservable {
		fmt.Fprintf(w, "\nTHIS RECORD CANNOT FILL in %s: not one of the %d operation(s) on the seam is\n"+
			"reachable there. Its emptiness is therefore evidence about the PROFILE, not about\n"+
			"the two paths, and waiting longer will not change it.\n",
			profile, len(doc.Operations))
	}
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
func stateProse(s OperationState, defaultProfile string) string {
	switch s {
	case StateUnknown:
		return "no dual-run observation has been recorded"
	case StateAgreed:
		return "every migrated operation was observed and none diverged"
	case StatePartial:
		return "some migrated operations have never been observed"
	case StateDiverged:
		return "at least one observation found the two paths different"
	case StateUnobservable:
		return "no dual-run observation has been recorded AND none can be: " +
			quoteProfile(defaultProfile) + " reaches nothing on the seam"
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
