//go:build !windows

package parity

import (
	"os"
	"syscall"
)

// SignalJourneysSupported reports whether this platform can run the two
// lifecycle rows that kill a real graphi subprocess with a real signal.
//
// It is the ONLY switch for that capability, and it is a build-tag constant
// rather than a runtime check so the syscall import cannot leak onto a platform
// that has no SIGKILL. AC-9's rule is that a platform limit is RECORDED, never
// silent: when this is false the two crash rows appear in the published report
// as SKIPPED with a coverage limit naming runtime.GOOS and the reason, and the
// run is INCOMPLETE and therefore not publishable. A skipped row that vanished
// from the report would be the failure mode this constant exists to prevent.
const SignalJourneysSupported = true

// killHard delivers SIGKILL to a real process.
//
// SIGKILL, not SIGTERM, and not context cancellation: AC-3 requires the crash to
// be indistinguishable from a machine losing power mid-pass. SIGTERM is
// catchable and Go's default disposition would still run the process's exit
// path; SIGKILL cannot be handled, blocked or ignored, so no deferred Close, no
// SQLite checkpoint and no meta rollback runs. That is the condition
// docs/adr/0004-ingest-recovery-disposition.md dispositions, and anything
// gentler would prove a weaker property than the one claimed.
func killHard(p *os.Process) error { return p.Signal(syscall.SIGKILL) }
