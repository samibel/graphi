//go:build unix

package retrieval

import (
	"runtime"
	"syscall"
)

// peakRSSMB reads the process's peak resident set via getrusage, as
// cmd/eval does. Linux reports MAXRSS in KiB, darwin in bytes. It is a
// PROCESS-LIFETIME peak, so within one run it is monotone across baselines;
// the report says so in the measure's unit note.
func peakRSSMB() (float64, bool) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, false
	}
	kb := int64(ru.Maxrss)
	if runtime.GOOS == "darwin" {
		kb /= 1024
	}
	return float64(kb) / 1024, true
}
