package extpack

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/samibel/graphi/core/registry"
	"github.com/samibel/graphi/internal/rootfile"
)

// LockSchemaVersion versions the lockfile independently of the manifest schema.
// They change for different reasons — one is the pack contract, the other is
// graphi's record of what is installed — and versioning them together would tie
// a host-side format change to a pack-facing break.
const LockSchemaVersion = "graphi.extension.lock/v1alpha1"

// Store layout. Every name here is FIXED by graphi. No pack-supplied string is
// ever used as a path component except the pack id, which ValidateID restricts
// to a single dot-separated lowercase segment grammar before it is used.
const (
	// StoreDir is the pack store, relative to the repository root. It sits under
	// the existing .graphi/ convention (the taint config's neighbour) rather than
	// under the repo's extensions/ directory, which ADR 0013 §5 records as
	// already taken by product integrations (vscode, github-action).
	StoreDir = ".graphi/extensions"
	// LockFile records what is installed. It is meant to be committed.
	LockFile = "extensions.lock.json"
	// manifestFileName / artifactFileName are the FIXED names an installed pack's
	// two files are stored under. The manifest's own artifact.path is used once,
	// at install time, to find the artifact next to the source manifest; it is
	// never a path again.
	manifestFileName = "manifest"
	artifactFileName = "artifact"
)

// Ref is a pack's provenance: the three values ADR 0013 D5.2 requires every
// extension-produced artifact to carry.
type Ref struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

// String renders the provenance for a human-readable finding. Every component is
// length-bounded, because all three are pack-controlled text on their way into
// an artifact.
func (r Ref) String() string {
	return fmt.Sprintf("pack %s@%s sha256:%s", Bound(r.ID), Bound(r.Version), Bound(r.SHA256))
}

// LockEntry is one installed pack, as recorded in the lockfile.
type LockEntry struct {
	ID             string `json:"id"`
	Version        string `json:"version"`
	Kind           Kind   `json:"kind"`
	ManifestSHA256 string `json:"manifest_sha256"`
	ArtifactSHA256 string `json:"artifact_sha256"`
	// Enabled is the rollback switch ADR 0013 §4.1 requires. A disabled pack is
	// not loaded at all — not loaded-and-ignored — so "disabled" and "never
	// installed" are the same behaviour by construction.
	Enabled bool `json:"enabled"`
}

// Ref returns the entry's provenance reference.
func (e LockEntry) Ref() Ref { return Ref{ID: e.ID, Version: e.Version, SHA256: e.ManifestSHA256} }

// Lock is the whole lockfile.
type Lock struct {
	SchemaVersion string      `json:"schema_version"`
	Packs         []LockEntry `json:"packs"`
}

// NewLock returns an empty lock at the current schema version.
func NewLock() Lock { return Lock{SchemaVersion: LockSchemaVersion, Packs: nil} }

// Dir returns the pack store directory for a repository root.
func Dir(root string) string { return filepath.Join(root, filepath.FromSlash(StoreDir)) }

// LockPath returns the lockfile path for a repository root.
func LockPath(root string) string { return filepath.Join(Dir(root), LockFile) }

// PackDir returns the directory one installed pack occupies. The id is validated
// by the caller (Install and LoadLock both do) before it reaches this function.
func PackDir(root, id string) string { return filepath.Join(Dir(root), id) }

// Encode serializes the lock canonically: entries sorted by id, two-space
// indentation, one trailing newline. Byte-stability is the point — the lockfile
// is a git artifact, and a file that reorders itself between runs is a file
// nobody can review.
func (l Lock) Encode() ([]byte, error) {
	out := Lock{SchemaVersion: l.SchemaVersion, Packs: append([]LockEntry(nil), l.Packs...)}
	if out.SchemaVersion == "" {
		out.SchemaVersion = LockSchemaVersion
	}
	sort.Slice(out.Packs, func(i, j int) bool { return out.Packs[i].ID < out.Packs[j].ID })
	if out.Packs == nil {
		out.Packs = []LockEntry{}
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("extpack: encode lockfile: %w", err)
	}
	return append(data, '\n'), nil
}

