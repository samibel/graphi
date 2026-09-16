package embed

import (
	"context"
	"errors"
	"fmt"
)

// RuntimeAttestation identifies the exact embedder runtime that produced a
// vector. IdentityDigest identifies the loaded model bytes; Epoch identifies
// the serving process instance.
type RuntimeAttestation struct {
	IdentityDigest string `json:"identity_digest"`
	Epoch          string `json:"epoch"`
}

// RuntimeAttestor is an optional Embedder capability for detecting a changed
// model or serving process while an embedding request is in progress.
type RuntimeAttestor interface {
	ExpectedRuntimeAttestation() RuntimeAttestation
	RuntimeAttestation(context.Context) (RuntimeAttestation, error)
}

// RuntimeAttestationError reports an invalid or changed runtime attestation.
// Its Error method intentionally omits expected and observed values so it
// cannot expose runtime details in diagnostics.
type RuntimeAttestationError struct {
	Phase    string
	Expected RuntimeAttestation
	Observed RuntimeAttestation
	Reason   string
}

func (e *RuntimeAttestationError) Error() string {
	return fmt.Sprintf("embed: runtime attestation failed at %s: %s", e.Phase, e.Reason)
}

// ValidateRuntimeAttestation confirms that a runtime attestation is safe and
// complete enough to compare across an embedding request.
func ValidateRuntimeAttestation(a RuntimeAttestation) error {
	if len(a.IdentityDigest) != 64 {
		return errors.New("identity digest must be 64 lowercase hexadecimal characters")
	}
	for _, c := range a.IdentityDigest {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return errors.New("identity digest must be 64 lowercase hexadecimal characters")
		}
	}
	if len(a.Epoch) == 0 || len(a.Epoch) > 128 {
		return errors.New("epoch must contain 1 to 128 printable ASCII characters")
	}
	for _, c := range a.Epoch {
		if c < 0x20 || c > 0x7e {
			return errors.New("epoch must contain 1 to 128 printable ASCII characters")
		}
	}
	return nil
}

// VerifyRuntime checks an optional runtime attestation. Embedders that do not
// implement RuntimeAttestor retain their existing behavior.
func VerifyRuntime(ctx context.Context, e Embedder, phase string) error {
	a, ok := e.(RuntimeAttestor)
	if !ok {
		return nil
	}
	want := a.ExpectedRuntimeAttestation()
	if err := ValidateRuntimeAttestation(want); err != nil {
		return &RuntimeAttestationError{Phase: phase, Expected: want, Reason: err.Error()}
	}
	got, err := a.RuntimeAttestation(ctx)
	if err != nil {
		return fmt.Errorf("embed: runtime attestation at %s: %w", phase, err)
	}
	if err := ValidateRuntimeAttestation(got); err != nil {
		return &RuntimeAttestationError{Phase: phase, Expected: want, Observed: got, Reason: err.Error()}
	}
	if got != want {
		return &RuntimeAttestationError{
			Phase:    phase,
			Expected: want,
			Observed: got,
			Reason:   "identity digest or process epoch changed",
		}
	}
	return nil
}
