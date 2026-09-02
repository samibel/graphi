package embed

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

// jsonUnmarshal is the Status/UnmarshalJSON helper; kept as a tiny
// indirection so the State.UnmarshalJSON method above reads naturally.
func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// Package embed's Status type — the engine-owned value the SW-265
// `graphi semantic status` verb serializes byte-identically on CLI/MCP/HTTP.
//
// # Why explicit preconditions, not a packed state
//
// The four "is the index usable?" preconditions — installed, configured,
// indexed, fresh — are FIRST-CLASS FIELDS, not a packed bit. The five
// real situations a user can be in are distinguishable only when every
// precondition is visible:
//
//   - first run / no embedder selector              (Configured=false)
//   - selector set but model artifact absent         (Configured=true, Indexed=false)
//   - model present, no generation                  (Configured=true, Indexed=false)
//   - generation exists but stale vs fingerprint    (Indexed=true, Fresh=false)
//   - generation corrupt or model corrupt           (Indexed=true, Fresh=false)
//
// Packing them would make "ready" the only state a caller could read
// without an outside fact, conflating an empty or stale index with
// good retrieval quality. The whole value of the verb is in the
// distinguishing.
//
// # Why State is the typed vocabulary, not a bool
//
// The SW-261 GenerationStore state vocabulary (missing|stale|corrupt|
// ready) is what the search service keys off. The status surface uses
// the SAME vocabulary so the user's mental model and the search service's
// internal checks never disagree about what "ready" means.

// MarshalJSON renders State as its closed-vocabulary string
// (`missing`|`stale`|`corrupt`|`ready`), never as the underlying int.
// The wire format is the string the search service's typed
// UnavailableReason keys off; rendering an int would silently misroute
// the value at every consumer.
func (s State) MarshalJSON() ([]byte, error) {
	return []byte(`"` + s.String() + `"`), nil
}

// UnmarshalJSON parses the closed-vocabulary string back into a State.
// The StateUnset sentinel is never wire-rendered (its String() is
// "unset"); an unknown string is parsed as StateUnset so a future state
// added to the vocabulary cannot make an old reader panic.
func (s *State) UnmarshalJSON(data []byte) error {
	var raw string
	if err := jsonUnmarshal(data, &raw); err != nil {
		return err
	}
	switch strings.ToLower(raw) {
	case "missing":
		*s = StateMissing
	case "stale":
		*s = StateStale
	case "corrupt":
		*s = StateCorrupt
	case "ready":
		*s = StateReady
	default:
		*s = StateUnset
	}
	return nil
}

// SchemaVersion is the version field the canonical wire document carries.
// Bump ONLY on a breaking change to the Status struct (a renamed field,
// a new required field, a state string added to the closed vocabulary);
// additive changes keep the version at 1.
const SchemaVersion = 1

// Status is the engine-owned canonical status document for the optional
// semantic-search surface. Every field is always present on the wire;
// the canonical encoder (surfaces/client/semantic_status.go) emits it
// once and every surface reads that document.
//
// Field order on the wire matches the struct order; the encoder relies on
// it for byte stability (see encodeCanonical in surfaces/client).
type Status struct {
	// Installed reports whether the binary carries the semantic-search
	// code paths. A `false` value here means the embed boundary has been
	// compiled out. Default graphi builds report `true`.
	Installed bool `json:"installed"`
	// Configured reports whether an embedder is selected — i.e. the
	// GRAPHI_EMBEDDER selector resolves to a registered Embedder. An
	// UNSELECTED selector reads `false` here.
	Configured bool `json:"configured"`
	// Indexed reports whether an active generation exists in the
	// GenerationStore, including a stale or corrupt one.
	Indexed bool `json:"indexed"`
	// Fresh reports whether the active generation's fingerprint matches
	// the requested fingerprint. A corrupt generation can be fresh but
	// unusable; State carries that independent validation verdict.
	Fresh bool `json:"fresh"`
	// State is the typed lifecycle state, named by the SW-261 vocabulary
	// (missing|stale|corrupt|ready). It is the single fact the CLI maps
	// to its exit code (0=ready, 1=actionable, 2=error/corrupt) and the
	// single fact the agent surfaces render into their "available" gate.
	State State `json:"state"`
	// Model is the identity of the configured embedder.
	Model Model `json:"model"`
	// ActiveGeneration is the current, SERVED generation. Empty when no
	// active generation exists.
	ActiveGeneration GenerationSummary `json:"active_generation"`
	// LastGeneration is the generation that was active BEFORE the current
	// one. Empty when no prior generation exists.
	LastGeneration GenerationSummary `json:"last_generation"`
	// Languages is the validation map the spec's decision 2 names:
	// `go` is `validated`, every other indexed language `unvalidated`.
	Languages map[string]string `json:"languages"`
	// Repair is the EXACT `graphi ...` command the user runs to leave
	// this state. Required per AC-3 (each repair must be the exact command
	// that fixes that situation); empty when the state needs no repair.
	Repair string `json:"repair"`
}

