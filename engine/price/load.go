package price

import (
	"fmt"
	"os"
)

// loadLocalFile reads and parses a price table from a local file path. It is
// the on-disk counterpart to the embedded Load() and applies identical
// validation.
func loadLocalFile(path string) (*PriceTable, error) {
	data, err := os.ReadFile(path) //nolint:gosec // local-first pricing loader reading a checked-in path
	if err != nil {
		return nil, fmt.Errorf("price: read %s: %w", path, err)
	}
	return parsePrices(data)
}
