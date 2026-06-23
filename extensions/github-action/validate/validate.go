// Package validate is the SW-043 action.yml contract checker. It makes the
// otherwise-declarative GitHub Action CHECKABLE in CI by parsing action.yml and
// the entrypoint and asserting the metadata + security invariants the story
// requires:
//
//   - every input documents type, default (or is required), and a required flag
//   - every output documents a type/description and a projected value
//   - runs.using is a PINNED composite runtime (not a floating docker tag)
//   - every `uses:` step is pinned by a FULL 40-hex commit SHA (no @vN / :latest)
//   - the entrypoint never places the GitHub token on the command line (argv)
//   - the entrypoint invokes the four real graphi subcommands IN ORDER
//
// It intentionally does NOT pull in a general YAML dependency (the module stays
// dependency-light, mirroring internal/bench's constrained reader): it operates
// on the well-known, hand-authored action.yml shape with targeted line scanning.
// Any deviation returns a descriptive error so a regression fails the build
// loudly rather than silently shipping an unpinned or under-documented Action.
package validate

import (
	"fmt"
	"regexp"
	"strings"
)

// RequiredInputs are the inputs the story mandates the Action expose (each must be
// documented with type/default/required).
var RequiredInputs = []string{
	"github-token",
	"base-ref",
	"head-ref",
	"merge-gate",
	"gate-threshold",
	"comment-marker",
}

// RequiredOutputs are the outputs the story mandates (each documented + projected).
var RequiredOutputs = []string{"risk-score", "gate-status", "comment-url"}

// OrderedSubcommands is the sibling invocation order the entrypoint must follow
// (AC1): analyze pr-risk -> pr-signals -> pr-questions -> pr-comment.
var OrderedSubcommands = []string{"pr-risk", "pr-signals", "pr-questions", "pr-comment"}

// fullSHA matches a 40-hex git commit SHA — the only acceptable `uses:` pin.
var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// InputSpec is the parsed contract for one action.yml input.
type InputSpec struct {
	Name        string
	HasType     bool
	HasDefault  bool
	HasRequired bool
	Required    bool
}

// OutputSpec is the parsed contract for one action.yml output.
type OutputSpec struct {
	Name     string
	HasValue bool
	HasDesc  bool
}

// Result is the parsed-and-validated view of an action.yml. A non-empty Errors
// slice means the contract is violated.
type Result struct {
	Using       string
	Inputs      map[string]InputSpec
	Outputs     map[string]OutputSpec
	UsesRefs    []string // every `uses:` value found under runs.steps
	Errors      []string
}

// ValidateActionYML parses the action.yml text and checks every contract rule.
// It returns a Result whose Errors slice is empty iff the contract holds.
func ValidateActionYML(text string) Result {
	res := Result{
		Inputs:  map[string]InputSpec{},
		Outputs: map[string]OutputSpec{},
	}
	parseTopLevel(text, &res)
	checkInputs(&res)
	checkOutputs(&res)
	checkPinnedRuntime(&res)
	return res
}

// parseTopLevel walks the action.yml as an indentation-structured document and
// extracts the inputs map, outputs map, runs.using, and every `uses:` ref. It
// relies on the canonical 2-space-indent block-mapping shape of action.yml.
func parseTopLevel(text string, res *Result) {
	lines := strings.Split(text, "\n")
	section := "" // "inputs" | "outputs" | "runs" | ""
	var curInput *InputSpec
	var curOutput *OutputSpec

	flushInput := func() {
		if curInput != nil {
			res.Inputs[curInput.Name] = *curInput
			curInput = nil
		}
	}
	flushOutput := func() {
		if curOutput != nil {
			res.Outputs[curOutput.Name] = *curOutput
			curOutput = nil
		}
	}

	for _, raw := range lines {
		line := stripComment(raw)
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := leadingSpaces(line)
		trimmed := strings.TrimSpace(line)

		// Top-level keys (indent 0).
		if indent == 0 {
			flushInput()
			flushOutput()
			switch {
			case strings.HasPrefix(trimmed, "inputs:"):
				section = "inputs"
			case strings.HasPrefix(trimmed, "outputs:"):
				section = "outputs"
			case strings.HasPrefix(trimmed, "runs:"):
				section = "runs"
			default:
				section = ""
			}
			continue
		}

		switch section {
		case "inputs":
			// indent 2 => a new input name; indent 4 => its attributes.
			if indent == 2 && strings.HasSuffix(trimmed, ":") {
				flushInput()
				name := strings.TrimSuffix(trimmed, ":")
				curInput = &InputSpec{Name: name}
			} else if curInput != nil && indent >= 4 {
				switch {
				case strings.HasPrefix(trimmed, "type:"):
					curInput.HasType = true
				case strings.HasPrefix(trimmed, "default:"):
					curInput.HasDefault = true
				case strings.HasPrefix(trimmed, "required:"):
					curInput.HasRequired = true
					curInput.Required = strings.Contains(trimmed, "true")
				}
			}
		case "outputs":
			if indent == 2 && strings.HasSuffix(trimmed, ":") {
				flushOutput()
				curOutput = &OutputSpec{Name: strings.TrimSuffix(trimmed, ":")}
			} else if curOutput != nil && indent >= 4 {
				switch {
				case strings.HasPrefix(trimmed, "value:"):
					curOutput.HasValue = true
				case strings.HasPrefix(trimmed, "description:"):
					curOutput.HasDesc = true
				}
			}
		case "runs":
			// A list item may carry the key on the same line as the dash, e.g.
			// "- uses: actions/checkout@<sha>". Strip a leading "- " so both the
			// dash form and a plain "uses:" line are recognized.
			key := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			if strings.HasPrefix(key, "using:") {
				res.Using = strings.Trim(strings.TrimSpace(strings.TrimPrefix(key, "using:")), `"'`)
			}
			if strings.HasPrefix(key, "uses:") {
				ref := strings.Trim(strings.TrimSpace(strings.TrimPrefix(key, "uses:")), `"'`)
				res.UsesRefs = append(res.UsesRefs, ref)
			}
		}
	}
	flushInput()
	flushOutput()
}

