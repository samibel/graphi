package retrieval

// Fingerprint comparison for the embedded-model qualification.
//
// WHY THIS FILE EXISTS
//
// `embed.Fingerprint.Canonical()` encodes eight length-prefixed fields in a
// fixed order (see engine/embed/fingerprint.go):
//
//	0 model_id         4 dim
//	1 revision         5 document_schema
//	2 model_sha256     6 chunker_config
//	3 tokenizer_sha256 7 graph_generation
//
// Seven of those eight are functions of pinned artifacts: the model weights,
// the tokenizer, the dimension, the document schema, the chunker/durable
// profile. Preregistering them is exactly what preregistration is for — they
// are decided before the run, and any drift between the preregistered value
// and the value a run observes is real evidence that the run measured a
// different embedding space than the one that was frozen.
//
// Field 7 is not like the others. `index.commit_generation` is minted by
// `mintCommitGeneration` (engine/ingest/warmstart.go) from `crypto/rand` — a
// fresh 128-bit UUID hex on every committed graph mutation. That randomness is
// deliberate: it makes a monotonic-counter race between two ingesting
// processes impossible to lose silently. The consequence for this gate is that
// two index builds over the SAME source tree, the SAME commit and the SAME
// binary carry DIFFERENT graph generations, and therefore different canonical
// fingerprints.
//
// So a preregistration that pins `fingerprint_canonical` in full, and a run
// that compares it byte-for-byte against what a fresh reindex produces, states
// a condition that can never hold. The gate was unsatisfiable, not strict.
//
// WHAT REPLACES IT
//
// Fields 0-6 stay preregistered and are still compared exactly, field by
// field. Field 7 is bound at RUNTIME instead of in advance: a random token
// fixed in advance carries no evidence, because nothing about the model, the
// corpus or the candidate can be inferred from it.
//
// What DOES carry evidence about the graph generation is its INTERNAL
// consistency WITHIN ONE ARM: all 64 observations of one arm must name the
// SAME generation, because every query of an arm is supposed to have been
// measured against the one index that arm built. A generation that changes
// mid-arm means the index was rebuilt under the measurement, and the 64
// results are then not one measurement at all. That property is checked in
// validateQualificationEvidence (see graphGenerationConsistent); before this
// change it was checked nowhere at all.
//
// The comparison stops at the arm boundary, and must: each arm builds its OWN
// index in its OWN work directory (captureQualificationBuilds), so different
// arms carry different generations BY CONSTRUCTION. A cross-arm comparison
// would therefore catch nothing real and would fail every run.
//
// For the same reason the two-build reproducibility digest cannot hash a
// generation: see qualificationObservationsExceptGraphGenerationSHA256.
//
// The preregistration therefore carries QualificationGraphGenerationPlaceholder
// in field 7. It is a documented constant, not an observation, and no gate ever
// compares it against a run.

import (
	"fmt"
	"strconv"
	"strings"
)

// qualificationFingerprintFieldCount is the exact field count
// embed.Fingerprint.Canonical() emits. A canonical with any other count is
// malformed — never "merely different" — so every helper here fails closed on
// it rather than comparing a short slice and reporting a mismatch that would
// read as model drift.
const qualificationFingerprintFieldCount = 8

// qualificationGraphGenerationField is the index of the runtime-bound field.
const qualificationGraphGenerationField = 7

// qualificationFingerprintFieldNames names the fields for error messages. An
// operator staring at a refusal should not have to count length prefixes to
// learn which of eight fields moved, so every message carries both the index
// and the name.
var qualificationFingerprintFieldNames = [qualificationFingerprintFieldCount]string{
	"model_id",
	"revision",
	"model_sha256",
	"tokenizer_sha256",
	"dim",
	"document_schema",
	"chunker_config",
	"graph_generation",
}

// QualificationGraphGenerationPlaceholder is the constant a preregistration
// carries in the canonical fingerprint's eighth field.
//
// It is deliberately NOT a 32-character hex string: a real
// `index.commit_generation` is exactly that, and a placeholder that could be
// mistaken for one would hide the very fact this constant exists to state —
// that the field was never observed and is bound when the run builds its
// index. It is also deliberately distinct from embed.GraphGenerationPlaceholder
// ("unknown"), which means something else entirely: a graphstore that exposes
// no generation at all.
const QualificationGraphGenerationPlaceholder = "runtime-bound-graph-generation"

// qualificationFingerprintFields decodes a canonical fingerprint fail-closed.
// role names the side being decoded ("observed", "preregistered", …) so a
// refusal says which of the two inputs was malformed.
func qualificationFingerprintFields(canonical, role string) ([]string, error) {
	fields, ok := decodeQualificationFingerprint(canonical)
	if !ok || len(fields) != qualificationFingerprintFieldCount {
		return nil, fmt.Errorf("%s fingerprint is not a complete %d-field canonical fingerprint (%s)",
			role, qualificationFingerprintFieldCount, qualificationFingerprintShape(canonical, fields, ok))
	}
	return fields, nil
}

// qualificationFingerprintShape describes a malformed canonical without
// echoing it: the raw string can be kilobytes long (the CodeRank arm's
// chunker_config is a whole durable manifest) and a refusal that dumps it is
// unreadable in CI output.
func qualificationFingerprintShape(canonical string, fields []string, ok bool) string {
	if strings.TrimSpace(canonical) == "" {
		return "value is empty"
	}
	if !ok {
		return fmt.Sprintf("value is not length-prefixed encodable, %d bytes", len(canonical))
	}
	return fmt.Sprintf("decoded %d fields", len(fields))
}

