package trust

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrSchemaUnsupported is the typed sentinel Decode wraps when the persisted
// snapshot's schema_version is not the one this binary supports (the package
// face of the contract doc §4 class ErrTrustSchemaUnsupported). Callers map it
// to snapshot state UNAVAILABLE — trust data that cannot be migrated is never
// partially interpreted (fail closed).
var ErrSchemaUnsupported = errors.New("trust: unsupported snapshot schema version")

// encodeCanonical is the shared canonical JSON emitter for every trust
// document (Snapshot, Assessment): encoding/json with HTML escaping disabled,
// no indentation, trailing newline stripped. Callers own nil-slice
// normalization and list ordering; this helper owns only the byte conventions,
// so the two document encoders cannot drift apart on them.
func encodeCanonical(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Encode serializes a Snapshot to its canonical byte form: encoding/json with
// HTML escaping disabled, no indentation, trailing newline stripped, nil
// slices normalized to empty (contract doc §2.3 rule 2: empty arrays, never
// null). Byte-identical encoding IS the digest contract — MetaSnapshotDigest
// is Digest over exactly these bytes, and DeriveState's digestOK input is a
// comparison against them — so any change to this function is a snapshot
// schema change and bumps SnapshotSchemaVersion. Field ordering follows the
// struct declaration and every list is a pre-sorted slice (no maps), so
// identical facts always encode to identical bytes.
func Encode(s Snapshot) ([]byte, error) {
	if s.Graph.EdgesByKind == nil {
		s.Graph.EdgesByKind = []KindCount{}
	}
	if s.External.TopBoundaries == nil {
		s.External.TopBoundaries = []Boundary{}
	}
	if s.Parse.ByReason == nil {
		s.Parse.ByReason = []ReasonCount{}
	}
	if s.Parse.Paths == nil {
		s.Parse.Paths = []string{}
	}
	if s.TypeResolution.DegradedUnits == nil {
		s.TypeResolution.DegradedUnits = []DegradedUnit{}
	}
	b, err := encodeCanonical(s)
	if err != nil {
		return nil, fmt.Errorf("trust: encode snapshot: %w", err)
	}
	return b, nil
}

// Decode parses bytes previously produced by Encode. A schema_version other
// than SnapshotSchemaVersion returns an error wrapping ErrSchemaUnsupported —
// an unknown schema is rejected whole, never best-effort interpreted.
func Decode(b []byte) (Snapshot, error) {
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return Snapshot{}, fmt.Errorf("trust: decode snapshot: %w", err)
	}
	if s.SchemaVersion != SnapshotSchemaVersion {
		return Snapshot{}, fmt.Errorf("%w: got %d, supported %d", ErrSchemaUnsupported, s.SchemaVersion, SnapshotSchemaVersion)
	}
	return s, nil
}

// Digest returns the content digest of the canonical Encode bytes in the form
// "sha256:<hex>" — the value persisted under MetaSnapshotDigest.
func Digest(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// FactDigest returns the digest of the snapshot's FACT sections only: the
// canonical Encode bytes with the generation binding (GenerationRef) zeroed.
//
// Decision of record for full/incremental parity: the persisted
// MetaSnapshotDigest always covers the FULL canonical bytes — it protects the
// stored document, binding included, and is what Load verifies. But
// FullPassGeneration is a per-pass random nonce (and the sync stamp trails the
// pass that wrote it), so two passes over identical source can never agree on
// the full digest. Byte-parity claims about the COLLECTED FACTS therefore
// compare FactDigest: identical facts yield an identical fact digest
// regardless of which pass — full or incremental — produced them.
func FactDigest(s Snapshot) (string, error) {
	s.Generation = GenerationRef{}
	b, err := Encode(s)
	if err != nil {
		return "", err
	}
	return Digest(b), nil
}
