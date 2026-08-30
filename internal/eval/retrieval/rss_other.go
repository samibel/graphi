//go:build !unix

package retrieval

// peakRSSMB has no probe on this platform; the measure reads UNKNOWN.
func peakRSSMB() (float64, bool) { return 0, false }
