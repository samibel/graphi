package meter

// MeterRecord is the honest per-call token-savings signal. It is the value SW-018
// prices in USD and SW-019 persists. Every token-related field is deterministic
// w.r.t. its inputs; the record carries no wall-clock so it is hermetic.
type MeterRecord struct {
	// CallID is the caller-supplied unique identifier of the engine call this
	// record attributes tokens to. One record per call; the caller owns uniqueness.
	CallID string `json:"call_id"`
	// Model is the model identifier the savings are attributed to (used by SW-018
	// to look up the USD-per-token rate). May be empty.
	Model string `json:"model"`
	// ActualTokens is the tokens the call actually consumed, as reported by the
	// caller (e.g. engine/context.Bundle.Tokens). The meter never invents this.
	ActualTokens int `json:"actual_tokens"`
	// BaselineTokens is the whole-file-read token equivalent of the call's target
	// artifacts. It is 0 when BaselineAvailable is false.
	BaselineTokens int `json:"baseline_tokens"`
	// SavingsTokens is BaselineTokens - ActualTokens, reported RAW (may be
	// negative). It is 0 when the baseline is unavailable. NOT clamped here — the
	// anti-gaming cap is a ledger concern (SW-020).
	SavingsTokens int `json:"savings_tokens"`
	// BaselineVersion is the frozen baseline-method version captured at emit time.
	// It lets prior records stay attributable to the method under which they were
	// produced even after the method changes.
	BaselineVersion string `json:"baseline_version"`
	// BaselineAvailable is false when no honest baseline could be determined
	// (empty artifacts). When false, BaselineTokens and SavingsTokens are 0.
	BaselineAvailable bool `json:"baseline_available"`
	// Artifacts is the target artifact file paths the baseline was computed from,
	// carried for auditability (the "what would otherwise have been read" set).
	Artifacts []string `json:"artifacts"`
}

// FileReader is the narrow local-only dependency the baseline computation needs.
// Implementations must read whole-file content from local disk ONLY and reject
// remote sources (local-first contract). Production uses LocalFileReader; tests
// inject an in-memory reader.
type FileReader interface {
	ReadFile(path string) ([]byte, error)
}
