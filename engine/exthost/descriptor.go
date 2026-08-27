package exthost

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/samibel/graphi/engine/extpack"
	"github.com/samibel/graphi/engine/opcatalog"
	"github.com/samibel/graphi/internal/rootfile"
)

// HostAPIVersion is the extension API version this host speaks. It is
// extpack.APIVersion, not a second number: tier A and tier C are two ways to
// deliver against ONE host API, and giving the subprocess tier its own version
// line would mean a pack author and an extension author reading different
// compatibility stories out of the same graphi release.
const HostAPIVersion = extpack.APIVersion

// KindProcessAnalyzer is the descriptor kind this spike hosts: a read-only
// analyzer in a separate local process.
//
// # Why this is not an extpack.Kind
//
// extpack.Kind is a CLOSED vocabulary of tier-A pack kinds
// (architecture-rules, taint-rules, plus four named as deferred), and
// extpack.Manifest.Validate rejects anything else. Adding "process-analyzer"
// there would have edited shipped, gated code on behalf of a spike that may well
// be thrown away — and would have put an executable kind into the type whose
// whole guarantee is that it cannot execute.
//
// So the descriptor below reuses the manifest MACHINERY — the same
// schema_version string, the same field names and YAML shape, the same
// extpack.Artifact bare-filename + SHA-256 pinning, the same extpack.APIRange
// semantics, the same extpack.Capabilities, the same id/version/hex validators,
// the same MaxManifestBytes cap, the same unknown-fields-are-rejected decode and
// the same extpack.Bound length discipline — while owning the two things tier A
// cannot express: a port list and process limits.
//
// **That gap is a finding, not an accident, and it is recorded in the decision
// document as part of the packaging-effort measurement.**
const KindProcessAnalyzer = "process-analyzer"

// Ceilings a descriptor may not exceed. A descriptor binds its author; these
// bind the descriptor, so an extension cannot raise its own limits — the same
// rule extpack applies to limits.max_output_bytes.
const (
	// MaxResponseCeiling is the largest max_response_bytes a descriptor may
	// declare. It is extpack.MaxArtifactBytes (1 MiB): the two are the same
	// question — how much extension-controlled data may enter this process in
	// one go — and answering it twice would let the answers drift.
	MaxResponseCeiling = extpack.MaxArtifactBytes
	// MaxTimeoutMS is the longest wall-clock limit a descriptor may declare.
	// Sixty seconds is far above any read-only analysis and still finite, which
	// is the property that matters: T4's residual risk is a pathological
	// extension burning its whole budget on every call, and an unbounded budget
	// would turn that from slow into wedged.
	MaxTimeoutMS int64 = 60_000
)

// Descriptor is the graphi.extension/v1alpha1 descriptor for a tier-C process
// extension.
//
// Field order mirrors extpack.Manifest so the two read as one format.
type Descriptor struct {
	SchemaVersion string               `yaml:"schema_version"`
	ID            string               `yaml:"id"`
	Version       string               `yaml:"version"`
	Kind          string               `yaml:"kind"`
	API           extpack.APIRange     `yaml:"api"`
	Artifact      extpack.Artifact     `yaml:"artifact"`
	Capabilities  extpack.Capabilities `yaml:"capabilities"`
	// Ports are the host seams the extension may reach, from opcatalog's closed
	// vocabulary. This is the tier-C addition to the tier-A shape, and it is the
	// whole permission story: an extension reaches data through these and
	// through nothing else. No database path appears anywhere in this struct or
	// on the wire, which is how ADR 0013 N4 holds.
	Ports []opcatalog.Port `yaml:"ports"`
	// Permissions are the grants the ports imply. Declared anyway and re-derived
	// at validation, exactly as opcatalog.OperationSpec does it: a hand edit that
	// contradicts the ports fails to load instead of shipping.
	Permissions []opcatalog.Permission `yaml:"permissions"`
	Determinism opcatalog.Determinism  `yaml:"determinism"`
	Limits      Limits                 `yaml:"limits"`
}

