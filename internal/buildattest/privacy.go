// Package buildattest carries build-time evidence embedded by the canonical
// release builder. It performs no I/O and makes no trust decision by itself.
package buildattest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	PrivacySchemaVersion = 1
	PrivacyGateID        = "internal/canary.static-zero-telemetry/v1"
	PrivacyScope         = "canonical-cgo-free-build-graph"
)

// PrivacyEncoded is populated by internal/release through -ldflags -X only
// after the static privacy gate has returned a complete PASS. An ordinary
// `go build` deliberately leaves it empty.
var PrivacyEncoded string

// Privacy records what the canonical builder measured before linking the
// binary. PASS is the only embeddable status: failed or incomplete scans must
// stop the build rather than produce an attestation.
type Privacy struct {
	SchemaVersion  int      `json:"schema_version"`
	Status         string   `json:"status"`
	GateID         string   `json:"gate_id"`
	Scope          string   `json:"scope"`
	SourceRevision string   `json:"source_revision"`
	SourceModified bool     `json:"source_modified"`
	EvidenceDigest string   `json:"evidence_digest"`
	CGOEnabled     string   `json:"cgo_enabled"`
	BuildTags      []string `json:"build_tags,omitempty"`
	GOOS           string   `json:"goos"`
	GOARCH         string   `json:"goarch"`
}

// Encode validates and encodes an attestation for safe use as an ldflag value.
func Encode(a Privacy) (string, error) {
	if err := a.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return "", fmt.Errorf("marshal privacy attestation: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// Decode validates an encoded attestation. Unknown schemas and every status
// other than PASS are rejected fail-closed.
func Decode(encoded string) (Privacy, error) {
	if strings.TrimSpace(encoded) == "" {
		return Privacy{}, fmt.Errorf("privacy attestation is absent")
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return Privacy{}, fmt.Errorf("decode privacy attestation: %w", err)
	}
	var a Privacy
	if err := json.Unmarshal(raw, &a); err != nil {
		return Privacy{}, fmt.Errorf("parse privacy attestation: %w", err)
	}
	if err := a.Validate(); err != nil {
		return Privacy{}, err
	}
	return a, nil
}

// Embedded returns the validated attestation linked into this process.
func Embedded() (Privacy, bool, error) {
	if PrivacyEncoded == "" {
		return Privacy{}, false, nil
	}
	a, err := Decode(PrivacyEncoded)
	return a, true, err
}

// Validate enforces the closed build-evidence contract.
func (a Privacy) Validate() error {
	switch {
	case a.SchemaVersion != PrivacySchemaVersion:
		return fmt.Errorf("privacy attestation schema = %d, want %d", a.SchemaVersion, PrivacySchemaVersion)
	case a.Status != "PASS":
		return fmt.Errorf("privacy attestation status = %q, want PASS", a.Status)
	case a.GateID != PrivacyGateID:
		return fmt.Errorf("privacy attestation gate = %q, want %q", a.GateID, PrivacyGateID)
	case a.Scope != PrivacyScope:
		return fmt.Errorf("privacy attestation scope = %q, want %q", a.Scope, PrivacyScope)
	case !isHex(a.SourceRevision, 40, 64):
		return fmt.Errorf("privacy attestation has invalid source revision")
	case !isHex(a.EvidenceDigest, 64):
		return fmt.Errorf("privacy attestation has invalid evidence digest")
	case a.CGOEnabled != "0":
		return fmt.Errorf("privacy attestation cgo_enabled = %q, want 0", a.CGOEnabled)
	case a.GOOS == "" || a.GOARCH == "":
		return fmt.Errorf("privacy attestation target platform is incomplete")
	}
	for i, tag := range a.BuildTags {
		if tag == "" || strings.TrimSpace(tag) != tag || strings.ContainsAny(tag, ", \t\r\n") {
			return fmt.Errorf("privacy attestation has invalid build tag %q", tag)
		}
		if i > 0 && a.BuildTags[i-1] >= tag {
			return fmt.Errorf("privacy attestation build tags are not unique and sorted")
		}
	}
	return nil
}

func isHex(s string, lengths ...int) bool {
	validLength := false
	for _, n := range lengths {
		if len(s) == n {
			validLength = true
			break
		}
	}
	if !validLength {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
