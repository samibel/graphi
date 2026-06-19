package graphstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/samibel/graphi/core/model"
)

// SnapshotFormatVersion versions the portable snapshot envelope produced by this
// package. It is independent of the model's own serialization version
// (ModelSchemaVersion below) so the container format can evolve separately from
// the record encoding. Bump this only when the envelope layout changes.
const SnapshotFormatVersion = 1

// ModelSchemaVersion records the core/model identity schema the snapshot was
// produced against. Load rejects snapshots whose model schema it cannot honor,
// failing closed rather than silently reinterpreting IDs.
const ModelSchemaVersion = model.IdentitySchemaVersion

// snapshotMagic identifies graphi graphstore snapshots and lets Load reject
// unrelated files early.
const snapshotMagic = "graphi.graphstore.snapshot"

// Errors specific to the snapshot/load trust boundary. All are fail-closed: when
// any is returned, the target store is left unmodified.
var (
	// ErrSnapshotFormat is returned for an unknown/incompatible format version,
	// wrong magic, or unsupported model schema version.
	ErrSnapshotFormat = errors.New("graphstore: incompatible snapshot format")

	// ErrSnapshotMalformed is returned for malformed or truncated snapshot
	// content (e.g. invalid JSON, failed model re-validation).
	ErrSnapshotMalformed = errors.New("graphstore: malformed snapshot")

	// ErrUnsafePath is returned when a snapshot/load path escapes its intended
	// directory via traversal ("..") or a symlink.
	ErrUnsafePath = errors.New("graphstore: unsafe snapshot path")
)

// snapshotEnvelope is the portable, versioned container. It carries an explicit
// magic + format_version + model_schema_version header and the canonical
// model.Graph bytes. The graph payload is itself canonically sorted (nodes by
// NodeId, edges by EdgeId) by model.Graph.Marshal, so two snapshots of the same
// logical state are byte-identical. The FTS index is intentionally NOT stored: it
// is re-derived on load.
type snapshotEnvelope struct {
	Magic              string          `json:"magic"`
	FormatVersion      int             `json:"format_version"`
	ModelSchemaVersion uint32          `json:"model_schema_version"`
	Graph              json.RawMessage `json:"graph"`
}

