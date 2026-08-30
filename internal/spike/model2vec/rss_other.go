//go:build !unix

package model2vec

// peakRSSBytes is unavailable without getrusage; the measurement reports UNKNOWN.
func peakRSSBytes() (int64, bool) { return 0, false }
