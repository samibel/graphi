package taint

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/samibel/graphi/internal/rootfile"
)

// ConfigDir / ConfigFile locate the optional per-project taint config, relative
// to the repository root.
const (
	ConfigDir  = ".graphi"
	ConfigFile = "taint.json"
	// maxConfigSize bounds the repository-controlled semantic config before JSON
	// decoding. One MiB is intentionally generous for declarative taint rules.
	maxConfigSize int64 = 1 << 20
)

// LoadConfig returns the taint configuration for the repository rooted at root.
// When <root>/.graphi/taint.json is absent it returns the built-in DefaultConfig
// UNCHANGED (so a repo with no custom config behaves — and persists findings —
// exactly as before, byte-parity preserved). When present, the file's
// sources/sinks/sanitizers are MERGED OVER the defaults by ID: a definition
// whose ID matches a built-in one REPLACES it (so a project can retune or
// disable a default — an override with empty NamePatterns matches nothing),
// and a new ID is ADDED. The merged config is validated and stamped with a fresh
// deterministic ContentHash so its findings are keyed distinctly from the
// default's. A malformed or invalid file is a hard error (fail-closed), never a
// silent fallback to defaults.
func LoadConfig(root string) (Config, error) {
	base := DefaultConfig()
	path := filepath.Join(root, ConfigDir, ConfigFile)
	data, present, err := readProjectConfig(root)
	if err != nil {
		return Config{}, fmt.Errorf("taint: read %s: %w", path, err)
	}
	if !present {
		return base, nil
	}
	return mergeProjectConfig(base, path, data)
}

// readProjectConfig is the single filesystem boundary shared by LoadConfig and
// ConfigFingerprint. Missing remains a valid default; every other path, type,
// root-escape, replacement, and size failure is fail-closed.
func readProjectConfig(root string) ([]byte, bool, error) {
	rel := filepath.Join(ConfigDir, ConfigFile)
	data, err := rootfile.Read(root, rel, maxConfigSize)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func mergeProjectConfig(base Config, path string, data []byte) (Config, error) {
	var overlay Config
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&overlay); err != nil {
		return Config{}, fmt.Errorf("taint: parse %s: %w", path, err)
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Config{}, fmt.Errorf("taint: parse %s: multiple JSON values", path)
		}
		return Config{}, fmt.Errorf("taint: parse %s: %w", path, err)
	}
	merged := mergeConfig(base, overlay)
	if err := merged.Validate(); err != nil {
		return Config{}, fmt.Errorf("taint: invalid config after merging %s: %w", path, err)
	}
	merged.ContentHash = computeConfigHash(merged)
	return merged, nil
}

// ConfigFingerprint returns a short, stable fingerprint of the repository's
// effective taint config for the ingest warm-start semantics stamp. It is the
// empty string when <root>/.graphi/taint.json is absent — so a repo with no
// custom config keeps the EXACT warm-start decision it had before WP-09
// (byte-parity: the persisted findings are DefaultConfig's either way). When the
// file is present and valid it is the merged config's ContentHash, so adding,
// editing, or removing the file changes the stamp and re-certifies with a cold
// pass (the config is part of what the persisted findings MEAN, exactly like the
// ignore-scope fingerprint). A present-but-malformed file yields a fixed
// "invalid" sentinel; a rejected path/type/size/read returns "unreadable".
// Either forces a cold pass whose ingest then fails closed with the real error
// rather than warm-starting stale findings.
func ConfigFingerprint(root string) string {
	path := filepath.Join(root, ConfigDir, ConfigFile)
	data, present, err := readProjectConfig(root)
	if err != nil {
		return "unreadable"
	}
	if !present {
		return ""
	}
	cfg, err := mergeProjectConfig(DefaultConfig(), path, data)
	if err != nil {
		return "invalid"
	}
	return cfg.ContentHash
}

// mergeConfig overlays project definitions onto the base by ID (override same
// ID, append new ID), preserving base order then appended overlay order for
// determinism. The overlay's Version wins when set. The result's ContentHash is
// left blank (the caller stamps it).
func mergeConfig(base, overlay Config) Config {
	out := Config{Version: base.Version}
	if overlay.Version != "" {
		out.Version = overlay.Version
	}
	out.Sources = mergeSources(base.Sources, overlay.Sources)
	out.Sinks = mergeSinks(base.Sinks, overlay.Sinks)
	out.Sanitizers = mergeSanitizers(base.Sanitizers, overlay.Sanitizers)
	return out
}

func mergeSources(base, overlay []SourceDef) []SourceDef {
	idx := map[string]int{}
	out := make([]SourceDef, len(base))
	copy(out, base)
	for i, d := range out {
		idx[d.ID] = i
	}
	for _, d := range overlay {
		if i, ok := idx[d.ID]; ok {
			out[i] = d
			continue
		}
		idx[d.ID] = len(out)
		out = append(out, d)
	}
	return out
}

func mergeSinks(base, overlay []SinkDef) []SinkDef {
	idx := map[string]int{}
	out := make([]SinkDef, len(base))
	copy(out, base)
	for i, d := range out {
		idx[d.ID] = i
	}
	for _, d := range overlay {
		if i, ok := idx[d.ID]; ok {
			out[i] = d
			continue
		}
		idx[d.ID] = len(out)
		out = append(out, d)
	}
	return out
}

func mergeSanitizers(base, overlay []SanitizerDef) []SanitizerDef {
	idx := map[string]int{}
	out := make([]SanitizerDef, len(base))
	copy(out, base)
	for i, d := range out {
		idx[d.ID] = i
	}
	for _, d := range overlay {
		if i, ok := idx[d.ID]; ok {
			out[i] = d
			continue
		}
		idx[d.ID] = len(out)
		out = append(out, d)
	}
	return out
}

// computeConfigHash is a deterministic 16-hex FNV-64a over the config's content
// (with ContentHash itself zeroed). Sorting the definition IDs first makes the
// hash independent of merge/append order, so the same effective config always
// hashes the same.
func computeConfigHash(c Config) string {
	c.ContentHash = ""
	sort.Slice(c.Sources, func(i, j int) bool { return c.Sources[i].ID < c.Sources[j].ID })
	sort.Slice(c.Sinks, func(i, j int) bool { return c.Sinks[i].ID < c.Sinks[j].ID })
	sort.Slice(c.Sanitizers, func(i, j int) bool { return c.Sanitizers[i].ID < c.Sanitizers[j].ID })
	b, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	h := fnv.New64a()
	_, _ = h.Write(b)
	return fmt.Sprintf("%016x", h.Sum64())
}
