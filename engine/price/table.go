package price

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Load parses the embedded price table (the sole, checked-in pricing source).
// It fail-fasts on a malformed table: missing version, empty models, negative
// rates, or invalid JSON all yield an explicit error — the package never
// silently uses stale or partial rates.
func Load() (*PriceTable, error) {
	return parsePrices(embeddedPrices)
}

// LoadFromBytes parses a caller-supplied table (used by tests and custom
// deployments). The bytes must be a complete, valid, version-stamped table; the
// same validation as Load applies.
func LoadFromBytes(data []byte) (*PriceTable, error) {
	return parsePrices(data)
}

// LoadFromPath parses a table from a local file path only. It rejects remote
// sources (http/https) so pricing can only come from a local checked-in file.
func LoadFromPath(path string) (*PriceTable, error) {
	if isRemoteSource(path) {
		return nil, fmt.Errorf("price: non-local source rejected (%q) — price table must be a local checked-in file", path)
	}
	return loadLocalFile(path)
}

func parsePrices(data []byte) (*PriceTable, error) {
	var pt PriceTable
	if err := json.Unmarshal(data, &pt); err != nil {
		return nil, fmt.Errorf("price: parse table: %w", err)
	}
	if err := pt.validate(); err != nil {
		return nil, err
	}
	return &pt, nil
}

// ErrMalformedTable is the typed sentinel for a table that fails validation at
// load. Callers may match with errors.Is.
var ErrMalformedTable = errors.New("price: malformed price table")

// validate enforces the fail-fast contract: a non-empty version, at least one
// model, and non-negative rates. Any violation returns a wrapped ErrMalformedTable.
func (p *PriceTable) validate() error {
	if p.Version == "" {
		return fmt.Errorf("%w: missing version stamp", ErrMalformedTable)
	}
	if len(p.Models) == 0 {
		return fmt.Errorf("%w: no models", ErrMalformedTable)
	}
	for name, r := range p.Models {
		if r.InputPerTokenMicroUSD < 0 || r.OutputPerTokenMicroUSD < 0 {
			return fmt.Errorf("%w: negative rate for model %q", ErrMalformedTable, name)
		}
	}
	return nil
}

// rateOf returns the rate for model and whether it was found. There is NO
// implicit default: an unknown model is simply absent, and the caller (Savings)
// produces an honest unpriced result.
func (p *PriceTable) rateOf(model string) (Rate, bool) {
	r, ok := p.Models[model]
	return r, ok
}

// isRemoteSource reports whether path looks like a remote URL. Pricing may only
// come from local checked-in files.
func isRemoteSource(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}
