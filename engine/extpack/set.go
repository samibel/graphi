package extpack

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/samibel/graphi/core/registry"
	"github.com/samibel/graphi/internal/rootfile"
)

// CollisionPolicy is this package's DECLARED collision rule: first-wins, with no
// sanctioned override path.
//
// It is stricter than several of the registries a pack's data eventually reaches
// (core/parse is last-wins so an opt-in CGO grammar may supersede a stdlib
// default), and that is the point: reaching a seam THROUGH a pack must not be
// able to shadow a built-in. ADR 0013 threat T5, stated as a constant rather than
// as a comment.
const CollisionPolicy = registry.PolicyFirstWins

// registryName is the short name the typed lifecycle errors carry.
const registryName = "extpack"

// RefuseOverride produces the ErrUnsupportedOverride-typed refusal for a pack
// trying to take a capability key somebody already owns.
//
// It is exported because the refusal must also be reachable from the CONSUMERS:
// this package knows that two packs collide with each other, but only
// engine/analysis/taint knows the ids of the built-in taint definitions, so that
// is where "a pack may not redefine a built-in" has to be enforced. Both sites
// produce the same sentinel through the same guard rather than two lookalike
// errors that only one caller knows how to match.
func RefuseOverride(subject, key, owner string) error {
	err := registry.GuardReplace(CollisionPolicy, registryName, subject, key, true)
	return fmt.Errorf("%w (%s already provides %q; a declarative pack may add capability, never take one)",
		err, owner, Bound(key))
}

// LoadedPack is one installed, hash-verified, decoded pack.
type LoadedPack struct {
	Entry    LockEntry
	Manifest Manifest
}

// Set is the merged view over every ENABLED installed pack.
//
// It is immutable once returned. Every accessor hands back a copy, so a consumer
// holding a Set cannot re-arm the rule data another consumer is about to read.
type Set struct {
	packs      []LoadedPack
	archRules  []ArchRule
	sources    []TaintSource
	sinks      []TaintSink
	sanitizers []TaintSanitizer
	refs       []Ref
}

// Empty reports whether no enabled pack contributed anything. It is the fast
// path every consumer takes in a repository with no packs, and it is what makes
// "no pack installed" cost one missing-file stat.
func (s *Set) Empty() bool { return s == nil || len(s.packs) == 0 }

// Packs returns the loaded packs in canonical (pack id) order.
func (s *Set) Packs() []LoadedPack {
	if s == nil {
		return nil
	}
	return append([]LoadedPack(nil), s.packs...)
}

// Refs returns the provenance of every enabled pack, in canonical order.
func (s *Set) Refs() []Ref {
	if s == nil {
		return nil
	}
	return append([]Ref(nil), s.refs...)
}

// ArchRules returns the merged architecture rules in canonical order.
func (s *Set) ArchRules() []ArchRule {
	if s == nil {
		return nil
	}
	return append([]ArchRule(nil), s.archRules...)
}

// TaintSources / TaintSinks / TaintSanitizers return the merged taint
// definitions in canonical order.
func (s *Set) TaintSources() []TaintSource {
	if s == nil {
		return nil
	}
	return append([]TaintSource(nil), s.sources...)
}

// TaintSinks returns the merged taint sinks in canonical order.
func (s *Set) TaintSinks() []TaintSink {
	if s == nil {
		return nil
	}
	return append([]TaintSink(nil), s.sinks...)
}

// TaintSanitizers returns the merged taint sanitizers in canonical order.
func (s *Set) TaintSanitizers() []TaintSanitizer {
	if s == nil {
		return nil
	}
	return append([]TaintSanitizer(nil), s.sanitizers...)
}

// Fingerprint is a deterministic short digest of the enabled pack set — the
// empty string when no pack is enabled.
//
// The empty-string case is load-bearing, not a convenience: consumers stamp this
// into cache keys, and a repository with no packs must produce the EXACT stamp
// it produced before packs existed. A fingerprint of "no packs" that was not ""
// would invalidate every warm start in the world on upgrade.
func (s *Set) Fingerprint() string {
	if s.Empty() {
		return ""
	}
	var b strings.Builder
	for _, r := range s.refs {
		b.WriteString(r.ID)
		b.WriteByte('@')
		b.WriteString(r.Version)
		b.WriteByte(':')
		b.WriteString(r.SHA256)
		b.WriteByte('\n')
	}
	return HashBytes([]byte(b.String()))[:16]
}

