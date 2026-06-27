// Package skillgen provides deterministic, non-LLM generation of agent skill
// artifacts from repeatable procedures.
package skillgen

import "context"

// Ledger records avoided token spend when a skill is generated.
type Ledger interface {
	RecordSkillGen(ctx context.Context, savedTokens int64) error
}

type noopLedger struct{}

func (noopLedger) RecordSkillGen(ctx context.Context, savedTokens int64) error { return nil }

// Step is one repeatable action in a procedure.
type Step struct {
	Name        string   `json:"name"`
	Action      string   `json:"action"`
	Inputs      []string `json:"inputs"`
	Outputs     []string `json:"outputs"`
	Guard       string   `json:"guard"`
	Description string   `json:"description"`
}

// Procedure is the repeatable process to turn into a skill.
type Procedure struct {
	Name        string   `json:"name"`
	Trigger     string   `json:"trigger"`
	Description string   `json:"description"`
	Inputs      []string `json:"inputs"`
	Outputs     []string `json:"outputs"`
	Steps       []Step   `json:"steps"`
}

// Skill is the generated artifact in parsed form.
type Skill struct {
	Name        string   `json:"name"`
	Trigger     string   `json:"trigger"`
	Description string   `json:"description"`
	Inputs      []string `json:"inputs"`
	Outputs     []string `json:"outputs"`
	Steps       []Step   `json:"steps"`
}