// readOnlyPermissions is ADR 0013's V1 envelope (I2, I3): extensions read the
// graph and the working tree through host ports, they write nothing, and they
// leave the machine never. It is the same table engine/extpack/conformance
// enforces for tier-B contributions, restated because a tier-C descriptor is
// validated before any conformance harness sees it.
var readOnlyPermissions = map[opcatalog.Permission]bool{
	opcatalog.PermissionGraphRead:   true,
	opcatalog.PermissionSourceRead:  true,
	opcatalog.PermissionHistoryRead: true,
	opcatalog.PermissionStateRead:   true,
}

// deniedPorts are ports an extension may not declare even though the read-only
// permission envelope would admit them.
//
// This is a DENY list, and entries are only ever added. opcatalog.PortGraphStore
// carries PermissionGraphRead, so the envelope check alone would let it through
// — but it is documented as "a direct READ-ONLY open of the repository's durable
// store", and ADR 0013 N4 closes exactly that: "No extension access to SQLite
// files. Extensions reach the graph through host ports only." A permission
// vocabulary that admits a port a decision closes is a permission vocabulary
// that needs a second, explicit refusal.
var deniedPorts = map[opcatalog.Port]string{
	opcatalog.PortGraphStore: "ADR 0013 N4: extensions reach the graph through host ports, never " +
		"through a direct open of the durable store, because file access would bypass the read-only " +
		"discipline, the selective-read cost contract (ADR 0003) and the generation-binding trust " +
		"evidence depends on",
}

// ParseDescriptor decodes descriptor bytes.
//
// Unknown fields are REJECTED, for extpack.ParseManifest's reason verbatim: a
// field this build does not understand may be the one that carries the meaning,
// and dropping it silently would let a descriptor mean something the validator
// never saw.
func ParseDescriptor(data []byte) (Descriptor, error) {
	var d Descriptor
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&d); err != nil {
		if err == io.EOF {
			return Descriptor{}, fmt.Errorf("%w: descriptor is empty", ErrDescriptor)
		}
		return Descriptor{}, fmt.Errorf("%w: %v", ErrDescriptor, err)
	}
	var trailing yaml.Node
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Descriptor{}, fmt.Errorf("%w: descriptor holds more than one document", ErrDescriptor)
		}
		return Descriptor{}, fmt.Errorf("%w: %v", ErrDescriptor, err)
	}
	return d, nil
}

// Validate checks a descriptor against the schema, naming the offending field
// and what would have been acceptable.
func (d Descriptor) Validate() error {
	if d.SchemaVersion != extpack.SchemaVersion {
		return fmt.Errorf("%w: schema_version is %q, this build accepts only %q "+
			"(an older spelling is rejected, never read best-effort)",
			ErrDescriptor, extpack.Bound(d.SchemaVersion), extpack.SchemaVersion)
	}
	if d.Kind != KindProcessAnalyzer {
		return fmt.Errorf("%w: kind is %q; this host loads only %q "+
			"(tier-A pack kinds belong to extpack, which executes nothing)",
			ErrDescriptor, extpack.Bound(d.Kind), KindProcessAnalyzer)
	}
	if err := extpack.ValidateID(d.ID); err != nil {
		return fmt.Errorf("%w: id: %v", ErrDescriptor, err)
	}
	if d.Version == "" {
		return fmt.Errorf("%w: version is required", ErrDescriptor)
	}
	if err := d.API.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrDescriptor, err)
	}
	if err := validateArtifactName(d.Artifact.Path); err != nil {
		return err
	}
	if err := extpack.ValidateHex(d.Artifact.SHA256); err != nil {
		return fmt.Errorf("%w: artifact.sha256: %v", ErrDescriptor, err)
	}
	if len(d.Capabilities.Provides) == 0 {
		return fmt.Errorf("%w: capabilities.provides is empty; an extension that advertises no "+
			"operation can never be called, and the host would have started a process for nothing",
			ErrDescriptor)
	}
	if err := d.validatePorts(); err != nil {
		return err
	}
	if d.Determinism != opcatalog.DeterminismDeterministic {
		return fmt.Errorf("%w: determinism is %q; this spike hosts only %q, because the conformance "+
			"harness proves byte-identical repeat runs and a self-declared non-deterministic "+
			"extension could not be held to that",
			ErrDescriptor, extpack.Bound(string(d.Determinism)), opcatalog.DeterminismDeterministic)
	}
	return d.Limits.validate()
}

