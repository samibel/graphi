package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// gateFile is the reversible publish-lock gate (CT-01). It is a checked-in file
// (default .github/publish-lock.json) that RC-01 lifts with a single documented
// change — flipping "locked" to false — without re-authoring any workflow.
//
// Locked is a *bool so that a gate file omitting the field is treated as an
// ambiguous/broken state and fails closed, rather than being read as "unlocked".
type gateFile struct {
	Locked *bool  `json:"locked"`
	Reason string `json:"reason"`
}

// Decision is the evaluated publish-lock state. When Locked is true the
// release DAG's publish job must not run: no tag, no release, no assets.
type Decision struct {
	Locked bool
	Reason string
}

// Evaluate reads the gate file and decides whether automatic publication is
// locked. It fails CLOSED: a missing, unreadable, malformed, or field-less gate
// file yields Locked=true. That is the "intentionally red gate creates no
// tag/release" contract (CT-01) — an ungated or broken state can never ship.
func Evaluate(gatePath string) Decision {
	data, err := os.ReadFile(gatePath)
	if err != nil {
		return Decision{Locked: true, Reason: fmt.Sprintf("gate file %q unreadable (%v); failing closed", gatePath, err)}
	}
	var g gateFile
	if err := json.Unmarshal(data, &g); err != nil {
		return Decision{Locked: true, Reason: fmt.Sprintf("gate file %q malformed (%v); failing closed", gatePath, err)}
	}
	if g.Locked == nil {
		return Decision{Locked: true, Reason: fmt.Sprintf("gate file %q has no \"locked\" field; failing closed", gatePath)}
	}
	if *g.Locked {
		reason := g.Reason
		if reason == "" {
			reason = "publish locked"
		}
		return Decision{Locked: true, Reason: reason}
	}
	reason := g.Reason
	if reason == "" {
		reason = "publish unlocked"
	}
	return Decision{Locked: false, Reason: reason}
}

// OutputLine is the `key=value` line the workflow consumes via $GITHUB_OUTPUT to
// gate the tag-push and release-dispatch steps.
func (d Decision) OutputLine() string {
	return fmt.Sprintf("locked=%t", d.Locked)
}

// Notice is the human-facing GitHub Actions annotation. It is non-empty only
// while locked, naming CT-01 so a suppressed publish is unambiguous in the log.
func (d Decision) Notice() string {
	if !d.Locked {
		return ""
	}
	return fmt.Sprintf("::notice::publish locked (CT-01) — %s", d.Reason)
}
