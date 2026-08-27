package extpack

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// SchemaVersion is the ONLY manifest schema version this build accepts.
//
// It is an exact-match check, not a range. A pack declaring an older schema is
// rejected rather than read on a best-effort basis, because "accept the old
// spelling too" is precisely how a superseded contract stays silently alive
// (standards: a renamed wire identifier must FAIL, not alias). A schema-version
// downgrade is one of this story's attack tests.
const SchemaVersion = "graphi.extension/v1alpha1"

// APIVersion is the host's rule-pack API version, as MAJOR.MINOR. A pack
// declares the closed range of host API versions it was written for; a host
// outside that range refuses the pack instead of guessing.
const APIVersion = "1.0"

// Byte budgets. Both bound repository-controlled input before it is decoded.
const (
	// MaxManifestBytes caps a manifest file. Manifests are a few dozen lines.
	MaxManifestBytes int64 = 64 << 10
	// MaxArtifactBytes caps an artifact file, and also caps what a pack may
	// declare in limits.max_output_bytes. A pack cannot raise its own ceiling.
	MaxArtifactBytes int64 = 1 << 20
)

// MaxFieldLength bounds any pack-controlled string this package emits into an
// artifact, following the engine/trust MaxPathLength convention (240 bytes with
// a visible truncation marker). Pack ids, versions and rule text are all written
// by whoever authored the pack, so they are exactly the "repository- or
// pack-controlled text" standards require to be bounded before it reaches an
// artifact.
const MaxFieldLength = 240

// truncationMarker matches engine/trust's marker so a shortened value reads the
// same wherever graphi shortens one.
const truncationMarker = "…[truncated]"

// Bound length-bounds one pack-controlled string. It is exported because the
// consumers that render pack provenance into their own artifacts must bound the
// same way this package does.
func Bound(s string) string {
	if len(s) <= MaxFieldLength {
		return s
	}
	return s[:MaxFieldLength-len(truncationMarker)] + truncationMarker
}

// Kind is the closed set of pack kinds this build understands.
type Kind string

const (
	// KindArchitectureRules declares forbidden dependency directions between
	// architecture units. Consumed by engine/agenttools/archintel.
	KindArchitectureRules Kind = "architecture-rules"
	// KindTaintRules declares additional taint sources, sinks and sanitizers.
	// Consumed by engine/analysis/taint.
	KindTaintRules Kind = "taint-rules"
)

// SupportedKinds lists the implemented kinds in canonical order.
func SupportedKinds() []Kind { return []Kind{KindArchitectureRules, KindTaintRules} }

// deferredKinds are the pack kinds ADR 0013 names for tier A that this build does
// NOT implement yet. They are listed by name so `extension validate` can tell a
// pack author "not yet" instead of "unknown", which is a materially different
// message: one is a typo, the other is a backlog entry. Each has an entry in the
// delivery portfolio's backlog (dated 2026-08-27).
var deferredKinds = []string{
	"classification-rules",
	"export-profiles",
	"framework-detection",
	"query-presets",
}

// Permission is the closed permission vocabulary a tier-A pack may request.
type Permission string

// PermissionGraphRead is the only permission a declarative pack can hold. There
// is deliberately no network, filesystem or exec permission to grant: ADR 0013
// I2 forbids an extension mechanism from introducing egress into the default
// path, and the cheapest way to keep that true is for the schema to be unable to
// express the request.
const PermissionGraphRead Permission = "graph:read"

// Determinism is the closed determinism vocabulary.
type Determinism string

// DeterminismDeterministic is the only accepted value. A pack is data merged in
// a fixed order; there is no non-deterministic tier-A pack, and a manifest
// claiming one is a manifest describing something this tier cannot host.
const DeterminismDeterministic Determinism = "deterministic"

// APIRange is the closed range of host API versions a pack supports.
type APIRange struct {
	Min string `yaml:"min" json:"min"`
	Max string `yaml:"max" json:"max"`
}