// Model is the identity of the active embedder on the wire.
type Model struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
	SHA256   string `json:"sha256"`
}

// GenerationSummary is one generation's identity on the wire. The fields
// mirror the build output (documents persisted, nodes embedded, share by
// span method, built timestamp) so the surface needs no second store read
// to answer a status query.
type GenerationSummary struct {
	ID              string             `json:"id"`
	Fingerprint     string             `json:"fingerprint"`
	Documents       int                `json:"documents"`
	Nodes           int                `json:"nodes"`
	SpanMethodShare map[string]float64 `json:"span_method_share"`
	BuiltAt         string             `json:"built_at"`
}

// LoadStatus composes the engine-owned Status for the requested repo. It
// reads the auto-managed state, the durable GenerationStore, and the live
// embedder registry, and returns the value the canonical encoder consumes.
//
// LoadStatus NEVER returns an error: every failure path fails closed to a
// typed Status shape so the CLI exit-code mapping and the MCP/HTTP wire
// stay consistent. The first-run / no-store shape is the failure default.
//
// reg nil reads as `Configured=false` (the graceful-skip state). An empty
// metaDir falls back to the auto-managed per-repo sidecar at metaDirResolved.
// An empty root reads as "no repository" (state missing).
func LoadStatus(ctx context.Context, metaDir string, reg *Registry, graphGeneration string, nodes NodeReferencer) Status {
	return loadStatus(ctx, metaDir, reg, graphGeneration, nodes)
}

func loadStatus(ctx context.Context, metaDir string, reg *Registry, graphGeneration string, nodes NodeReferencer) Status {
	s := Status{
		Installed: true,
		Languages: languageValidationMap(),
	}
	if reg == nil {
		s.State = StateMissing
		s.Repair = setupRepair()
		return s
	}
	emb, ok := reg.Active()
	if !ok {
		s.State = StateMissing
		s.Repair = setupRepair()
		return s
	}
	s.Configured = true
	s.Model = Model{
		ID:       emb.ID(),
		Revision: embedderRevision(emb),
		SHA256:   embedderModelSHA(emb),
	}

	// AC-3 (b): an embedder whose artifact is missing reports the typed
	// unavailable error with the setup-embedder repair. The AvailabilityChecker
	// precedes generation-state checks so the operator's first action is
	// the right one (install artifact, then re-index).
	if checker, ok := emb.(AvailabilityChecker); ok {
		if err := checker.CheckAvailable(ctx); err != nil {
			var r Repairable
			if errors.As(err, &r) {
				s.Repair = r.Repair()
				s.State = StateMissing
				return s
			}
			s.Repair = setupRepair()
			s.State = StateCorrupt
			return s
		}
	}

	if metaDir == "" {
		s.State = StateMissing
		s.Repair = semanticRepair(StateMissing)
		return s
	}
	store, err := OpenSQLiteGenerationStoreReadOnly(ctx, metaDir)
	if err != nil {
		s.State = StateMissing
		s.Repair = semanticRepair(StateMissing)
		return s
	}
	defer func() { _ = store.Close() }()

	fp := fingerprintForEmbedder(emb, graphGeneration)
	if fp.Dim == 0 {
		if dim, ok, derr := store.DimForModel(ctx, emb.ID()); derr == nil && ok {
			fp.Dim = dim
		}
	}

	gen, state, activeErr := store.Active(ctx, fp, nodes)
	if activeErr != nil && gen.ID == "" {
		s.State = StateCorrupt
		s.Repair = semanticRepair(StateCorrupt)
		return s
	}
	if activeErr != nil {
		state = StateCorrupt
	}
	s.State = state
	s.Indexed = gen.ID != ""
	s.Fresh = gen.ID != "" && gen.Fingerprint.Canonical() == fp.Canonical()
	if gen.ID != "" {
		rows, lerr := store.Load(ctx, gen.ID)
		if lerr != nil {
			s.State = StateCorrupt
			s.Repair = semanticRepair(StateCorrupt)
			return s
		}
		share, distinct := generationRowStats(rows)
		s.ActiveGeneration = GenerationSummary{
			ID:              string(gen.ID),
			Fingerprint:     gen.Fingerprint.Canonical(),
			Documents:       len(rows),
			Nodes:           distinct,
			SpanMethodShare: share,
			BuiltAt:         gen.CommittedAt,
		}
		if prior, ok, perr := store.Previous(ctx, gen.ID); perr == nil && ok {
			priorRows, _ := store.Load(ctx, prior.ID)
			priorShare, priorNodes := generationRowStats(priorRows)
			s.LastGeneration = GenerationSummary{
				ID:              string(prior.ID),
				Fingerprint:     prior.Fingerprint.Canonical(),
				Documents:       len(priorRows),
				Nodes:           priorNodes,
				SpanMethodShare: priorShare,
				BuiltAt:         prior.CommittedAt,
			}
		}
	}
	if s.State != StateReady {
		s.Repair = semanticRepair(s.State)
	}
	return s
}

