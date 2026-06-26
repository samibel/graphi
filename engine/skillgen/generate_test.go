package skillgen

import (
	"bytes"
	"context"
	"testing"
)

type testLedger struct {
	generations []int64
}

func (tl *testLedger) RecordSkillGen(ctx context.Context, savedTokens int64) error {
	tl.generations = append(tl.generations, savedTokens)
	return nil
}

func sampleProcedure() Procedure {
	return Procedure{
		Name:        "Run Refactor",
		Trigger:     "/refactor",
		Description: "Apply a safe refactor across the workspace.",
		Inputs:      []string{"target", "kind"},
		Outputs:     []string{"edit-count"},
		Steps: []Step{
			{Name: "validate", Action: "check precondition", Inputs: []string{"target"}, Guard: "target exists"},
			{Name: "apply", Action: "apply edit", Outputs: []string{"edit-count"}},
		},
	}
}

func TestGenerateDeterminism(t *testing.T) {
	ctx := context.Background()
	g := NewGenerator(nil)
	p := sampleProcedure()

	var first []byte
	for i := 0; i < 3; i++ {
		_, md, err := g.Generate(ctx, p)
		if err != nil {
			t.Fatalf("generate %d: %v", i, err)
		}
		if first == nil {
			first = md
			continue
		}
		if !bytes.Equal(first, md) {
			t.Fatalf("run %d output differs:\n%s\nvs\n%s", i, first, md)
		}
	}
}

func TestGenerateAndParse(t *testing.T) {
	ctx := context.Background()
	tl := &testLedger{}
	g := NewGenerator(tl)
	p := sampleProcedure()

	skill, md, err := g.Generate(ctx, p)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if skill.Name != "Run Refactor" {
		t.Fatalf("unexpected skill name: %s", skill.Name)
	}
	if len(tl.generations) != 1 {
		t.Fatalf("expected 1 ledger entry, got %d", len(tl.generations))
	}

	loaded, err := ParseSkill(md)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if loaded.Name != skill.Name {
		t.Fatalf("loaded name mismatch: %s", loaded.Name)
	}
	if loaded.Trigger != skill.Trigger {
		t.Fatalf("loaded trigger mismatch")
	}
	if len(loaded.Steps) != len(skill.Steps) {
		t.Fatalf("loaded steps mismatch: got %d want %d", len(loaded.Steps), len(skill.Steps))
	}
}

func TestGenerateNoNetwork(t *testing.T) {
	ctx := context.Background()
	g := NewGenerator(nil)
	_, md, err := g.Generate(ctx, sampleProcedure())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(md) == 0 {
		t.Fatalf("expected non-empty markdown")
	}
}