// Load reads, verifies and merges every ENABLED pack recorded in the lockfile
// under root.
//
// It is fail-closed throughout. A hash mismatch, a manifest that no longer
// validates, an artifact larger than the pack's own declared limit, or two packs
// claiming one capability key all return an error; none of them degrades to
// "load what works". A pack set that cannot be loaded completely is a pack set
// whose effect on an answer nobody can state, and an answer nobody can state is
// worse than a refusal.
//
// A disabled entry is skipped BEFORE any file is opened, so a disabled pack
// cannot fail a load either.
func Load(root string) (*Set, error) {
	lock, err := LoadLock(root)
	if err != nil {
		return nil, err
	}
	return LoadFromLock(root, lock)
}

// LoadFromLock is Load over an already-read lockfile.
func LoadFromLock(root string, lock Lock) (*Set, error) {
	s := &Set{}
	// Canonical merge order: by pack id, from the lockfile CONTENT. Install order
	// is not an input, which is why permuting it cannot change the result.
	entries := append([]LockEntry(nil), lock.Packs...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })

	owners := map[string]string{}
	for _, entry := range entries {
		if !entry.Enabled {
			continue
		}
		loaded, p, lerr := loadOne(root, entry)
		if lerr != nil {
			return nil, lerr
		}
		ref := entry.Ref()
		for _, key := range loaded.Manifest.Capabilities.Provides {
			if owner, exists := owners[key]; exists {
				return nil, RefuseOverride("capability", key, "pack "+Bound(owner))
			}
			owners[key] = entry.ID
		}
		s.packs = append(s.packs, loaded)
		s.refs = append(s.refs, ref)
		s.absorb(ref, p)
	}
	s.sortAll()
	return s, nil
}

// loadOne verifies and decodes one installed pack.
func loadOne(root string, entry LockEntry) (LoadedPack, payload, error) {
	if err := ValidateID(entry.ID); err != nil {
		return LoadedPack{}, payload{}, err
	}
	dir := PackDir(root, entry.ID)
	rel := filepath.Join(StoreDir, entry.ID)

	manifestBytes, err := rootfile.Read(dir, manifestFileName, MaxManifestBytes)
	if err != nil {
		// A lockfile row whose files are gone is a missing dependency, in the
		// shared vocabulary rather than a bespoke error: `doctor` matches it to
		// report an orphaned entry.
		return LoadedPack{}, payload{}, registry.Errorf(registry.ErrMissingDependency, registryName, "Load", entry.ID,
			"extpack: pack %q is in the lockfile but %s is unreadable: %v", Bound(entry.ID), filepath.Join(rel, manifestFileName), err)
	}
	if err := VerifyHash(fmt.Sprintf("installed pack %q manifest", Bound(entry.ID)), manifestBytes, entry.ManifestSHA256); err != nil {
		return LoadedPack{}, payload{}, err
	}
	m, err := ParseManifest(manifestBytes)
	if err != nil {
		return LoadedPack{}, payload{}, err
	}
	if err := m.Validate(); err != nil {
		return LoadedPack{}, payload{}, err
	}
	if m.ID != entry.ID || m.Version != entry.Version || m.Kind != entry.Kind {
		return LoadedPack{}, payload{}, fmt.Errorf(
			"extpack: lockfile records pack %q version %q kind %q, the installed manifest says %q/%q/%q",
			Bound(entry.ID), Bound(entry.Version), Bound(string(entry.Kind)),
			Bound(m.ID), Bound(m.Version), Bound(string(m.Kind)))
	}
	if m.Artifact.SHA256 != entry.ArtifactSHA256 {
		return LoadedPack{}, payload{}, fmt.Errorf(
			"extpack: lockfile pins pack %q artifact to %s, the installed manifest pins %s",
			Bound(entry.ID), entry.ArtifactSHA256, m.Artifact.SHA256)
	}

	artifactBytes, err := readArtifact(dir, rel, m)
	if err != nil {
		return LoadedPack{}, payload{}, err
	}
	if err := VerifyHash(fmt.Sprintf("installed pack %q artifact", Bound(entry.ID)), artifactBytes, m.Artifact.SHA256); err != nil {
		return LoadedPack{}, payload{}, err
	}
	p, err := decodePayload(m.Kind, artifactBytes)
	if err != nil {
		return LoadedPack{}, payload{}, err
	}
	if err := checkProvides(m, p); err != nil {
		return LoadedPack{}, payload{}, err
	}
	return LoadedPack{Entry: entry, Manifest: m}, p, nil
}

