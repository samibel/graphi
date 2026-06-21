// Package canary — netns isolation harness.
//
// On Linux, Run sets up a loopback-only network namespace and executes the
// isolated function inside it, then tears it down. On non-Linux (or when the
// runner lacks the required capabilities), IsAvailable returns false and Run
// HARD-FAILS — a misconfigured environment must never silently mask egress
// (SW-008 AC: "the job hard-fails rather than silently passing").
//
// This file declares the portable interface; platform implementations live in
// netns_linux.go (real isolation) and netns_other.go (hard-fail stub).
package canary

// IsolationError reports that runner-level network isolation is unavailable.
// The canary treats this as fatal: it MUST NOT proceed without isolation.
type IsolationError struct {
	Reason string
}

func (e *IsolationError) Error() string { return "canary: isolation unavailable: " + e.Reason }

// Isolator abstracts the runner's ability to provide loopback-only network
// isolation. The production implementation is the Linux netns harness; tests
// inject a fake to exercise the preflight pass/fail branches without root.
type Isolator interface {
	// IsAvailable reports whether this runner can actually deny non-loopback
	// egress. A false result means the canary must hard-fail.
	IsAvailable() bool
	// Run executes fn under loopback-only isolation. It MUST be callable only
	// when IsAvailable() is true; callers gate on IsAvailable first.
	Run(fn func() error) error
}

// noIsolation is the default isolator used when the platform cannot provide a
// real netns (non-Linux, or unprivileged runner). Its IsAvailable() is false so
// the canary hard-fails by construction.
type noIsolation struct{}

func (noIsolation) IsAvailable() bool { return false }

func (noIsolation) Run(fn func() error) error {
	return &IsolationError{Reason: "no network-namespace isolation on this runner"}
}

// defaultIsolator returns the best available isolator for the current platform.
func defaultIsolator() Isolator {
	return newPlatformIsolator()
}