// Artifact locates and pins the pack's data file.
type Artifact struct {
	// Path is a BARE FILENAME, resolved next to the manifest at install time
	// through internal/rootfile and never used as a path afterwards. Separators,
	// parent references, absolute paths and URL schemes are all rejected.
	Path string `yaml:"path" json:"path"`
	// SHA256 is the lowercase hex SHA-256 of the artifact bytes.
	SHA256 string `yaml:"sha256" json:"sha256"`
}

// Capabilities declares what the pack contributes.
type Capabilities struct {
	// Provides is the exact set of capability keys the artifact defines. It is
	// checked against the decoded artifact, so it cannot drift into decoration.
	Provides []string `yaml:"provides" json:"provides"`
}

// Limits are the pack's self-declared bounds.
type Limits struct {
	// MaxOutputBytes bounds the artifact. A pack that ships more than it
	// declared is refused — the number binds its author, not only the host.
	MaxOutputBytes int64 `yaml:"max_output_bytes" json:"max_output_bytes"`
}

// Manifest is the graphi.extension/v1alpha1 pack manifest.
type Manifest struct {
	SchemaVersion string       `yaml:"schema_version" json:"schema_version"`
	ID            string       `yaml:"id" json:"id"`
	Version       string       `yaml:"version" json:"version"`
	Kind          Kind         `yaml:"kind" json:"kind"`
	API           APIRange     `yaml:"api" json:"api"`
	Artifact      Artifact     `yaml:"artifact" json:"artifact"`
	Capabilities  Capabilities `yaml:"capabilities" json:"capabilities"`
	Permissions   []Permission `yaml:"permissions" json:"permissions"`
	Determinism   Determinism  `yaml:"determinism" json:"determinism"`
	Limits        Limits       `yaml:"limits" json:"limits"`
}

// ParseManifest decodes manifest bytes. YAML is a superset of JSON, so one
// decoder serves both spellings the plan calls for. Unknown fields are REJECTED:
// a field this build does not understand may be the one that carries the meaning,
// and silently dropping it would let a pack mean something the validator never
// saw.
func ParseManifest(data []byte) (Manifest, error) {
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		if err == io.EOF {
			return Manifest{}, fmt.Errorf("extpack: manifest is empty")
		}
		return Manifest{}, fmt.Errorf("extpack: parse manifest: %w", err)
	}
	var trailing yaml.Node
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Manifest{}, fmt.Errorf("extpack: manifest holds more than one document")
		}
		return Manifest{}, fmt.Errorf("extpack: parse manifest: %w", err)
	}
	return m, nil
}