// fingerprintForEmbedder builds the requested fingerprint from the active
// embedder and the optional current-graph identity. An empty graphGen
// substitutes the documented placeholder so the store's fingerprint
// comparison matches the build's.
func fingerprintForEmbedder(emb Embedder, graphGen string) Fingerprint {
	fp := Fingerprint{
		ModelID:         emb.ID(),
		Revision:        embedderRevision(emb),
		ModelSHA256:     embedderModelSHA(emb),
		TokenizerSHA256: embedderTokenizerSHA(emb),
		Dim:             emb.Dim(),
		DocumentSchema:  DocumentSchema,
		ChunkerConfig:   embedderChunkerConfig(emb),
	}
	if graphGen != "" {
		fp.GraphGeneration = graphGen
	} else {
		fp.GraphGeneration = GraphGenerationPlaceholder
	}
	return fp
}

// generationRowStats computes the span-method share and the distinct
// NodeId count for one generation's rows. Rows are already in canonical
// order from the store (node_id, document_id), so a single pass suffices.
func generationRowStats(rows []Row) (map[string]float64, int) {
	share := map[string]float64{}
	if len(rows) == 0 {
		return share, 0
	}
	nodes := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		share[r.SpanMethod]++
		nodes[string(r.NodeID)] = struct{}{}
	}
	total := float64(len(rows))
	for k, v := range share {
		share[k] = v / total
	}
	return share, len(nodes)
}

// semanticRepair maps the typed state to the exact command that fixes it.
// AC-3 contract — each repair must be the exact `graphi ...` invocation
// that resolves the state. An empty string means "no operator action"
// (a `ready` state).
func semanticRepair(state State) string {
	switch state {
	case StateMissing, StateStale, StateCorrupt:
		return "graphi index --semantic"
	}
	return ""
}

// setupRepair is the operator command for the unconfigured path (no
// embedder selected). The pinned static selector is the recommended Labs
// path; the printed form is exactly what `graphi setup-embedder` would
// render.
func setupRepair() string {
	return "graphi setup-embedder"
}

// languageValidationMap returns the AC-5 / spec-decision-2 validation
// table: Go is `validated`. Every other language that ships parsers is
// added by FillLanguages at compose time, derived live from the parser
// registry so adding a language requires no change here.
func languageValidationMap() map[string]string {
	return map[string]string{
		"go": "validated",
	}
}

// FillLanguages derives the validation map for the wired registry: Go
// is `validated`, every other indexed language `unvalidated`. Exported so
// the surfaces/client composition can call it once and serialise the
// result — the language list is the AC-5 / spec-decision-2 contract,
// derived live rather than maintained as a table.
func FillLanguages(indexedLanguages []string) map[string]string {
	out := make(map[string]string, len(indexedLanguages))
	for _, lang := range indexedLanguages {
		if lang == "go" {
			out[lang] = "validated"
			continue
		}
		out[lang] = "unvalidated"
	}
	return out
}

// SortedLanguages returns the languages map's keys in canonical order.
func SortedLanguages(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Repairable is the typed repair an embedder surfaces through its
// AvailabilityChecker. Mirrors engine/search.Repairable; this local copy
// keeps the leaf free of the search package.
type Repairable interface {
	error
	Repair() string
}

// commitTimestamp is the RFC3339 UTC stamp Build.Commit writes into
// Generation.CommittedAt. Centralising it keeps the two adapters (mem and
// SQLite) in lock-step on the format.
func commitTimestamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}
