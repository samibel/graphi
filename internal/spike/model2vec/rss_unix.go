//go:build unix

package model2vec

import (
	"runtime"
	"syscall"
)

// peakRSSBytes is the process-lifetime peak resident set from getrusage, as
// internal/eval/retrieval and cmd/eval read it. Linux reports MAXRSS in KiB,
// darwin in bytes.
func peakRSSBytes() (int64, bool) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, false
	}
	v := int64(ru.Maxrss)
	if runtime.GOOS != "darwin" {
		v *= 1024
	}
	return v, true
}