// Validate checks a manifest against the schema. Every failure names the field
// and what would have been acceptable, because the only way a pack author can
// fix a rejection is to be told what it was.
func (m Manifest) Validate() error {
	if m.SchemaVersion != SchemaVersion {
		if m.SchemaVersion == "" {
			return fmt.Errorf("extpack: manifest declares no schema_version; this build accepts %q", SchemaVersion)
		}
		return fmt.Errorf("extpack: unsupported schema_version %q: this build accepts %q only "+
			"(an older schema is rejected, not read best-effort)", Bound(m.SchemaVersion), SchemaVersion)
	}
	if err := ValidateID(m.ID); err != nil {
		return err
	}
	if err := validateVersion(m.Version); err != nil {
		return err
	}
	if err := m.validateKind(); err != nil {
		return err
	}
	if err := m.API.validate(); err != nil {
		return err
	}
	if err := validateArtifactPath(m.Artifact.Path); err != nil {
		return err
	}
	if err := ValidateHex(m.Artifact.SHA256); err != nil {
		return fmt.Errorf("extpack: artifact.sha256: %w", err)
	}
	if len(m.Capabilities.Provides) == 0 {
		return fmt.Errorf("extpack: capabilities.provides is empty: a pack that provides nothing has nothing to install")
	}
	seen := map[string]struct{}{}
	for _, key := range m.Capabilities.Provides {
		if err := validateCapabilityKey(m.Kind, key); err != nil {
			return err
		}
		if _, dup := seen[key]; dup {
			return fmt.Errorf("extpack: capabilities.provides lists %q twice", Bound(key))
		}
		seen[key] = struct{}{}
	}
	for _, p := range m.Permissions {
		if p != PermissionGraphRead {
			return fmt.Errorf("extpack: permission %q is not available to a declarative pack: "+
				"the only permission tier A can hold is %q (ADR 0013: no code execution, no network)",
				Bound(string(p)), PermissionGraphRead)
		}
	}
	if m.Determinism != DeterminismDeterministic {
		return fmt.Errorf("extpack: determinism %q is not accepted: a declarative pack is %q",
			Bound(string(m.Determinism)), DeterminismDeterministic)
	}
	if m.Limits.MaxOutputBytes <= 0 {
		return fmt.Errorf("extpack: limits.max_output_bytes must be a positive byte count")
	}
	if m.Limits.MaxOutputBytes > MaxArtifactBytes {
		return fmt.Errorf("extpack: limits.max_output_bytes %d exceeds the host ceiling of %d bytes: "+
			"a pack cannot raise its own limit", m.Limits.MaxOutputBytes, MaxArtifactBytes)
	}
	return nil
}

func (m Manifest) validateKind() error {
	for _, k := range SupportedKinds() {
		if m.Kind == k {
			return nil
		}
	}
	for _, k := range deferredKinds {
		if string(m.Kind) == k {
			return fmt.Errorf("extpack: pack kind %q is a planned tier-A kind that this build does not implement yet "+
				"(implemented: %s); it is booked in the delivery backlog, not a typo", k, kindList())
		}
	}
	return fmt.Errorf("extpack: unknown pack kind %q: this build implements %s", Bound(string(m.Kind)), kindList())
}

