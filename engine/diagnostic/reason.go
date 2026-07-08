package diagnostic

// ReasonCode is the closed, governed catalog of diagnostic reason codes. Every
// analyzer must emit codes from this catalog; the coverage test enforces this.
type ReasonCode string

const (
	ReasonDeadInternalSymbol       ReasonCode = "dead_internal_symbol"
	ReasonUnresolvedInternalRef    ReasonCode = "unresolved_internal_ref"
	ReasonUnresolvedExternalImport ReasonCode = "unresolved_external_import"
	ReasonEntrypointCandidate      ReasonCode = "entrypoint_candidate"
)

// reasonCatalog maps each code to its documented meaning. This is the single
// source of truth for the catalog.
var reasonCatalog = map[ReasonCode]string{
	ReasonDeadInternalSymbol:       "A symbol with no live inbound references that is internal to the analyzed module.",
	ReasonUnresolvedInternalRef:    "A reference to an internal symbol the resolver could not confirm.",
	ReasonUnresolvedExternalImport: "A reference to an external symbol the resolver could not confirm, aggregated by target.",
	ReasonEntrypointCandidate:      "A symbol with no live inbound references that looks like a framework/language entry point (annotation, main, or test path), so it is reported at info severity rather than flagged as dead.",
}

// ValidReasonCodes returns the canonical ordered list of catalog members.
func ValidReasonCodes() []ReasonCode {
	return []ReasonCode{
		ReasonDeadInternalSymbol,
		ReasonUnresolvedInternalRef,
		ReasonUnresolvedExternalImport,
		ReasonEntrypointCandidate,
	}
}

// IsValidReasonCode reports whether c is a member of the closed catalog.
func IsValidReasonCode(c ReasonCode) bool {
	_, ok := reasonCatalog[c]
	return ok
}

// ReasonDocs returns the documented meaning of c, or a placeholder if c is not
// catalogued.
func ReasonDocs(c ReasonCode) string {
	if d, ok := reasonCatalog[c]; ok {
		return d
	}
	return "(off-catalogue reason code)"
}