// checkInputs verifies every required input is present and that EVERY declared
// input documents type + required flag + (default or required:true).
func checkInputs(res *Result) {
	for _, want := range RequiredInputs {
		if _, ok := res.Inputs[want]; !ok {
			res.Errors = append(res.Errors, fmt.Sprintf("required input %q is missing", want))
		}
	}
	for name, in := range res.Inputs {
		if !in.HasType {
			res.Errors = append(res.Errors, fmt.Sprintf("input %q is missing a `type`", name))
		}
		if !in.HasRequired {
			res.Errors = append(res.Errors, fmt.Sprintf("input %q is missing a `required` flag", name))
		}
		// A documented contract: an input must either carry a default OR be required
		// (a required input legitimately has no default — e.g. the secret token).
		if !in.HasDefault && !in.Required {
			res.Errors = append(res.Errors, fmt.Sprintf("input %q has neither a `default` nor `required: true`", name))
		}
	}
}

// checkOutputs verifies every required output is present, documented, and projects
// a value.
func checkOutputs(res *Result) {
	for _, want := range RequiredOutputs {
		o, ok := res.Outputs[want]
		if !ok {
			res.Errors = append(res.Errors, fmt.Sprintf("required output %q is missing", want))
			continue
		}
		if !o.HasValue {
			res.Errors = append(res.Errors, fmt.Sprintf("output %q is missing a projected `value`", want))
		}
		if !o.HasDesc {
			res.Errors = append(res.Errors, fmt.Sprintf("output %q is missing a `description`", want))
		}
	}
}

// checkPinnedRuntime enforces the supply-chain invariant: a composite runtime and
// every `uses:` pinned by a full 40-hex SHA (no floating @vN tag or :latest).
func checkPinnedRuntime(res *Result) {
	if res.Using != "composite" {
		res.Errors = append(res.Errors, fmt.Sprintf("runs.using must be a pinned composite runtime, got %q", res.Using))
	}
	if len(res.UsesRefs) == 0 {
		res.Errors = append(res.Errors, "no `uses:` steps found to validate pinning")
	}
	for _, ref := range res.UsesRefs {
		at := strings.LastIndexByte(ref, '@')
		if at < 0 {
			res.Errors = append(res.Errors, fmt.Sprintf("uses ref %q is not pinned (missing @<sha>)", ref))
			continue
		}
		pin := ref[at+1:]
		if !fullSHA.MatchString(pin) {
			res.Errors = append(res.Errors, fmt.Sprintf("uses ref %q is not pinned by a full 40-hex commit SHA (floating tag/:latest forbidden)", ref))
		}
	}
}

// ValidateEntrypoint checks the entrypoint script for the two source-level
// invariants the action.yml cannot express: the token is never on argv, and the
// real subcommands are invoked in the required order.
func ValidateEntrypoint(script string) []string {
	var errs []string

	// S1: the token must NEVER be placed on the command line. Flag forms that pass
	// the token as an argument are forbidden; the engine reads it from env.
	tokenOnArgv := regexp.MustCompile(`-(?:token|github-token)[ =]\s*["']?\$?\{?(?:GITHUB_TOKEN|INPUT_GITHUB_TOKEN|inputs\.github-token)`)
	if tokenOnArgv.MatchString(script) {
		errs = append(errs, "entrypoint places the GitHub token on the command line (argv); pass it via the environment only")
	}

	// AC1: the four real subcommands must appear in order.
	lastIdx := -1
	for _, sub := range OrderedSubcommands {
		idx := strings.Index(script, sub)
		if idx < 0 {
			errs = append(errs, fmt.Sprintf("entrypoint does not invoke the real subcommand %q", sub))
			continue
		}
		if idx < lastIdx {
			errs = append(errs, fmt.Sprintf("entrypoint invokes %q out of order (must follow %v)", sub, OrderedSubcommands))
		}
		lastIdx = idx
	}

	// AC1: must drive the EXISTING binary, not reimplement — assert it calls the
	// graphi binary for analysis + publish.
	if !strings.Contains(script, "analyze pr-risk") || !strings.Contains(script, "pr-comment") {
		errs = append(errs, "entrypoint must drive the existing graphi CLI subcommands (analyze pr-* and pr-comment)")
	}

	// AC2: the publish step must request a real publish (the single egress).
	if !strings.Contains(script, "-publish") {
		errs = append(errs, "entrypoint must request a real sticky-comment publish (-publish) for the single GitHub egress")
	}

	return errs
}

// stripComment removes a trailing `#` comment outside of quotes. action.yml uses
// simple quoting, so a quote-aware single pass is sufficient.
func stripComment(line string) string {
	inSingle, inDouble := false, false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble {
				return line[:i]
			}
		}
	}
	return line
}

// leadingSpaces counts the leading-space indentation of a line.
func leadingSpaces(line string) int {
	n := 0
	for n < len(line) && line[n] == ' ' {
		n++
	}
	return n
}