func kindList() string {
	names := make([]string, 0, len(SupportedKinds()))
	for _, k := range SupportedKinds() {
		names = append(names, string(k))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// validate checks that the host's APIVersion falls inside the declared range.
func (r APIRange) validate() error {
	lo, err := parseAPIVersion(r.Min)
	if err != nil {
		return fmt.Errorf("extpack: api.min: %w", err)
	}
	hi, err := parseAPIVersion(r.Max)
	if err != nil {
		return fmt.Errorf("extpack: api.max: %w", err)
	}
	if compareAPI(lo, hi) > 0 {
		return fmt.Errorf("extpack: api range %q..%q is empty (min is above max)", Bound(r.Min), Bound(r.Max))
	}
	host, err := parseAPIVersion(APIVersion)
	if err != nil {
		return fmt.Errorf("extpack: host api version %q is malformed: %w", APIVersion, err)
	}
	if compareAPI(host, lo) < 0 || compareAPI(host, hi) > 0 {
		return fmt.Errorf("extpack: pack requires host api %s..%s, this graphi speaks %s",
			Bound(r.Min), Bound(r.Max), APIVersion)
	}
	return nil
}

type apiVersion struct{ major, minor int }

func parseAPIVersion(s string) (apiVersion, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 2 {
		return apiVersion{}, fmt.Errorf("%q is not a MAJOR.MINOR version", Bound(s))
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil || major < 0 {
		return apiVersion{}, fmt.Errorf("%q has a non-numeric major version", Bound(s))
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil || minor < 0 {
		return apiVersion{}, fmt.Errorf("%q has a non-numeric minor version", Bound(s))
	}
	return apiVersion{major, minor}, nil
}

func compareAPI(a, b apiVersion) int {
	switch {
	case a.major != b.major:
		if a.major < b.major {
			return -1
		}
		return 1
	case a.minor != b.minor:
		if a.minor < b.minor {
			return -1
		}
		return 1
	}
	return 0
}

// maxIDLength bounds a pack id. The id is used as a DIRECTORY NAME in the pack
// store, so it is bounded and character-restricted before it ever reaches a
// filesystem call.
const maxIDLength = 64

// ValidateID checks a pack id. The grammar is dot-separated segments of
// lowercase alphanumerics and hyphens, each segment starting and ending
// alphanumeric — which makes "..", ".", a leading dot, a trailing dot, a
// separator and an absolute path all unrepresentable rather than filtered.
func ValidateID(id string) error {
	if id == "" {
		return fmt.Errorf("extpack: manifest declares no id")
	}
	if len(id) > maxIDLength {
		return fmt.Errorf("extpack: pack id is %d bytes, the limit is %d", len(id), maxIDLength)
	}
	for _, segment := range strings.Split(id, ".") {
		if segment == "" {
			return fmt.Errorf("extpack: pack id %q has an empty segment: "+
				"the grammar is dot-separated [a-z0-9-] segments starting and ending alphanumeric", Bound(id))
		}
		for i := 0; i < len(segment); i++ {
			c := segment[i]
			isAlnum := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
			if isAlnum {
				continue
			}
			if c == '-' && i > 0 && i < len(segment)-1 {
				continue
			}
			return fmt.Errorf("extpack: pack id %q is not a valid identifier: "+
				"the grammar is dot-separated [a-z0-9-] segments starting and ending alphanumeric", Bound(id))
		}
	}
	return nil
}

// maxVersionLength bounds a pack version string.
const maxVersionLength = 32

func validateVersion(v string) error {
	if v == "" {
		return fmt.Errorf("extpack: manifest declares no version: an unversioned pack cannot be pinned or diagnosed")
	}
	if len(v) > maxVersionLength {
		return fmt.Errorf("extpack: pack version is %d bytes, the limit is %d", len(v), maxVersionLength)
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case c == '.' || c == '-' || c == '+':
		default:
			return fmt.Errorf("extpack: pack version %q contains %q: versions are [0-9A-Za-z.+-]", Bound(v), string(c))
		}
	}
	return nil
}

// validateArtifactPath enforces that artifact.path is a BARE FILENAME.
//
// This is the schema half of "graphi follows no pack-supplied path". The other
// half is that the value is used exactly once, at install time, through
// internal/rootfile against the directory the USER named — and never again,
// because the installed artifact is stored under a fixed name.
func validateArtifactPath(p string) error {
	if p == "" {
		return fmt.Errorf("extpack: artifact.path is empty")
	}
	if len(p) > maxIDLength {
		return fmt.Errorf("extpack: artifact.path is %d bytes, the limit is %d", len(p), maxIDLength)
	}
	if p == "." || p == ".." {
		return fmt.Errorf("extpack: artifact.path %q is a directory reference, not a file name", p)
	}
	if strings.ContainsAny(p, `/\:`) || strings.Contains(p, "..") {
		return fmt.Errorf("extpack: artifact.path %q must be a bare file name next to the manifest — "+
			"no directory separators, no parent references, no URLs (graphi follows no pack-supplied path)", Bound(p))
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case c == '.' || c == '-' || c == '_':
		default:
			return fmt.Errorf("extpack: artifact.path %q contains %q: file names are [0-9A-Za-z._-]", Bound(p), string(c))
		}
	}
	return nil
}

// ValidateHex checks a lowercase hex SHA-256.
func ValidateHex(h string) error {
	const want = 64
	if h == "" {
		return fmt.Errorf("missing sha256")
	}
	if len(h) != want {
		return fmt.Errorf("%q is %d characters, a hex sha256 is %d", Bound(h), len(h), want)
	}
	for i := 0; i < len(h); i++ {
		c := h[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') {
			continue
		}
		return fmt.Errorf("%q is not lowercase hex", Bound(h))
	}
	return nil
}
