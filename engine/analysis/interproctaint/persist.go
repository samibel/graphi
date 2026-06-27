package interproctaint

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis/interproc"
	"github.com/samibel/graphi/engine/analysis/taint"
	"github.com/samibel/graphi/engine/query"
)

// computeContentHash derives the persisted-artifact identity key from the exact
// graph state plus the taint config. It reuses the engine's existing FNV-1a 64
// content-hashing scheme (engine/ingest uses the same) rather than introducing a
// new scheme: nodes and edges are emitted in sorted, content-addressed id order
// so the same final graph state hashes identically whether it was built fresh or
// reached incrementally. No wall-clock, run id, or iteration order participates.
func computeContentHash(nodes []model.Node, edges []model.Edge, cfg taint.Config) string {
	nodeKeys := make([]string, 0, len(nodes))
	for _, n := range nodes {
		nodeKeys = append(nodeKeys, string(n.ID())+"\x00"+n.Kind()+"\x00"+n.QualifiedName())
	}
	sort.Strings(nodeKeys)

	edgeKeys := make([]string, 0, len(edges))
	for _, e := range edges {
		edgeKeys = append(edgeKeys, string(e.ID())+"\x00"+string(e.From())+"\x00"+string(e.To())+"\x00"+e.Kind())
	}
	sort.Strings(edgeKeys)

	h := fnv.New64a()
	_, _ = h.Write([]byte("interproctaint/v1\x00"))
	_, _ = h.Write([]byte(cfg.ContentHash))
	_, _ = h.Write([]byte("\x00nodes\x00"))
	for _, k := range nodeKeys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte("\n"))
	}
	_, _ = h.Write([]byte("\x00edges\x00"))
	for _, k := range edgeKeys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Marshal serializes a Solution to the canonical, byte-stable artifact bytes. It
// reuses the project's canonical-ordering discipline: HTML escaping disabled,
// trailing newline trimmed, and (because Solution slices are pre-sorted by the
// solver and Go encodes struct fields in declaration order and map keys sorted)
// the output is identical regardless of the path taken to reach the graph state.
// Runtime observability flags (Loaded/Solved) are json:"-" and never appear.
func Marshal(sol Solution) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(sol); err != nil {
		return nil, fmt.Errorf("interproctaint: marshal: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Store is a content-hash-keyed on-disk store for solved fixpoint artifacts. The
// directory is the only state; each artifact is a single canonical JSON file
// named by its content hash. It is local file I/O only — no network, no CGo.
type Store struct {
	dir string
}

// NewStore returns a Store rooted at dir. The directory is created lazily on the
// first Save.
func NewStore(dir string) *Store { return &Store{dir: dir} }

// artifactPath returns the sanitized on-disk path for a content-hash key. The
// key must be a non-empty lowercase hex string (as produced by computeContentHash)
// so it can never contain a path separator or traversal sequence.
func (s *Store) artifactPath(key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("interproctaint: empty artifact key")
	}
	for _, r := range key {
		isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')
		if !isHex {
			return "", fmt.Errorf("interproctaint: non-hex artifact key %q", key)
		}
	}
	return filepath.Join(s.dir, key+".fixpoint.json"), nil
}

// Save writes the solution atomically (temp file + rename) under its content-hash
// key. The persisted bytes are the canonical Marshal output.
func (s *Store) Save(sol Solution) error {
	path, err := s.artifactPath(sol.ContentHash)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("interproctaint: mkdir: %w", err)
	}
	data, err := Marshal(sol)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".fixpoint-*.tmp")
	if err != nil {
		return fmt.Errorf("interproctaint: temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("interproctaint: write artifact: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("interproctaint: close artifact: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("interproctaint: rename artifact: %w", err)
	}
	return nil
}

// Load reads the artifact for the given content-hash key. It returns ok=false
// (a miss) when the file is absent, the schema version differs, or the stored
// content hash does not match the requested key — never a stale/wrong answer.
func (s *Store) Load(key string) (Solution, bool, error) {
	path, err := s.artifactPath(key)
	if err != nil {
		return Solution{}, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Solution{}, false, nil
		}
		return Solution{}, false, fmt.Errorf("interproctaint: read artifact: %w", err)
	}
	var sol Solution
	if err := json.Unmarshal(data, &sol); err != nil {
		// A corrupt/unreadable artifact is a miss, not a fatal error.
		return Solution{}, false, nil
	}
	if sol.SchemaVersion != SchemaVersion || sol.ContentHash != key {
		return Solution{}, false, nil
	}
	sol.Loaded = true
	sol.Solved = false
	return sol, true, nil
}

// LoadOrSolve serves a source→sink-queryable Solution. When a store is provided
// and a matching artifact exists for the current graph/config content hash, the
// solution is served from disk with NO recomputation (sol.Loaded=true,
// sol.Solved=false). Otherwise the fixpoint is solved (sol.Solved=true) and, if a
// store is provided, persisted for subsequent restarts. A key mismatch always
// recomputes — it never serves a stale answer.
func LoadOrSolve(ctx context.Context, store *Store, r query.Reader, cfg taint.Config, caps interproc.Caps, wideningThreshold int) (Solution, error) {
	nodes, err := r.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return Solution{}, fmt.Errorf("interproctaint: load nodes: %w", err)
	}
	edges, err := r.Edges(ctx, graphstore.Query{})
	if err != nil {
		return Solution{}, fmt.Errorf("interproctaint: load edges: %w", err)
	}
	key := computeContentHash(nodes, edges, cfg)

	if store != nil {
		if sol, ok, err := store.Load(key); err != nil {
			return Solution{}, err
		} else if ok {
			return sol, nil
		}
	}

	sol, err := solveFrom(ctx, nodes, edges, cfg, caps, wideningThreshold)
	if err != nil {
		return Solution{}, err
	}
	if store != nil {
		if err := store.Save(sol); err != nil {
			return Solution{}, err
		}
	}
	return sol, nil
}

// QueryFlow looks up a single source→sink flow in a (loaded or solved) Solution
// by source and sink qualified names. It is a pure in-memory lookup over the
// solved relation — no recomputation — so it answers identically pre- and
// post-restart. Returns the flow and whether it was found.
func QueryFlow(sol Solution, sourceName, sinkName string) (Flow, bool) {
	for _, f := range sol.Flows {
		if f.SourceName == sourceName && f.SinkName == sinkName {
			return f, true
		}
	}
	return Flow{}, false
}
