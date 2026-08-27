package extpack

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/samibel/graphi/core/registry"
	"github.com/samibel/graphi/internal/rootfile"
)

// State-dir permissions follow graphi's privacy defaults: directories 0700,
// files 0600.
const (
	storeDirPerm  os.FileMode = 0o700
	storeFilePerm os.FileMode = 0o600
)

// Candidate is a validated, not-yet-installed pack: the manifest, both files'
// bytes, and the hashes they produced.
type Candidate struct {
	Manifest       Manifest
	ManifestSHA256 string
	ArtifactSHA256 string

	manifestBytes []byte
	artifactBytes []byte
}

// Entry renders the lockfile entry this candidate would install as.
func (c Candidate) Entry(enabled bool) LockEntry {
	return LockEntry{
		ID:             c.Manifest.ID,
		Version:        c.Manifest.Version,
		Kind:           c.Manifest.Kind,
		ManifestSHA256: c.ManifestSHA256,
		ArtifactSHA256: c.ArtifactSHA256,
		Enabled:        enabled,
	}
}

// ValidateFile reads a pack manifest from a LOCAL file path, resolves its
// artifact next to it, and validates both — without writing anything.
//
// manifestPath is USER-supplied (a command-line argument), so following it is
// the user's own instruction and not a pack instruction. `artifact.path` is
// PACK-supplied, and is therefore resolved only as a bare file name inside the
// manifest's own directory, through internal/rootfile, which cannot be walked
// out of by a symlink or a "..".
//
// wantManifestSHA256 is optional here (empty skips the check) because `validate`
// is the verb a pack AUTHOR runs on a pack they just wrote. `install` requires
// it — see Install.
func ValidateFile(manifestPath, wantManifestSHA256 string) (Candidate, error) {
	dir := filepath.Dir(manifestPath)
	name := filepath.Base(manifestPath)

	manifestBytes, err := rootfile.Read(dir, name, MaxManifestBytes)
	if err != nil {
		var tooLarge *rootfile.TooLargeError
		if errors.As(err, &tooLarge) {
			return Candidate{}, fmt.Errorf("extpack: manifest %s is %d bytes, the limit is %d",
				manifestPath, tooLarge.Size, MaxManifestBytes)
		}
		return Candidate{}, fmt.Errorf("extpack: read manifest %s: %w", manifestPath, err)
	}
	manifestHash := HashBytes(manifestBytes)
	if wantManifestSHA256 != "" {
		if err := VerifyHash("manifest "+manifestPath, manifestBytes, wantManifestSHA256); err != nil {
			return Candidate{}, err
		}
	}

	m, err := ParseManifest(manifestBytes)
	if err != nil {
		return Candidate{}, err
	}
	if err := m.Validate(); err != nil {
		return Candidate{}, err
	}

	limit := m.Limits.MaxOutputBytes
	if limit > MaxArtifactBytes {
		limit = MaxArtifactBytes
	}
	artifactBytes, err := rootfile.Read(dir, m.Artifact.Path, limit)
	if err != nil {
		var tooLarge *rootfile.TooLargeError
		if errors.As(err, &tooLarge) {
			return Candidate{}, fmt.Errorf("extpack: artifact %q is %d bytes but the pack declares limits.max_output_bytes = %d: "+
				"a pack that ships more than it declared is refused", Bound(m.Artifact.Path), tooLarge.Size, m.Limits.MaxOutputBytes)
		}
		return Candidate{}, fmt.Errorf("extpack: read artifact %q next to %s: %w", Bound(m.Artifact.Path), manifestPath, err)
	}
	if err := VerifyHash(fmt.Sprintf("artifact %q", Bound(m.Artifact.Path)), artifactBytes, m.Artifact.SHA256); err != nil {
		return Candidate{}, err
	}
	p, err := decodePayload(m.Kind, artifactBytes)
	if err != nil {
		return Candidate{}, err
	}
	if err := checkProvides(m, p); err != nil {
		return Candidate{}, err
	}
	return Candidate{
		Manifest:       m,
		ManifestSHA256: manifestHash,
		ArtifactSHA256: m.Artifact.SHA256,
		manifestBytes:  manifestBytes,
		artifactBytes:  artifactBytes,
	}, nil
}

// ErrHashRequired is returned when Install is called without a pinned hash.
var ErrHashRequired = errors.New("extpack: install requires the manifest sha256")

