//go:build !linux

// Package canary — non-Linux isolator stub.
//
// graphi's egress canary requires a loopback-only network namespace, which is a
// Linux-only facility. On every other platform (macOS, Windows, …) isolation is
// unavailable, so IsAvailable() is false and the canary HARD-FAILS by design.
// This is the acceptance criterion: a misconfigured environment cannot mask
// egress by silently passing.
package canary

// newPlatformIsolator returns a no-op isolator whose IsAvailable() is false on
// non-Linux platforms, forcing the canary to hard-fail with a clear reason.
func newPlatformIsolator() Isolator { return noIsolation{} }
