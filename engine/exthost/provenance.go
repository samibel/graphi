package exthost

import (
	"encoding/json"
	"fmt"

	"github.com/samibel/graphi/engine/extpack"
)

// The confidence vocabulary an extension may claim, and the one it may not.
//
// ADR 0013 D5: `confirmed` means "confirmed by an authoritative source", and the
// host does not regard an extension as one — which is the same reasoning by
// which engine/link, graphi's OWN first-party resolver, is barred from it
// ("The linker NEVER returns TierConfirmed", engine/link/link.go:60). The
// ceiling for anything an extension produces is `derived`.
//
// The host REJECTS a `confirmed` claim rather than downgrading it. Downgrading
// would be the friendlier behaviour and the wrong one: it teaches an author that
// the ceiling is advisory, and it produces a result whose tier nobody chose.
const (
	ConfidenceHeuristic = "heuristic"
	ConfidenceDerived   = "derived"
	confidenceConfirmed = "confirmed"
)

// Provenance is the record every extension-influenced result carries.
//
// ADR 0013 D5.2: an extension-produced artifact must be distinguishable from a
// first-party one AT THE POINT OF CONSUMPTION, not only in a log. So provenance
// is a field of Result, not a side channel — a caller cannot hold the findings
// without holding the record of who produced them and from which bytes.
type Provenance struct {
	// ExtensionID and ExtensionVersion are the descriptor's, not the
	// handshake's. The handshake must AGREE with the descriptor (ErrIdentityMismatch
	// otherwise), and recording the pinned side means a provenance record can
	// never carry a name the user did not install.
	ExtensionID      string `json:"extension_id"`
	ExtensionVersion string `json:"extension_version"`
	// ArtifactSHA256 is the verified hash of the executable that produced this.
	ArtifactSHA256 string `json:"artifact_sha256"`
	// DescriptorSHA256 hashes the descriptor document, so "which grant was in
	// force" is answerable after the fact and not only "which binary ran".
	DescriptorSHA256 string `json:"descriptor_sha256"`
	Protocol         string `json:"protocol"`
	HostAPI          string `json:"host_api"`
	Operation        string `json:"operation"`
	// Confidence is the tier the extension claimed, after the host verified it
	// is one an extension may claim.
	Confidence string `json:"confidence"`
	// Tier is fixed at "labs". ADR 0013 I1/I4: no extension may claim a Stable
	// operation id or appear in mcp.StableOperations, and tier C does not leave
	// Labs without a new ADR. It is a constant field rather than an omitted one
	// so a consumer reads the ceiling instead of inferring it.
	Tier string `json:"tier"`
	// Trust is the ADR 0013 D3 honesty statement, carried in the DATA.
	//
	// It is here, and not only in a doc comment, because the surface that
	// renders a tier-C result is the surface that owes the user those words —
	// and a documentation obligation that lives only in prose is one a later
	// consumer can forget to discharge. Anything projecting this record has the
	// sentence in hand.
	Trust string `json:"trust"`
}

// TrustStatement is ADR 0013 D3, in the words the ADR requires: not
// "sandboxed", not "isolated", not "restricted".
const TrustStatement = "trusted local code, not a sandbox: this extension ran as a separate " +
	"process with the user's own OS rights; the declared ports bound its access to graphi's data, " +
	"not its access to the machine"

// Result is one extension answer, inseparable from its provenance.
type Result struct {
	Provenance Provenance      `json:"provenance"`
	Findings   json.RawMessage `json:"findings"`
}

// CanonicalJSON renders a result as stable bytes.
//
// Provenance is a struct, so its key order is fixed by declaration order.
// Findings pass through verbatim as the extension produced them — the host does
// not re-serialize foreign data, because re-serializing would hide an
// extension's non-determinism behind graphi's normalisation and make the
// conformance determinism check measure the host instead of the extension.
func (r Result) CanonicalJSON() ([]byte, error) {
	if len(r.Findings) == 0 {
		r.Findings = json.RawMessage("null")
	}
	b, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("exthost: encode result: %w", err)
	}
	return b, nil
}

// newProvenance builds the record for one answered call.
//
// Every extension-controlled string is length-bounded with extpack.Bound before
// it lands here, per standards ("repository-controlled text is length-bounded
// before it reaches an artifact") — the descriptor is written by whoever wrote
// the extension, so id, version and operation are exactly that text.
func newProvenance(l Loaded, operation, confidence string) Provenance {
	return Provenance{
		ExtensionID:      extpack.Bound(l.Descriptor.ID),
		ExtensionVersion: extpack.Bound(l.Descriptor.Version),
		ArtifactSHA256:   l.Descriptor.Artifact.SHA256,
		DescriptorSHA256: l.DescriptorSHA256,
		Protocol:         ProtocolVersion,
		HostAPI:          HostAPIVersion,
		Operation:        extpack.Bound(operation),
		Confidence:       confidence,
		Tier:             "labs",
		Trust:            TrustStatement,
	}
}

// checkConfidence enforces the ADR 0013 D5 ceiling.
func checkConfidence(claimed string) (string, error) {
	switch claimed {
	case ConfidenceHeuristic, ConfidenceDerived:
		return claimed, nil
	case confidenceConfirmed:
		return "", fmt.Errorf("%w: the extension claimed %q; the ceiling for anything an extension "+
			"produces is %q (ADR 0013 D5 — graphi's own linker is barred from %q for the same "+
			"reason). The result is rejected, not downgraded",
			ErrConfidenceLaundering, confidenceConfirmed, ConfidenceDerived, confidenceConfirmed)
	case "":
		return "", fmt.Errorf("%w: the result declares no confidence tier; an unstated tier cannot "+
			"be found too high, which is the same as not checking",
			ErrProtocolViolation)
	default:
		return "", fmt.Errorf("%w: confidence %q is not one of %q, %q",
			ErrProtocolViolation, extpack.Bound(claimed), ConfidenceHeuristic, ConfidenceDerived)
	}
}