// Find returns the entry for id and whether it exists.
func (l Lock) Find(id string) (LockEntry, bool) {
	for _, e := range l.Packs {
		if e.ID == id {
			return e, true
		}
	}
	return LockEntry{}, false
}

// Upsert inserts or replaces an entry, keeping the slice sorted by id.
func (l Lock) Upsert(e LockEntry) Lock {
	out := Lock{SchemaVersion: LockSchemaVersion}
	replaced := false
	for _, existing := range l.Packs {
		if existing.ID == e.ID {
			out.Packs = append(out.Packs, e)
			replaced = true
			continue
		}
		out.Packs = append(out.Packs, existing)
	}
	if !replaced {
		out.Packs = append(out.Packs, e)
	}
	sort.Slice(out.Packs, func(i, j int) bool { return out.Packs[i].ID < out.Packs[j].ID })
	return out
}

// Remove drops the entry for id and reports whether one was there.
func (l Lock) Remove(id string) (Lock, bool) {
	out := Lock{SchemaVersion: LockSchemaVersion}
	found := false
	for _, existing := range l.Packs {
		if existing.ID == id {
			found = true
			continue
		}
		out.Packs = append(out.Packs, existing)
	}
	return out, found
}

// maxLockBytes bounds the lockfile before decoding.
const maxLockBytes int64 = 256 << 10

// LoadLock reads and validates the lockfile for a repository root. A missing
// lockfile is the empty lock and NOT an error — a repository with no packs is
// the default state, and it must cost nothing.
//
// Every other failure is fail-closed. In particular a malformed lockfile is an
// error rather than a fallback to "no packs": silently treating a corrupt
// lockfile as an empty one would turn a broken install into an invisible
// behaviour change, which is the shape of bug the disable-restores-baseline
// contract exists to make impossible to hide.
func LoadLock(root string) (Lock, error) {
	data, err := rootfile.Read(Dir(root), LockFile, maxLockBytes)
	if os.IsNotExist(err) {
		return NewLock(), nil
	}
	if err != nil {
		return Lock{}, fmt.Errorf("extpack: read %s: %w", filepath.Join(StoreDir, LockFile), err)
	}
	return ParseLock(data)
}

// ParseLock decodes and validates lockfile bytes.
func ParseLock(data []byte) (Lock, error) {
	var l Lock
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		return Lock{}, fmt.Errorf("extpack: parse lockfile: %w", err)
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Lock{}, fmt.Errorf("extpack: parse lockfile: multiple JSON values")
		}
		return Lock{}, fmt.Errorf("extpack: parse lockfile: %w", err)
	}
	if l.SchemaVersion != LockSchemaVersion {
		return Lock{}, fmt.Errorf("extpack: unsupported lockfile schema_version %q: this build writes %q",
			Bound(l.SchemaVersion), LockSchemaVersion)
	}
	seen := map[string]struct{}{}
	for _, e := range l.Packs {
		if err := ValidateID(e.ID); err != nil {
			return Lock{}, fmt.Errorf("extpack: lockfile: %w", err)
		}
		if _, exists := seen[e.ID]; exists {
			// SW-222's sentinel, not a new one: two lockfile rows claiming one
			// pack id is a duplicate registration, and callers match it the same
			// way they match every other duplicate in the tree.
			return Lock{}, registry.Errorf(registry.ErrDuplicate, "extpack", "LoadLock", e.ID,
				"extpack: lockfile lists pack %q twice", Bound(e.ID))
		}
		seen[e.ID] = struct{}{}
		if err := validateVersion(e.Version); err != nil {
			return Lock{}, fmt.Errorf("extpack: lockfile entry %q: %w", Bound(e.ID), err)
		}
		if err := ValidateHex(e.ManifestSHA256); err != nil {
			return Lock{}, fmt.Errorf("extpack: lockfile entry %q manifest_sha256: %w", Bound(e.ID), err)
		}
		if err := ValidateHex(e.ArtifactSHA256); err != nil {
			return Lock{}, fmt.Errorf("extpack: lockfile entry %q artifact_sha256: %w", Bound(e.ID), err)
		}
	}
	sort.Slice(l.Packs, func(i, j int) bool { return l.Packs[i].ID < l.Packs[j].ID })
	return l, nil
}
