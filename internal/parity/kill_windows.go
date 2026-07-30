//go:build windows

package parity

import (
	"errors"
	"os"
)

// SignalJourneysSupported is FALSE on Windows, and the row it gates is recorded
// as a coverage limit rather than dropped — see the doc comment on the unix
// variant, and AC-9.
//
// WHY, precisely, so the limit is auditable rather than folklore: Windows has no
// SIGKILL. The nearest equivalent, TerminateProcess (which os.Process.Kill maps
// to), is not the same fault: it is asynchronous with respect to pending I/O and
// the OS may still complete writes already handed to the filesystem, so "the
// process died between the durable graph write and the meta commit" is not a
// state this platform can be made to reproduce faithfully. Running the row here
// would produce a green that means something WEAKER than the row claims, which
// is worse than a recorded skip.
const SignalJourneysSupported = false

func killHard(p *os.Process) error {
	return errors.New("parity: no SIGKILL equivalent on this platform")
}
