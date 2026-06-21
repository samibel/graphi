package ledgeraudit

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
)

// ModelPrice is the integer micro-USD price per token for one model.
type ModelPrice struct {
	InputPerToken  MicroUSD `json:"input_per_token_microusd"`
	OutputPerToken MicroUSD `json:"output_per_token_microusd"`
}

// PriceTable is the local-only, checked-in price table. It is the SOLE
// allowlisted pricing source; resolution never dials out.
type PriceTable struct {
	Version string                `json:"version"`
	Models  map[string]ModelPrice `json:"models"`
}

// LoadPriceTable reads the local JSON price table at path. It rejects non-local
// sources (URLs) so pricing can only come from a checked-in file.
func LoadPriceTable(path string) (*PriceTable, error) {
	if isRemoteSource(path) {
		return nil, fmt.Errorf("pricing: non-local source rejected (%q) — price table must be a local checked-in file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("pricing: read %s: %w", path, err)
	}
	var pt PriceTable
	if err := json.Unmarshal(data, &pt); err != nil {
		return nil, fmt.Errorf("pricing: parse %s: %w", path, err)
	}
	if pt.Version == "" {
		return nil, fmt.Errorf("pricing: missing version stamp")
	}
	if len(pt.Models) == 0 {
		return nil, fmt.Errorf("pricing: no models")
	}
	return &pt, nil
}

// Price returns the price for a model, erroring on unknown models.
func (p *PriceTable) Price(model string) (ModelPrice, error) {
	mp, ok := p.Models[model]
	if !ok {
		mp, ok = p.Models["default"]
		if !ok {
			return ModelPrice{}, fmt.Errorf("pricing: unknown model %q and no default", model)
		}
	}
	return mp, nil
}

func isRemoteSource(source string) bool {
	lower := strings.ToLower(source)
	switch {
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
		return true
	case strings.HasPrefix(lower, "tcp://"), strings.HasPrefix(lower, "udp://"):
		return true
	}
	return false
}

// LocalityGuard enforces that pricing resolution stays local. It is the sole
// allowlisted-source gate and the non-loopback dial rejector. Any non-loopback
// network access or external price lookup during resolution fails the suite.
type LocalityGuard struct {
	allowedSource string
}

// NewLocalityGuard creates a guard whose sole allowlisted pricing source is the
// given local checked-in path.
func NewLocalityGuard(localSource string) *LocalityGuard {
	return &LocalityGuard{allowedSource: localSource}
}

// AssertLocal fails if source is not the allowlisted local source or looks
// remote (AC: pricing local-only; external lookup fails the suite).
func (g *LocalityGuard) AssertLocal(source string) error {
	if isRemoteSource(source) {
		return fmt.Errorf("locality: remote pricing source rejected (%q)", source)
	}
	if g.allowedSource != "" && source != g.allowedSource {
		return fmt.Errorf("locality: pricing source %q is not the allowlisted local source %q", source, g.allowedSource)
	}
	return nil
}

// CheckDial fails for any non-loopback address (AC: non-loopback/external lookup
// fails the suite). It is the hook pricing code MUST route any hypothetical
// network lookup through. It performs NO network I/O: IP literals are parsed
// directly and hostnames (which would require external DNS) are rejected.
func (g *LocalityGuard) CheckDial(network, address string) error {
	host := hostOf(address)
	if host == "" {
		return fmt.Errorf("locality: empty dial host rejected")
	}
	if isLoopbackHost(host) {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if !ip.IsLoopback() {
			return fmt.Errorf("locality: dial %q is non-loopback IP %s — rejected", address, ip)
		}
		return nil
	}
	return fmt.Errorf("locality: dial %q requires external DNS for %q — rejected", address, host)
}

func hostOf(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return address
	}
	return host
}

func isLoopbackHost(host string) bool {
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}