// validatePorts enforces the closed port vocabulary, the read-only envelope, and
// the ports → permissions derivation.
func (d Descriptor) validatePorts() error {
	if len(d.Ports) == 0 {
		return fmt.Errorf("%w: ports is empty; an extension reaches data ONLY through declared "+
			"ports, so an empty list is a descriptor for something that cannot read anything",
			ErrDescriptor)
	}
	seen := map[opcatalog.Port]bool{}
	for _, p := range d.Ports {
		if !opcatalog.ValidPort(p) || p == opcatalog.PortsUnaudited {
			return fmt.Errorf("%w: ports: %q is not a host port; the vocabulary is closed and lives "+
				"in engine/opcatalog", ErrDescriptor, extpack.Bound(string(p)))
		}
		if reason, denied := deniedPorts[p]; denied {
			return fmt.Errorf("%w: ports: %q is closed to extensions — %s",
				ErrDescriptor, p, reason)
		}
		if seen[p] {
			return fmt.Errorf("%w: ports: %q is listed twice", ErrDescriptor, extpack.Bound(string(p)))
		}
		seen[p] = true
	}
	if !sort.SliceIsSorted(d.Ports, func(i, j int) bool { return d.Ports[i] < d.Ports[j] }) {
		return fmt.Errorf("%w: ports must be sorted, so two descriptors for the same access are the "+
			"same bytes", ErrDescriptor)
	}
	derived := opcatalog.PermissionsFor(d.Ports)
	for _, perm := range derived {
		if !readOnlyPermissions[perm] {
			return fmt.Errorf("%w: ports imply permission %q, which ADR 0013 I3 closes to V1 "+
				"extensions (read-only: no writes, no egress)", ErrDescriptor, perm)
		}
	}
	if !equalPermissions(d.Permissions, derived) {
		return fmt.Errorf("%w: permissions %s do not match the set the declared ports imply (%s); "+
			"the ports are the truth and the declaration is the checked redundancy",
			ErrDescriptor, permissionList(d.Permissions), permissionList(derived))
	}
	return nil
}

func (l Limits) validate() error {
	if l.MaxResponseBytes < MinResponseLimit || l.MaxResponseBytes > MaxResponseCeiling {
		return fmt.Errorf("%w: limits.max_response_bytes is %d; it must be between %d and %d "+
			"(an extension cannot raise its own ceiling)",
			ErrDescriptor, l.MaxResponseBytes, MinResponseLimit, MaxResponseCeiling)
	}
	if l.TimeoutMS <= 0 || l.TimeoutMS > MaxTimeoutMS {
		return fmt.Errorf("%w: limits.timeout_ms is %d; it must be between 1 and %d "+
			"(there is no unbounded budget)", ErrDescriptor, l.TimeoutMS, MaxTimeoutMS)
	}
	return nil
}

