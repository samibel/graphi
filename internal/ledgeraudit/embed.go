package ledgeraudit

import (
	"encoding/json"
	"fmt"

	_ "embed"
)

// EmbeddedPriceSource is the locality label for the checked-in price table baked
// into the binary via go:embed. It is unambiguously local (not a path, not a
// URL), so the LocalityGuard accepts it as the sole allowlisted source.
const EmbeddedPriceSource = "(embedded)"

//go:embed pricetable.json
var embeddedPriceTable []byte

// LoadEmbeddedPriceTable loads the checked-in, local price table baked into the
// binary. This is the production pricing source for the audit: no filesystem
// path, no network — fully hermetic.
func LoadEmbeddedPriceTable() (*PriceTable, error) {
	var pt PriceTable
	if err := json.Unmarshal(embeddedPriceTable, &pt); err != nil {
		return nil, fmt.Errorf("pricing: parse embedded table: %w", err)
	}
	if pt.Version == "" {
		return nil, fmt.Errorf("pricing: embedded table missing version stamp")
	}
	return &pt, nil
}