// readArtifact reads a pack's artifact under the pack's OWN declared limit.
//
// The limit binds its author: `limits.max_output_bytes` is a promise the pack
// made, and a pack that ships more than it promised is refused. The host ceiling
// (MaxArtifactBytes) is enforced separately, at manifest validation, so a pack
// cannot declare its way past it either.
func readArtifact(dir, rel string, m Manifest) ([]byte, error) {
	limit := m.Limits.MaxOutputBytes
	if limit > MaxArtifactBytes {
		limit = MaxArtifactBytes
	}
	data, err := rootfile.Read(dir, artifactFileName, limit)
	if err != nil {
		var tooLarge *rootfile.TooLargeError
		if errors.As(err, &tooLarge) {
			return nil, fmt.Errorf("extpack: pack %q artifact is %d bytes but the pack declares limits.max_output_bytes = %d: "+
				"a pack that ships more than it declared is refused", Bound(m.ID), tooLarge.Size, m.Limits.MaxOutputBytes)
		}
		return nil, fmt.Errorf("extpack: read %s: %w", filepath.Join(rel, artifactFileName), err)
	}
	return data, nil
}

// checkProvides binds capabilities.provides to what the artifact actually
// defines, in BOTH directions.
//
// A manifest that under-declares would let a pack contribute a rule nobody
// approved; a manifest that over-declares would let a pack reserve capability
// keys it does not implement, so a later, honest pack could not claim them. The
// field is the pack's statement of intent, and a statement that does not have to
// be true is decoration.
func checkProvides(m Manifest, p payload) error {
	declared := append([]string(nil), m.Capabilities.Provides...)
	sort.Strings(declared)
	actual := p.provides(m.Kind)
	if len(declared) != len(actual) {
		return provideMismatch(m, declared, actual)
	}
	for i := range declared {
		if declared[i] != actual[i] {
			return provideMismatch(m, declared, actual)
		}
	}
	return nil
}

func provideMismatch(m Manifest, declared, actual []string) error {
	missing := diffKeys(declared, actual)
	extra := diffKeys(actual, declared)
	var b strings.Builder
	fmt.Fprintf(&b, "extpack: pack %q capabilities.provides does not match its artifact", Bound(m.ID))
	if len(missing) > 0 {
		fmt.Fprintf(&b, "; declared but not defined: %s", boundList(missing))
	}
	if len(extra) > 0 {
		fmt.Fprintf(&b, "; defined but not declared: %s", boundList(extra))
	}
	return fmt.Errorf("%s", b.String())
}

// maxListedKeys bounds how many pack-controlled keys one error message quotes.
const maxListedKeys = 8

func boundList(keys []string) string {
	shown := keys
	suffix := ""
	if len(shown) > maxListedKeys {
		shown = shown[:maxListedKeys]
		suffix = fmt.Sprintf(" (+%d more)", len(keys)-maxListedKeys)
	}
	out := make([]string, 0, len(shown))
	for _, k := range shown {
		out = append(out, Bound(k))
	}
	return strings.Join(out, ", ") + suffix
}

func diffKeys(a, b []string) []string {
	in := make(map[string]struct{}, len(b))
	for _, v := range b {
		in[v] = struct{}{}
	}
	var out []string
	for _, v := range a {
		if _, ok := in[v]; !ok {
			out = append(out, v)
		}
	}
	return out
}

// absorb copies one pack's decoded payload into the set, stamping provenance.
func (s *Set) absorb(ref Ref, p payload) {
	for _, r := range p.arch.Rules {
		r.Pack = ref
		s.archRules = append(s.archRules, r)
	}
	for _, d := range p.taint.Sources {
		d.Pack = ref
		s.sources = append(s.sources, d)
	}
	for _, d := range p.taint.Sinks {
		d.Pack = ref
		s.sinks = append(s.sinks, d)
	}
	for _, d := range p.taint.Sanitizers {
		d.Pack = ref
		s.sanitizers = append(s.sanitizers, d)
	}
}

// sortAll pins every emitted list to (pack id, item id) order, so the merged
// result is a function of the lockfile content alone.
func (s *Set) sortAll() {
	sort.SliceStable(s.archRules, func(i, j int) bool {
		return lessByPackThenID(s.archRules[i].Pack.ID, s.archRules[i].ID, s.archRules[j].Pack.ID, s.archRules[j].ID)
	})
	sort.SliceStable(s.sources, func(i, j int) bool {
		return lessByPackThenID(s.sources[i].Pack.ID, s.sources[i].ID, s.sources[j].Pack.ID, s.sources[j].ID)
	})
	sort.SliceStable(s.sinks, func(i, j int) bool {
		return lessByPackThenID(s.sinks[i].Pack.ID, s.sinks[i].ID, s.sinks[j].Pack.ID, s.sinks[j].ID)
	})
	sort.SliceStable(s.sanitizers, func(i, j int) bool {
		return lessByPackThenID(s.sanitizers[i].Pack.ID, s.sanitizers[i].ID, s.sanitizers[j].Pack.ID, s.sanitizers[j].ID)
	})
}

func lessByPackThenID(packA, idA, packB, idB string) bool {
	if packA != packB {
		return packA < packB
	}
	return idA < idB
}