// qualificationFingerprintsAgree compares two canonical fingerprints field by
// field, EXCLUDING graph_generation (field 7), and names the first field that
// differs.
//
// Excluding field 7 is the entire semantic change: see this file's header for
// why a randomly minted generation cannot be preregistered. Every other field
// is compared exactly, as before.
//
// Fail-closed: either side failing to decode as a complete eight-field
// canonical is an error, never a silent "they differ".
func qualificationFingerprintsAgree(observed, preregistered string) error {
	observedFields, err := qualificationFingerprintFields(observed, "observed")
	if err != nil {
		return err
	}
	preregisteredFields, err := qualificationFingerprintFields(preregistered, "preregistered")
	if err != nil {
		return err
	}
	for i := 0; i < qualificationFingerprintFieldCount; i++ {
		if i == qualificationGraphGenerationField {
			continue
		}
		if observedFields[i] != preregisteredFields[i] {
			return fmt.Errorf("fingerprint field %d (%s) differs: observed %s, preregistered %s",
				i, qualificationFingerprintFieldNames[i],
				qualificationFingerprintValue(observedFields[i]),
				qualificationFingerprintValue(preregisteredFields[i]))
		}
	}
	return nil
}

// qualificationFingerprintValue renders one field value for an error message.
// Short values are quoted verbatim; a long one (the CodeRank durable profile)
// is elided down to its head plus its exact byte count, so the message stays
// one readable line while still distinguishing two different profiles.
func qualificationFingerprintValue(value string) string {
	const maxInline = 96
	if len(value) <= maxInline {
		return fmt.Sprintf("%q", value)
	}
	return fmt.Sprintf("%q… (%d bytes, sha256 %s)", value[:maxInline], len(value), SHA256Hex([]byte(value)))
}

// qualificationGraphGenerationOf returns the canonical fingerprint's eighth
// field. It reports false — never a guessed empty string — when the canonical
// is not a complete eight-field encoding, so a caller cannot mistake a
// malformed fingerprint for one naming the empty generation.
func qualificationGraphGenerationOf(canonical string) (string, bool) {
	fields, err := qualificationFingerprintFields(canonical, "observed")
	if err != nil {
		return "", false
	}
	return fields[qualificationGraphGenerationField], true
}

// qualificationDigestElidedGraphGeneration is what replaces field 7 before a
// canonical fingerprint is fed to a reproducibility digest. It is a fixed,
// self-describing token rather than "": a reader who dumps the hashed bytes
// should see WHY the value is not there, and an empty field 7 is a value a
// real fingerprint can genuinely carry.
const qualificationDigestElidedGraphGeneration = "excluded-from-reproducibility-digest"

// qualificationEncodeFingerprintFields is the inverse of
// decodeQualificationFingerprint and mirrors embed.encodeCanonical (which is
// unexported): each field becomes `<len>:<value>`, joined with "\n".
func qualificationEncodeFingerprintFields(fields []string) string {
	var b strings.Builder
	for i, field := range fields {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strconv.Itoa(len(field)))
		b.WriteByte(':')
		b.WriteString(field)
	}
	return b.String()
}

// qualificationFingerprintWithGraphGenerationElided returns canonical with
// field 7 replaced by qualificationDigestElidedGraphGeneration, leaving fields
// 0-6 byte-for-byte untouched.
//
// A value that does not decode as a complete eight-field canonical is returned
// VERBATIM. That is deliberate: the lexical M0 control records an empty index
// fingerprint, and a malformed one is evidence in its own right — neither may
// be silently normalised into some other value by a digest helper.
func qualificationFingerprintWithGraphGenerationElided(canonical string) string {
	fields, ok := decodeQualificationFingerprint(canonical)
	if !ok || len(fields) != qualificationFingerprintFieldCount {
		return canonical
	}
	fields[qualificationGraphGenerationField] = qualificationDigestElidedGraphGeneration
	return qualificationEncodeFingerprintFields(fields)
}

// qualificationModelIDField is the index of the model identity inside a
// canonical fingerprint.
const qualificationModelIDField = 0

// qualificationModelIDOf returns the canonical fingerprint's FIRST field, the
// model id.
//
// It exists because two different KINDS of value travel under the word
// "fingerprint" in this code base, and comparing one against the other is a
// condition that can never hold:
//
//   - a CANONICAL fingerprint — the eight length-prefixed fields
//     embed.Fingerprint.Canonical() emits;
//   - a MODEL ID — embed.Fingerprint.ModelID, i.e. field 0 of that canonical,
//     which is what engine/retrieval stamps into Summary.ModelFingerprint
//     (see engine/retrieval/service.go: `model = st.Requested.ModelID`).
//
// A gate that wants to check a retrieval summary's model identity against a
// preregistered or loaded generation must therefore compare it against THIS
// field, not against the whole canonical. Reporting false rather than an empty
// string keeps a malformed canonical from silently reading as "the empty model
// id".
func qualificationModelIDOf(canonical string) (string, bool) {
	fields, err := qualificationFingerprintFields(canonical, "observed")
	if err != nil {
		return "", false
	}
	return fields[qualificationModelIDField], true
}

// qualificationFingerprintsDifferOutsideGraphGeneration reports whether two
// canonical fingerprints differ in at least one field OTHER than
// graph_generation.
//
// It exists for the M1/M2 distinctness check: two arms that are "different"
// only because their random generations happen to differ are not two different
// embedding spaces, and accepting that as arm separation would let the
// 512-vs-8192 comparison compare nothing.
func qualificationFingerprintsDifferOutsideGraphGeneration(left, right string) (bool, error) {
	if _, err := qualificationFingerprintFields(left, "first"); err != nil {
		return false, err
	}
	if _, err := qualificationFingerprintFields(right, "second"); err != nil {
		return false, err
	}
	return qualificationFingerprintsAgree(left, right) != nil, nil
}
