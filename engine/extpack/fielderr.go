package extpack

import (
	"errors"
	"fmt"
)

// SW-230 (AX-10) — field attribution for the pack linter.
//
// # Why the errors grew a field and not a new message
//
// SW-229's validators already said what was wrong and what would have been
// acceptable; what they could not say is WHERE. A pack author fixing a rejection
// had to find the offending key by reading the message and searching the file.
// Lint turns "artifact.sha256 is not lowercase hex" into
// "pack.yaml:9:11: …", and the only fact it needs that the validator did not
// already have is which field produced the error.
//
// So FieldError is a WRAPPER, not a rewrite. Error() returns the wrapped
// message unchanged — byte for byte, including the "extpack: " prefix — so every
// existing caller, test and attack fixture that matches on the text is
// unaffected. The field is carried beside the message, where only the linter
// looks for it.
//
// # Scope
//
// A diagnostic has to name a FILE as well as a field, and a pack is two files.
// Scope records which one a field path is rooted in, so `id` (the manifest) and
// `rules[0].id` (the artifact) cannot be resolved against the same document.

// Scope names which of a pack's two files a field path is rooted in.
type Scope string

const (
	// ScopeManifest roots a field path in the pack manifest.
	ScopeManifest Scope = "manifest"
	// ScopeArtifact roots a field path in the pack's data artifact.
	ScopeArtifact Scope = "artifact"
)

// FieldError attributes a validation failure to one field of one pack file.
//
// It deliberately adds nothing to the message. A linter needs structure; a user
// reading a CLI error needs the sentence SW-229 already wrote.
type FieldError struct {
	// Scope is the file the field path is rooted in.
	Scope Scope
	// Field is the dotted/indexed path of the offending field, e.g.
	// "artifact.sha256" or "rules[2].from".
	Field string
	// Err is the underlying validation error.
	Err error
}

// Error returns the wrapped message unchanged.
func (e *FieldError) Error() string { return e.Err.Error() }

// Unwrap exposes the wrapped error to errors.Is/As.
func (e *FieldError) Unwrap() error { return e.Err }

// AsFieldError reports the field attribution carried by err, if any.
func AsFieldError(err error) (*FieldError, bool) {
	var fe *FieldError
	if errors.As(err, &fe) {
		return fe, true
	}
	return nil, false
}

// manifestErrf builds a manifest-scoped field error.
func manifestErrf(field, format string, a ...any) error {
	return &FieldError{Scope: ScopeManifest, Field: field, Err: fmt.Errorf(format, a...)}
}

// artifactErrf builds an artifact-scoped field error.
func artifactErrf(field, format string, a ...any) error {
	return &FieldError{Scope: ScopeArtifact, Field: field, Err: fmt.Errorf(format, a...)}
}

// withField attaches a field path to an already-built error, keeping the
// message. It is used where a shared helper (validateName, validateText)
// produces the message and only the caller knows the path.
func withField(scope Scope, field string, err error) error {
	if err == nil {
		return nil
	}
	if fe, ok := AsFieldError(err); ok && fe.Field != "" {
		return err
	}
	return &FieldError{Scope: scope, Field: field, Err: err}
}