// validateArtifactName enforces extpack.Artifact's rule: a BARE FILENAME,
// resolved beside the descriptor and never used as a path afterwards. For an
// executable this matters more than it does for a data file — the value decides
// what gets exec'd — so the check is stated as its own error rather than folded
// into the generic descriptor complaint.
func validateArtifactName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("%w: artifact.path is empty", ErrUnsafeBinary)
	case name == "." || name == "..":
		return fmt.Errorf("%w: artifact.path %q is a directory reference, not a file name",
			ErrUnsafeBinary, name)
	case filepath.Base(name) != name, strings.ContainsAny(name, `/\`), strings.Contains(name, ":"):
		return fmt.Errorf("%w: artifact.path %q must be a bare file name resolved beside the "+
			"descriptor; separators, parent references, absolute paths and URL schemes are refused",
			ErrUnsafeBinary, extpack.Bound(name))
	}
	return nil
}

// Loaded is a descriptor whose artifact has been READ AND VERIFIED. The type
// exists so a caller cannot hold a descriptor and an unverified path at the same
// time: Start takes one of these, and the only way to get one is LoadDescriptor.
type Loaded struct {
	Descriptor Descriptor
	// Dir is the directory the descriptor was read from — the root the artifact
	// was resolved under.
	Dir string
	// BinaryPath is the verified executable's absolute path.
	BinaryPath string
	// DescriptorSHA256 is the hash of the descriptor bytes themselves, so a
	// provenance record can name the document as well as the executable.
	DescriptorSHA256 string
}

// LoadDescriptor reads, validates and HASH-VERIFIES an extension at path.
//
// The ordering is the security-relevant part and is asserted by the attack
// tests: the artifact's SHA-256 is checked here, in Load, and Start does not
// spawn anything Load did not return. An altered executable is therefore never
// executed — not merely never trusted.
//
// path must name a REGULAR FILE the caller chose. A directory is refused rather
// than searched: this package performs no discovery (ADR 0013 N2).
func LoadDescriptor(path string) (Loaded, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Loaded{}, fmt.Errorf("%w: %v", ErrDescriptor, err)
	}
	if info.IsDir() {
		return Loaded{}, fmt.Errorf("%w: %q is a directory; a descriptor is one file the caller "+
			"named, and this host never searches a directory for extensions",
			ErrDescriptor, extpack.Bound(path))
	}
	if !info.Mode().IsRegular() {
		return Loaded{}, fmt.Errorf("%w: %q is not a regular file", ErrDescriptor, extpack.Bound(path))
	}
	if info.Size() > extpack.MaxManifestBytes {
		return Loaded{}, fmt.Errorf("%w: descriptor is %d bytes, the limit is %d",
			ErrDescriptor, info.Size(), extpack.MaxManifestBytes)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Loaded{}, fmt.Errorf("%w: %v", ErrDescriptor, err)
	}
	d, err := ParseDescriptor(raw)
	if err != nil {
		return Loaded{}, err
	}
	if err := d.Validate(); err != nil {
		return Loaded{}, err
	}

	dir := filepath.Dir(path)
	// rootfile.Read resolves a BARE NAME under an os.Root, so a symlink out of
	// the descriptor's directory cannot be followed. Reused rather than
	// reimplemented — it is the same containment extpack applies to a pack
	// artifact, and an executable deserves it at least as much.
	binary, err := rootfile.Read(dir, d.Artifact.Path, MaxArtifactBytes)
	if err != nil {
		return Loaded{}, fmt.Errorf("%w: artifact %q: %v", ErrDescriptor, extpack.Bound(d.Artifact.Path), err)
	}
	if err := extpack.VerifyHash("extension artifact", binary, d.Artifact.SHA256); err != nil {
		return Loaded{}, fmt.Errorf("%w: %v (the pinned bytes are not the bytes on disk; "+
			"nothing was executed)", ErrArtifactMismatch, err)
	}
	abs, err := filepath.Abs(filepath.Join(dir, d.Artifact.Path))
	if err != nil {
		return Loaded{}, fmt.Errorf("%w: %v", ErrDescriptor, err)
	}
	return Loaded{
		Descriptor:       d,
		Dir:              dir,
		BinaryPath:       abs,
		DescriptorSHA256: extpack.HashBytes(raw),
	}, nil
}

// MaxArtifactBytes bounds the executable this host is willing to read in order
// to hash it. 128 MiB is generous for a static Go binary (graphi's own is ~35
// MB) and still refuses a file that would be a memory problem to verify.
//
// This number is a measured cost, not a formality: verification reads the whole
// executable on every activation, and the decision document records what that
// costs in wall-clock for a real binary.
const MaxArtifactBytes int64 = 128 << 20

func equalPermissions(a, b []opcatalog.Permission) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func permissionList(p []opcatalog.Permission) string {
	if len(p) == 0 {
		return "(none)"
	}
	parts := make([]string, len(p))
	for i, v := range p {
		parts[i] = string(v)
	}
	return strings.Join(parts, ", ")
}