// Install validates a local pack file and, ONLY if everything verifies, writes
// it into the repository's pack store and records it in the lockfile.
//
// The ordering is the security property, and it is why validation and writing
// are one function rather than two a caller could reorder: every read, hash
// check, schema check and collision check happens BEFORE the first byte is
// written. A pack that fails any of them leaves the store exactly as it was.
//
// wantManifestSHA256 is REQUIRED. There is no "install this and trust it"
// path — ADR 0013 T1 accepts a deferred signature only because pinning is
// mandatory from day one, and an optional pin is not a pin.
//
// Installation is entirely local: the source is a file path, and nothing here
// opens a socket. The egress canary sees no dialer because there is none.
func Install(root, manifestPath, wantManifestSHA256 string) (LockEntry, error) {
	if wantManifestSHA256 == "" {
		return LockEntry{}, ErrHashRequired
	}
	candidate, err := ValidateFile(manifestPath, wantManifestSHA256)
	if err != nil {
		return LockEntry{}, err
	}
	lock, err := LoadLock(root)
	if err != nil {
		return LockEntry{}, err
	}
	if existing, ok := lock.Find(candidate.Manifest.ID); ok {
		return LockEntry{}, registry.Errorf(registry.ErrDuplicate, registryName, "Install", existing.ID,
			"extpack: pack %q is already installed at version %q: remove it first (graphi extension remove %s)",
			Bound(existing.ID), Bound(existing.Version), Bound(existing.ID))
	}
	// Collision check against the packs ALREADY installed, before writing.
	if err := checkCapabilityCollisions(root, lock, candidate.Manifest); err != nil {
		return LockEntry{}, err
	}

	entry := candidate.Entry(true)
	next := lock.Upsert(entry)
	lockBytes, err := next.Encode()
	if err != nil {
		return LockEntry{}, err
	}

	dir := PackDir(root, candidate.Manifest.ID)
	if err := os.MkdirAll(dir, storeDirPerm); err != nil {
		return LockEntry{}, fmt.Errorf("extpack: create pack dir: %w", err)
	}
	if err := writeFile(filepath.Join(dir, manifestFileName), candidate.manifestBytes); err != nil {
		return LockEntry{}, err
	}
	if err := writeFile(filepath.Join(dir, artifactFileName), candidate.artifactBytes); err != nil {
		return LockEntry{}, err
	}
	if err := writeFile(LockPath(root), lockBytes); err != nil {
		return LockEntry{}, err
	}
	return entry, nil
}

// checkCapabilityCollisions loads the already-installed packs and refuses a
// candidate that claims a key one of them already provides.
//
// It reads the ENABLED and DISABLED entries alike: a disabled pack still owns
// its keys, because otherwise disabling a pack would open a window in which a
// second pack could take its keys and re-enabling would then fail.
func checkCapabilityCollisions(root string, lock Lock, m Manifest) error {
	owners := map[string]string{}
	entries := append([]LockEntry(nil), lock.Packs...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	for _, entry := range entries {
		installed, err := rootfile.Read(PackDir(root, entry.ID), manifestFileName, MaxManifestBytes)
		if err != nil {
			// An orphaned entry is `doctor`'s finding to report, not a reason to
			// block an unrelated install. It owns no keys we can read.
			continue
		}
		parsed, err := ParseManifest(installed)
		if err != nil {
			continue
		}
		for _, key := range parsed.Capabilities.Provides {
			if _, exists := owners[key]; !exists {
				owners[key] = entry.ID
			}
		}
	}
	for _, key := range m.Capabilities.Provides {
		if owner, exists := owners[key]; exists {
			return RefuseOverride("capability", key, "pack "+Bound(owner))
		}
	}
	return nil
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), storeDirPerm); err != nil {
		return fmt.Errorf("extpack: create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, storeFilePerm); err != nil {
		return fmt.Errorf("extpack: write %s: %w", path, err)
	}
	return nil
}

// ErrNotInstalled is returned when a verb names a pack the lockfile does not
// know.
var ErrNotInstalled = errors.New("extpack: pack is not installed")

// SetEnabled flips one pack's enabled flag and rewrites the lockfile.
//
// This is ADR 0013 §4.1's tier-A rollback, and it is deliberately a one-field
// edit to a reviewable file: no schema migration, no reindex, no rebuild. AC-8's
// "requires no schema hack" is a property of the design, not a procedure.
func SetEnabled(root, id string, enabled bool) (LockEntry, error) {
	if err := ValidateID(id); err != nil {
		return LockEntry{}, err
	}
	lock, err := LoadLock(root)
	if err != nil {
		return LockEntry{}, err
	}
	entry, ok := lock.Find(id)
	if !ok {
		return LockEntry{}, fmt.Errorf("%w: %q", ErrNotInstalled, Bound(id))
	}
	entry.Enabled = enabled
	next := lock.Upsert(entry)
	data, err := next.Encode()
	if err != nil {
		return LockEntry{}, err
	}
	if err := writeFile(LockPath(root), data); err != nil {
		return LockEntry{}, err
	}
	return entry, nil
}

