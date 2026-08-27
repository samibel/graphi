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

	"github.com/samibel/graphi/engine/extpack"
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
//
// SW-229: after the project config, the repository's installed and ENABLED
// declarative rule packs (engine/extpack) are applied. The two layers are
// deliberately NOT the same kind of thing:
//
//   - The project's own .graphi/taint.json may REPLACE a built-in definition.
//     It is written by whoever owns the repository, and retuning a default is
//     the point of having it.
//   - A rule pack may only ADD. A pack that claims an id a built-in or the
//     project already owns is refused with registry.ErrUnsupportedOverride —
//     ADR 0013 threat T5, and the sentinel SW-222 reserved for exactly this.
//
// With no pack installed this function returns the pre-pack config byte for
// byte, which is the rollback contract ADR 0013 §4.1 makes tier A owe.
func LoadConfig(root string) (Config, error) {
	base := DefaultConfig()
	path := filepath.Join(root, ConfigDir, ConfigFile)
	data, present, err := readProjectConfig(root)
	if err != nil {
		return Config{}, fmt.Errorf("taint: read %s: %w", path, err)
	}
	cfg := base
	if present {
		cfg, err = mergeProjectConfig(base, path, data)
		if err != nil {
			return Config{}, err
		}
	}
	return applyPacks(root, cfg)
}

// applyPacks merges the repository's enabled rule packs into cfg.
//
// It returns cfg UNCHANGED — same value, same ContentHash, including the empty
// one — when no pack is enabled. That identity is the whole reason this function
// takes the early return rather than always recomputing: recomputing would
// produce the same bytes today and would be one refactor away from not doing so.
func applyPacks(root string, cfg Config) (Config, error) {
	set, err := extpack.Load(root)
	if err != nil {
		return Config{}, fmt.Errorf("taint: rule packs: %w", err)
	}
	if set.Empty() || (len(set.TaintSources())+len(set.TaintSinks())+len(set.TaintSanitizers())) == 0 {
		return cfg, nil
	}
	owned := map[string]struct{}{}
	for _, d := range cfg.Sources {
		owned[d.ID] = struct{}{}
	}
	for _, d := range cfg.Sinks {
		owned[d.ID] = struct{}{}
	}
	for _, d := range cfg.Sanitizers {
		owned[d.ID] = struct{}{}
	}
	claim := func(id string) error {
		if _, exists := owned[id]; exists {
			return extpack.RefuseOverride("taint definition", id, "graphi or this repository's .graphi/taint.json")
		}
		owned[id] = struct{}{}
		return nil
	}

	out := cfg
	out.Sources = append([]SourceDef(nil), cfg.Sources...)
	out.Sinks = append([]SinkDef(nil), cfg.Sinks...)
	out.Sanitizers = append([]SanitizerDef(nil), cfg.Sanitizers...)
	for _, d := range set.TaintSources() {
		if err := claim(d.ID); err != nil {
			return Config{}, fmt.Errorf("taint: rule packs: %w", err)
		}
		ref := d.Pack
		out.Sources = append(out.Sources, SourceDef{
			ID: d.ID, Label: d.Label,
			NodeKinds: append([]string(nil), d.NodeKinds...), NamePatterns: append([]string(nil), d.NamePatterns...),
			Pack: &ref,
		})
	}
	for _, d := range set.TaintSinks() {
		if err := claim(d.ID); err != nil {
			return Config{}, fmt.Errorf("taint: rule packs: %w", err)
		}
		ref := d.Pack
		out.Sinks = append(out.Sinks, SinkDef{
			ID: d.ID, Category: d.Category,
			NodeKinds: append([]string(nil), d.NodeKinds...), NamePatterns: append([]string(nil), d.NamePatterns...),
			Pack: &ref,
		})
	}
	for _, d := range set.TaintSanitizers() {
		if err := claim(d.ID); err != nil {
			return Config{}, fmt.Errorf("taint: rule packs: %w", err)
		}
		ref := d.Pack
		out.Sanitizers = append(out.Sanitizers, SanitizerDef{
			ID: d.ID, NamePatterns: append([]string(nil), d.NamePatterns...),
			RemoveLabels: append([]string(nil), d.RemoveLabels...),
			Pack:         &ref,
		})
	}
	out.Packs = set.Refs()
	if err := out.Validate(); err != nil {
		return Config{}, fmt.Errorf("taint: invalid config after merging rule packs: %w", err)
	}
	out.ContentHash = computeConfigHash(out)
	return out, nil
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
	// Pack provenance is minted by the host, never declared by the thing it
	// describes. A repository that could write `packs` into its own taint config
	// could attribute its findings to a pack it never installed — provenance a
	// repository can write for itself is not provenance (ADR 0013 D5.2, and the
	// same shape as ADR 0006's "a surface may consume facts but must not mint
	// them"). Same for the per-definition `pack` stamp.
	if len(overlay.Packs) > 0 {
		return Config{}, fmt.Errorf("taint: %s declares `packs`: pack provenance is recorded by graphi from the "+
			"verified pack lockfile and cannot be set by a project config", path)
	}
	for _, d := range overlay.Sources {
		if d.Pack != nil {
			return Config{}, fmt.Errorf("taint: %s source %q declares `pack`: pack provenance cannot be set by a project config", path, d.ID)
		}
	}
	for _, d := range overlay.Sinks {
		if d.Pack != nil {
			return Config{}, fmt.Errorf("taint: %s sink %q declares `pack`: pack provenance cannot be set by a project config", path, d.ID)
		}
	}
	for _, d := range overlay.Sanitizers {
		if d.Pack != nil {
			return Config{}, fmt.Errorf("taint: %s sanitizer %q declares `pack`: pack provenance cannot be set by a project config", path, d.ID)
		}
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
// SW-229 extends it to the pack set for exactly the same reason: an installed,
// enabled pack is part of what the persisted findings MEAN, so installing,
// disabling or removing one must re-certify with a cold pass. A repository with
// neither a project config nor a pack still returns "" — the pre-pack stamp,
// unchanged, so no existing warm start is invalidated by the upgrade.
func ConfigFingerprint(root string) string {
	path := filepath.Join(root, ConfigDir, ConfigFile)
	data, present, err := readProjectConfig(root)
	if err != nil {
		return "unreadable"
	}
	cfg := DefaultConfig()
	if present {
		cfg, err = mergeProjectConfig(DefaultConfig(), path, data)
		if err != nil {
			return "invalid"
		}
	}
	packed, err := applyPacks(root, cfg)
	if err != nil {
		return "invalid"
	}
	return packed.ContentHash
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
