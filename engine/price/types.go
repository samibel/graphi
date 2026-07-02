package price

// MicroUSD is the integer fixed-point USD type used for all pricing. One USD
// dollar == 1e6 MicroUSD. Using an integer type avoids floating-point rounding
// inflation entirely: the per-call computation is an exact integer multiply.
//
// Example: a model at 2500 input-per-token micro-USD ($0.0025/token) saving
// 1000 tokens yields 2_500_000 micro-USD == $2.50, computed as 1000 * 2500.
type MicroUSD = int64

// USDPerMicro is the divisor to convert MicroUSD to dollars for display only.
// Arithmetic stays in MicroUSD; conversion to a dollar string is a presentation
// concern (see FormatUSD), not a computation concern.
const USDPerMicro MicroUSD = 1_000_000

// Rate is the per-token price for one model, in micro-USD. Both fields are
// non-negative; a negative rate is rejected at load (fail-fast).
type Rate struct {
	InputPerTokenMicroUSD  MicroUSD `json:"input_per_token_microusd"`
	OutputPerTokenMicroUSD MicroUSD `json:"output_per_token_microusd"`
}

// PriceTable is the checked-in, version-stamped price table. Version is a
// required, non-empty stamp so every figure is reproducible/auditable against
// the exact table that produced it. Models maps a model identifier to its Rate.
type PriceTable struct {
	Version string          `json:"version"`
	Models  map[string]Rate `json:"models"`
}

// USDResult is the honest per-call USD savings figure. When Priced is false, the
// model was unknown and MicroUSD is 0 (no fabrication). TableVersion is the
// stamp of the table that produced (or declined) the figure, so the result is
// reproducible/auditable.
type USDResult struct {
	MicroUSD     MicroUSD `json:"micro_usd"`
	TableVersion string   `json:"table_version"`
	Priced       bool     `json:"priced"`
	Model        string   `json:"model"`
}
