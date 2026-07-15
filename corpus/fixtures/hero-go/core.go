// Package hero is the SW-122 multi-file hero fixture. The tier-1 single-file
// fixture (corpus/fixtures/go) is frozen as the SW-110 byte-parity oracle, so
// cross-file behaviors (related_files ranking, cross-file callers) are
// exercised here instead. Layout: core.go defines the shared surface,
// consumer.go and report.go depend on it from separate files.
package hero

// Document is the parsed unit every consumer shares.
type Document struct{ Body string }

// Parse turns raw input into a Document.
func Parse(raw string) Document { return Document{Body: raw} }

// Render produces the printable form of a Document.
func Render(d Document) string { return d.Body }