// Remove deletes a pack's files and its lockfile entry.
func Remove(root, id string) error {
	if err := ValidateID(id); err != nil {
		return err
	}
	lock, err := LoadLock(root)
	if err != nil {
		return err
	}
	next, found := lock.Remove(id)
	if !found {
		return fmt.Errorf("%w: %q", ErrNotInstalled, Bound(id))
	}
	data, err := next.Encode()
	if err != nil {
		return err
	}
	// The lockfile is rewritten FIRST: after this write the pack is gone from
	// graphi's view even if the directory removal fails, and a leftover directory
	// is an orphan `doctor` reports — the reverse order could leave a lockfile
	// entry pointing at deleted files, which every load would then fail on.
	if err := writeFile(LockPath(root), data); err != nil {
		return err
	}
	if err := os.RemoveAll(PackDir(root, id)); err != nil {
		return fmt.Errorf("extpack: remove pack dir: %w", err)
	}
	return nil
}

// DiagnosisKind is the closed set of problems Diagnose reports.
type DiagnosisKind string

const (
	// DiagnosisOK: the pack is installed, enabled, and both hashes verify.
	DiagnosisOK DiagnosisKind = "ok"
	// DiagnosisDisabled: installed and intact, but not loaded.
	DiagnosisDisabled DiagnosisKind = "disabled"
	// DiagnosisHashMismatch: the stored bytes are not the bytes the lockfile
	// pins. Either the pack was edited in place or the lockfile was.
	DiagnosisHashMismatch DiagnosisKind = "hash-mismatch"
	// DiagnosisOrphaned: the lockfile names a pack whose files are missing.
	DiagnosisOrphaned DiagnosisKind = "orphaned"
	// DiagnosisInvalid: the pack's files are present and hash correctly, but no
	// longer validate against this build's schema.
	DiagnosisInvalid DiagnosisKind = "invalid"
	// DiagnosisUntracked: a directory in the store no lockfile entry claims.
	DiagnosisUntracked DiagnosisKind = "untracked"
)

// Healthy reports whether a diagnosis is one that needs no action.
func (k DiagnosisKind) Healthy() bool { return k == DiagnosisOK || k == DiagnosisDisabled }

// Diagnosis is one pack's health row.
type Diagnosis struct {
	ID     string        `json:"id"`
	Kind   DiagnosisKind `json:"kind"`
	Detail string        `json:"detail"`
	Entry  *LockEntry    `json:"entry,omitempty"`
}

// Diagnose reports the health of every installed pack plus every untracked
// directory in the store, in canonical id order.
//
// It never repairs anything. `doctor` is a read-only observer everywhere else in
// graphi, and a pack doctor that silently fixed a hash mismatch would be
// destroying the only evidence that one occurred.
func Diagnose(root string) ([]Diagnosis, error) {
	lock, err := LoadLock(root)
	if err != nil {
		return nil, err
	}
	var out []Diagnosis
	tracked := map[string]struct{}{}
	entries := append([]LockEntry(nil), lock.Packs...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	for _, entry := range entries {
		tracked[entry.ID] = struct{}{}
		e := entry
		d := Diagnosis{ID: entry.ID, Entry: &e}
		switch _, _, lerr := loadOne(root, forceEnabled(entry)); {
		case lerr == nil && entry.Enabled:
			d.Kind = DiagnosisOK
			d.Detail = fmt.Sprintf("%s, %s, enabled", Bound(entry.Version), entry.Kind)
		case lerr == nil:
			d.Kind = DiagnosisDisabled
			d.Detail = fmt.Sprintf("%s, %s, installed but disabled — graphi behaves exactly as if it were absent", Bound(entry.Version), entry.Kind)
		case errors.Is(lerr, registry.ErrMissingDependency):
			d.Kind = DiagnosisOrphaned
			d.Detail = lerr.Error()
		case isHashMismatch(lerr):
			d.Kind = DiagnosisHashMismatch
			d.Detail = lerr.Error()
		default:
			d.Kind = DiagnosisInvalid
			d.Detail = lerr.Error()
		}
		out = append(out, d)
	}
	for _, name := range storeSubdirs(root) {
		if _, ok := tracked[name]; ok {
			continue
		}
		out = append(out, Diagnosis{
			ID:     name,
			Kind:   DiagnosisUntracked,
			Detail: fmt.Sprintf("%s holds a pack directory no lockfile entry claims; nothing loads it", filepath.Join(StoreDir, Bound(name))),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// forceEnabled lets Diagnose verify a DISABLED pack's bytes without loading it
// into any answer. A disabled pack is still worth checking: "your rollback is
// intact" is exactly what a doctor should be able to say.
func forceEnabled(e LockEntry) LockEntry {
	e.Enabled = true
	return e
}

// isHashMismatch recognises the verification failure by its message, because
// VerifyHash returns a plain error rather than a sentinel — the message names
// both hashes and that is what a user needs. Diagnose classifies it so `doctor`
// can distinguish "someone edited the pack" from "the schema moved".
func isHashMismatch(err error) bool {
	return err != nil && containsSubstring(err.Error(), "sha256 mismatch")
}

func containsSubstring(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOfSubstring(haystack, needle) >= 0
}

func indexOfSubstring(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// storeSubdirs lists the directory names directly under the pack store.
func storeSubdirs(root string) []string {
	entries, err := os.ReadDir(Dir(root))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}
