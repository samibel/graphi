package skillgen

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
)

// Generator produces deterministic skill artifacts.
type Generator struct {
	ledger Ledger
}

// NewGenerator returns a generator. If ledger is nil, a no-op ledger is used.
func NewGenerator(ledger Ledger) *Generator {
	if ledger == nil {
		ledger = noopLedger{}
	}
	return &Generator{ledger: ledger}
}

// Generate converts a procedure into a deterministic Markdown skill artifact.
func (g *Generator) Generate(ctx context.Context, p Procedure) (Skill, []byte, error) {
	steps := normalizeSteps(p.Steps)
	inputs := dedupeSort(p.Inputs)
	outputs := dedupeSort(p.Outputs)

	skill := Skill{
		Name:        strings.TrimSpace(p.Name),
		Trigger:     strings.TrimSpace(p.Trigger),
		Description: strings.TrimSpace(p.Description),
		Inputs:      inputs,
		Outputs:     outputs,
		Steps:       steps,
	}

	md, err := marshalSkill(skill)
	if err != nil {
		return skill, nil, err
	}
	if err := g.ledger.RecordSkillGen(ctx, int64(len(steps))*500); err != nil {
		return skill, md, fmt.Errorf("skillgen: ledger: %w", err)
	}
	return skill, md, nil
}

func normalizeSteps(steps []Step) []Step {
	out := make([]Step, 0, len(steps))
	seen := make(map[string]struct{})
	for _, s := range steps {
		name := strings.TrimSpace(s.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, Step{
			Name:        name,
			Action:      strings.TrimSpace(s.Action),
			Description: strings.TrimSpace(s.Description),
			Inputs:      dedupeSort(s.Inputs),
			Outputs:     dedupeSort(s.Outputs),
			Guard:       strings.TrimSpace(s.Guard),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func dedupeSort(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func marshalSkill(s Skill) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %q\n", s.Name)
	fmt.Fprintf(&b, "trigger: %q\n", s.Trigger)
	fmt.Fprintf(&b, "description: %q\n", s.Description)
	writeStringList(&b, "inputs", s.Inputs)
	writeStringList(&b, "outputs", s.Outputs)
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %s\n\n", s.Name)
	fmt.Fprintf(&b, "%s\n\n", s.Description)
	b.WriteString("## Inputs\n\n")
	if len(s.Inputs) == 0 {
		b.WriteString("_None._\n")
	} else {
		for _, in := range s.Inputs {
			fmt.Fprintf(&b, "- %s\n", in)
		}
	}
	b.WriteString("\n## Outputs\n\n")
	if len(s.Outputs) == 0 {
		b.WriteString("_None._\n")
	} else {
		for _, out := range s.Outputs {
			fmt.Fprintf(&b, "- %s\n", out)
		}
	}
	b.WriteString("\n## Steps\n\n")
	for i, st := range s.Steps {
		fmt.Fprintf(&b, "%d. **%s** — %s\n", i+1, st.Name, st.Action)
		if st.Description != "" {
			fmt.Fprintf(&b, "   - %s\n", st.Description)
		}
		if st.Guard != "" {
			fmt.Fprintf(&b, "   - Guard: %s\n", st.Guard)
		}
		for _, in := range st.Inputs {
			fmt.Fprintf(&b, "   - Input: %s\n", in)
		}
		for _, out := range st.Outputs {
			fmt.Fprintf(&b, "   - Output: %s\n", out)
		}
	}
	return b.Bytes(), nil
}

func writeStringList(b *bytes.Buffer, key string, values []string) {
	values = dedupeSort(values)
	if len(values) == 0 {
		fmt.Fprintf(b, "%s: []\n", key)
		return
	}
	fmt.Fprintf(b, "%s:\n", key)
	for _, v := range values {
		fmt.Fprintf(b, "  - %q\n", v)
	}
}
