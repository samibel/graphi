// Package contract serialization helpers.
package contract

import (
	"bytes"
	"encoding/json"
)

// Serialize marshals the result to canonical JSON: compact, stable key order,
// no HTML escaping, UTF-8, no trailing whitespace.
func Serialize(r *Result) ([]byte, error) {
	if err := ValidateResult(r); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	// json.Encoder.Encode appends a trailing newline; canonical output does not.
	return bytes.TrimSpace(buf.Bytes()), nil
}

// SerializeStable is an alias for Serialize; it exists for callers that want
// to be explicit about stability guarantees.
func SerializeStable(r *Result) ([]byte, error) {
	return Serialize(r)
}