// encodeSnapshot builds the deterministic snapshot bytes for the given nodes and
// edges. Insertion order is irrelevant: the inner model.Graph.Marshal sorts
// canonically, and the envelope's field order is fixed by struct layout.
func encodeSnapshot(nodes []model.Node, edges []model.Edge) ([]byte, error) {
	// Defensive canonical sort up-front keeps the payload deterministic even if a
	// backend hands us records in an arbitrary order; model.Graph.Marshal sorts
	// again, but doing it here makes the contract explicit.
	ns := make([]model.Node, len(nodes))
	copy(ns, nodes)
	sort.Slice(ns, func(i, j int) bool { return ns[i].ID() < ns[j].ID() })
	es := make([]model.Edge, len(edges))
	copy(es, edges)
	sort.Slice(es, func(i, j int) bool { return es[i].ID() < es[j].ID() })

	graphBytes, err := model.NewGraph(ns, es).Marshal()
	if err != nil {
		return nil, fmt.Errorf("graphstore: marshal snapshot graph: %w", err)
	}

	env := snapshotEnvelope{
		Magic:              snapshotMagic,
		FormatVersion:      SnapshotFormatVersion,
		ModelSchemaVersion: ModelSchemaVersion,
		Graph:              json.RawMessage(graphBytes),
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(env); err != nil {
		return nil, fmt.Errorf("graphstore: marshal snapshot envelope: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// decodeSnapshot validates and parses snapshot bytes into a model.Graph. It fails
// closed: an unknown magic/version yields ErrSnapshotFormat, and malformed
// content or failed re-validation yields ErrSnapshotMalformed. The model layer
// re-derives and re-validates every node/edge ID, so provenance and identity are
// guaranteed intact (and the null/empty/populated evidence distinction is
// preserved by the model round-trip).
func decodeSnapshot(data []byte) (model.Graph, error) {
	var env snapshotEnvelope
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&env); err != nil {
		return model.Graph{}, fmt.Errorf("%w: %v", ErrSnapshotMalformed, err)
	}
	if env.Magic != snapshotMagic {
		return model.Graph{}, fmt.Errorf("%w: bad magic %q", ErrSnapshotFormat, env.Magic)
	}
	if env.FormatVersion != SnapshotFormatVersion {
		return model.Graph{}, fmt.Errorf("%w: format_version %d (want %d)", ErrSnapshotFormat, env.FormatVersion, SnapshotFormatVersion)
	}
	if env.ModelSchemaVersion != ModelSchemaVersion {
		return model.Graph{}, fmt.Errorf("%w: model_schema_version %d (want %d)", ErrSnapshotFormat, env.ModelSchemaVersion, ModelSchemaVersion)
	}
	if len(env.Graph) == 0 {
		return model.Graph{}, fmt.Errorf("%w: empty graph payload", ErrSnapshotMalformed)
	}
	g, err := model.Unmarshal(env.Graph)
	if err != nil {
		return model.Graph{}, fmt.Errorf("%w: %v", ErrSnapshotMalformed, err)
	}
	if err := g.Validate(); err != nil {
		return model.Graph{}, fmt.Errorf("%w: %v", ErrSnapshotMalformed, err)
	}
	return g, nil
}

// safeSnapshotPath validates a snapshot/load path against directory traversal and
// symlink escape. The returned path is cleaned and absolute. The rule: after
// resolving symlinks of the existing parent chain, the target must remain inside
// its own cleaned parent directory and must contain no ".." segment. This upholds
// the local security invariant (path/name sanitizing) for snapshot/load.
func safeSnapshotPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%w: empty path", ErrUnsafePath)
	}
	// Reject any traversal segment outright; cleaning alone could mask intent.
	for _, seg := range strings.Split(filepath.ToSlash(path), "/") {
		if seg == ".." {
			return "", fmt.Errorf("%w: traversal segment in %q", ErrUnsafePath, path)
		}
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnsafePath, err)
	}
	dir := filepath.Dir(abs)
	// Resolve symlinks on the parent directory (the file itself may not exist yet
	// for Snapshot). If the resolved real dir differs such that the final path no
	// longer sits within it, reject — this catches a symlinked parent escaping.
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		// Parent does not exist (or cannot be resolved). For Snapshot this can be
		// legitimate if the caller pre-created the dir; we require the parent to
		// exist so we can prove non-escape. Fail closed.
		return "", fmt.Errorf("%w: cannot resolve parent of %q: %v", ErrUnsafePath, path, err)
	}
	resolved := filepath.Join(realDir, filepath.Base(abs))
	// If the target file already exists as a symlink, resolve it and require it to
	// stay within realDir.
	if fi, lerr := os.Lstat(resolved); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
		target, rerr := filepath.EvalSymlinks(resolved)
		if rerr != nil {
			return "", fmt.Errorf("%w: cannot resolve symlink %q: %v", ErrUnsafePath, path, rerr)
		}
		if !within(realDir, target) {
			return "", fmt.Errorf("%w: symlink escapes %q", ErrUnsafePath, realDir)
		}
	}
	return resolved, nil
}

// within reports whether child is dir itself or lies beneath it.
func within(dir, child string) bool {
	rel, err := filepath.Rel(dir, child)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}

// writeFileAtomic writes data to path via a temp file in the same directory
// followed by an atomic rename. It fsyncs the temp file before rename so a crash
// cannot leave a partially-written snapshot under the final name.
func writeFileAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".snapshot-*.tmp")
	if err != nil {
		return fmt.Errorf("graphstore: create temp snapshot: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()
	if _, werr := tmp.Write(data); werr != nil {
		_ = tmp.Close()
		return fmt.Errorf("graphstore: write temp snapshot: %w", werr)
	}
	if serr := tmp.Sync(); serr != nil {
		_ = tmp.Close()
		return fmt.Errorf("graphstore: sync temp snapshot: %w", serr)
	}
	if cerr := tmp.Close(); cerr != nil {
		return fmt.Errorf("graphstore: close temp snapshot: %w", cerr)
	}
	if rerr := os.Rename(tmpName, path); rerr != nil {
		return fmt.Errorf("graphstore: rename snapshot into place: %w", rerr)
	}
	return nil
}
