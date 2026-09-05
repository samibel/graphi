package retrieval

// The rater's view. This file exists so that "the rater saw exactly the
// preserved bundle bytes" is a property of the code rather than a promise in a
// report: BuildRaterPrompt embeds the preserved slice verbatim and returns the
// exact offset at which it sits, and a test reads the bytes back out of the
// prompt and compares digests.

import (
	"bytes"
	"fmt"
)

// RaterInstructions is the frozen instruction block every rater receives. It
// names no repository, no expected answer, no rubric criterion and no other
// rater. It is part of the prompt's content address, so a later edit to the
// instructions changes every prompt hash rather than passing unnoticed.
const RaterInstructions = `You are answering one question about a Go repository you cannot see.

Your ONLY source of information is the single JSON-RPC response printed between
the BEGIN and END markers below. It is the complete, unmodified response bytes
of one tool call. You have no repository, no checkout, no search, no notes and
no other material, and you must not use recollection of any public project to
supply facts that are not in the response.

Answer the question directly and concretely, citing the specific evidence in the
response you relied on (file paths, symbol names, snippet lines).

If the response does not contain enough information to answer, reply with the
single word INSUFFICIENT followed by one sentence saying what was missing. That
is a legitimate answer, not a refusal.`

// RaterPrompt is one rater invocation's exact input.
type RaterPrompt struct {
	QueryID string
	// Bytes is the complete prompt as handed to the rater.
	Bytes []byte
	// BundleOffset and BundleLength locate the preserved payload inside Bytes.
	// They are returned so a check can read the bundle back out rather than
	// trusting that it was embedded unchanged.
	BundleOffset int
	BundleLength int
	// SHA256 is the prompt's content address, recorded on every response.
	SHA256 string
	// BundleSHA256 and QueryTextSHA256 are the pre-registered addresses this
	// prompt was built from.
	BundleSHA256    string
	QueryTextSHA256 string
}

const (
	bundleBeginMarker = "----- BEGIN task_context/2 RESPONSE BYTES -----\n"
	bundleEndMarker   = "----- END task_context/2 RESPONSE BYTES -----\n"
)

// BuildRaterPrompt assembles the one permitted rater input: the question text
// and the exact preserved bundle bytes.
//
// It refuses a payload whose recorded digest does not recompute, because a
// prompt built from bytes nobody checked would make the byte-identity claim
// vacuous.
func BuildRaterPrompt(queryID, queryText string, payload PreservedPayload) (RaterPrompt, error) {
	if queryID == "" {
		return RaterPrompt{}, fmt.Errorf("retrieval %s: rater prompt needs a query id", QrelBlindSmokeEvaluationName)
	}
	if queryText == "" {
		return RaterPrompt{}, fmt.Errorf("retrieval %s: rater prompt for %s needs the query text", QrelBlindSmokeEvaluationName, queryID)
	}
	if payload.Bytes == nil {
		return RaterPrompt{}, fmt.Errorf("retrieval %s: rater prompt for %s has no preserved bundle bytes", QrelBlindSmokeEvaluationName, queryID)
	}
	if payload.SHA256 != SHA256Hex(payload.Bytes) || payload.ByteCount != len(payload.Bytes) {
		return RaterPrompt{}, fmt.Errorf("retrieval %s: rater prompt for %s was handed a payload whose digest or byte count does not recompute", QrelBlindSmokeEvaluationName, queryID)
	}
	if payload.Boundary != PayloadBoundaryCandidate {
		return RaterPrompt{}, fmt.Errorf("retrieval %s: rater prompt for %s was handed a %q payload, not %q", QrelBlindSmokeEvaluationName, queryID, payload.Boundary, PayloadBoundaryCandidate)
	}

	var buf bytes.Buffer
	buf.WriteString(RaterInstructions)
	buf.WriteString("\n\nQUESTION:\n")
	buf.WriteString(queryText)
	buf.WriteString("\n\n")
	buf.WriteString(bundleBeginMarker)
	offset := buf.Len()
	buf.Write(payload.Bytes)
	buf.WriteString(bundleEndMarker)

	prompt := buf.Bytes()
	return RaterPrompt{
		QueryID:         queryID,
		Bytes:           prompt,
		BundleOffset:    offset,
		BundleLength:    len(payload.Bytes),
		SHA256:          SHA256Hex(prompt),
		BundleSHA256:    payload.SHA256,
		QueryTextSHA256: SHA256Hex([]byte(queryText)),
	}, nil
}

// EmbeddedBundleBytes reads the bundle back out of the prompt at the recorded
// offset. It is the check side of BuildRaterPrompt: a rater that received a
// pretty-printed or re-marshaled bundle did not evaluate what the claim charges
// for, and this is how that is detected rather than assumed.
func (p RaterPrompt) EmbeddedBundleBytes() ([]byte, error) {
	if p.BundleOffset < 0 || p.BundleLength < 0 || p.BundleOffset+p.BundleLength > len(p.Bytes) {
		return nil, fmt.Errorf("retrieval %s: prompt for %s records a bundle span outside its own bytes", QrelBlindSmokeEvaluationName, p.QueryID)
	}
	return p.Bytes[p.BundleOffset : p.BundleOffset+p.BundleLength], nil
}

// CheckPromptCarriesPreservedBundle asserts byte identity between the prompt's
// embedded bundle and the preserved payload.
func CheckPromptCarriesPreservedBundle(p RaterPrompt, payload PreservedPayload) error {
	embedded, err := p.EmbeddedBundleBytes()
	if err != nil {
		return err
	}
	if !bytes.Equal(embedded, payload.Bytes) {
		return fmt.Errorf("retrieval %s: the bytes handed to a rater for %s are not byte-identical to the preserved payload", QrelBlindSmokeEvaluationName, p.QueryID)
	}
	if SHA256Hex(embedded) != payload.SHA256 {
		return fmt.Errorf("retrieval %s: the bundle embedded in the prompt for %s digests to %s, not the preserved %s", QrelBlindSmokeEvaluationName, p.QueryID, SHA256Hex(embedded), payload.SHA256)
	}
	return nil
}
