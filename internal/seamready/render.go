package seamready

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RuleOfThree is the sentence printed beside a set K (AC-4): with K clean
// observations, the residual divergence rate is bounded at about 3/K with 95%
// confidence, so lowering K is legible as widening that bound.
func RuleOfThree(k int) string {
	return fmt.Sprintf("rule of three: residual divergence rate <= 3/%d = %.1f%% at 95%% confidence", k, 300.0/float64(k))
}

func (a Assessment) counts() (ready, notReady, unknown int) {
	for _, o := range a.Operations {
		switch o.Verdict {
		case VerdictReady:
			ready++
		case VerdictNotReady:
			notReady++
		default:
			unknown++
		}
	}
	return
}

// Text renders the human form: a header with K and the record, then per
// operation one verdict line `<op>  <VERDICT>` and six criterion rows.
func (a Assessment) Text() string {
	var b strings.Builder
	b.WriteString("graphi executor-seam cutover readiness (SW-254, AX-14)\n\n")
	if a.K == nil {
		b.WriteString("K unset — owner decision 1 not taken (stories/SW-238/preconditions.md); c2 reads UNKNOWN for every operation.\n")
	} else {
		fmt.Fprintf(&b, "K = %d observation(s) per operation (%s).\n", *a.K, RuleOfThree(*a.K))
	}
	if a.Record.Error != "" {
		fmt.Fprintf(&b, "divergence record: UNREADABLE — %s\n", a.Record.Error)
	} else {
		fmt.Fprintf(&b, "divergence record: %s — %d observation(s), %d mismatch(es), %d skipped",
			a.Record.State, a.Record.Observations, a.Record.Mismatches, a.Record.Skipped)
		if a.Record.Unreadable > 0 || a.Record.Pruned > 0 {
			fmt.Fprintf(&b, ", %d unreadable segment(s), %d pruned (totals are a lower bound)", a.Record.Unreadable, a.Record.Pruned)
		}
		if a.Record.Directory != "" {
			fmt.Fprintf(&b, " (%s)", a.Record.Directory)
		}
		b.WriteString("\n")
	}
	nameWidth := 0
	for _, c := range Criteria {
		if len(c.Name) > nameWidth {
			nameWidth = len(c.Name)
		}
	}
	for _, o := range a.Operations {
		fmt.Fprintf(&b, "\n%s  %s\n", o.Operation, o.Verdict)
		for _, r := range o.Criteria {
			fmt.Fprintf(&b, "  %s  %-*s  %-7s", r.ID, nameWidth, r.Name, r.State)
			if r.Evidence != "" {
				fmt.Fprintf(&b, "  %s", r.Evidence)
			}
			if r.Reason != "" {
				fmt.Fprintf(&b, "  — %s", r.Reason)
			}
			b.WriteString("\n")
		}
	}
	ready, notReady, unknown := a.counts()
	fmt.Fprintf(&b, "\nSUMMARY  %d READY, %d NOT_READY, %d UNKNOWN of %d operation(s) on the seam.\n",
		ready, notReady, unknown, len(a.Operations))
	if ready == 0 {
		b.WriteString("  No operation qualifies for `active`. READY is the precondition for a flip story;\n" +
			"  UNKNOWN is not \"not yet\" — it is the absence of an artifact, and it never counts as PASS.\n")
	} else {
		b.WriteString("  A READY operation may be flipped by its own story, never by this tool (docs/executor-seam-rollback.md §Readiness).\n")
	}
	return b.String()
}

// jsonDocument is the AC-1 JSON shape.
type jsonDocument struct {
	Schema      string                `json:"schema"`
	K           *int                  `json:"k"`
	RuleOfThree string                `json:"rule_of_three,omitempty"`
	Record      RecordSummary         `json:"record"`
	Operations  []OperationAssessment `json:"operations"`
}

// JSON renders the machine form:
//
//	{schema: "seam-readiness-v1", k, record, operations: [{operation, verdict,
//	 criteria: [{id, name, state: PASS|FAIL|UNKNOWN, evidence, reason}]}]}
func (a Assessment) JSON() ([]byte, error) {
	doc := jsonDocument{
		Schema:     SchemaV1,
		K:          a.K,
		Record:     a.Record,
		Operations: a.Operations,
	}
	if a.K != nil {
		doc.RuleOfThree = RuleOfThree(*a.K)
	}
	if doc.Operations == nil {
		doc.Operations = []OperationAssessment{}
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
