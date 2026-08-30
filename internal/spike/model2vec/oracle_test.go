package model2vec

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Epsilon is the per-component tolerance of AC-3, after normalisation.
const oracleEpsilon = 1e-5

const oraclePath = "testdata/oracle/oracle.json"

// oracleFile mirrors testdata/oracle/gen_oracle.py's output.
type oracleFile struct {
	Schema        string            `json:"schema"`
	Model         string            `json:"model"`
	Revision      string            `json:"revision"`
	Files         map[string]string `json:"files"`
	Config        json.RawMessage   `json:"config"`
	Shape         []int             `json:"embedding_shape"`
	DtypeInMemory string            `json:"embedding_dtype_in_memory"`
	Normalize     bool              `json:"normalize"`
	Reference     map[string]string `json:"reference"`
	Cases         []oracleCase      `json:"cases"`
	Batch         struct {
		Texts   []string    `json:"texts"`
		Vectors [][]float32 `json:"vectors"`
	} `json:"batch"`
	RowSamples map[string][]float32 `json:"embedding_row_samples"`
}

type oracleCase struct {
	Name     string    `json:"name"`
	Text     string    `json:"text"`
	TokenIDs []int     `json:"token_ids"`
	Vector   []float32 `json:"vector"`
	Norm     float64   `json:"norm"`
}

func loadOracle(t testing.TB) *oracleFile {
	t.Helper()
	raw, err := os.ReadFile(oraclePath)
	if err != nil {
		t.Fatal(err)
	}
	var o oracleFile
	if err := json.Unmarshal(raw, &o); err != nil {
		t.Fatal(err)
	}
	if o.Schema != "graphi.model2vec-oracle.v1" {
		t.Fatalf("oracle schema %q", o.Schema)
	}
	return &o
}

// The fixture must describe the artifact the tests pin (runs without the artifact).
// pinnedReferenceVersions are the model2vec / numpy / tokenizers / Python
// versions the oracle.json's reference block is required to declare (per
// PINNED.md §Oracle fixtures). The "field present" guard keeps the test honest
// when an older oracle.json is in place: a missing key fails with a clear
// message, not a panic.
var pinnedReferenceVersions = map[string]string{
	"model2vec":  "0.9.0",
	"numpy":      "2.5.2",
	"tokenizers": "0.23.1",
	"python":     "3.13.5",
}

func TestOracle_FixtureMatchesPins(t *testing.T) {
	o := loadOracle(t)
	if o.Model != pinnedModel || o.Revision != pinnedRevision {
		t.Fatalf("oracle is for %s@%s, tests pin %s@%s", o.Model, o.Revision, pinnedModel, pinnedRevision)
	}
	if !reflect.DeepEqual(o.Files, pinnedSHA256) {
		t.Fatalf("oracle file pins %v differ from the test pins %v", o.Files, pinnedSHA256)
	}
	if len(o.Cases) < 15 || len(o.Batch.Texts) < 8 {
		t.Fatalf("oracle has %d cases and a batch of %d; AC-2 needs ≥15 and ≥8", len(o.Cases), len(o.Batch.Texts))
	}
	names := map[string]bool{}
	for _, c := range o.Cases {
		names[c.Name] = true
	}
	for _, want := range []string{"empty", "ascii_identifier", "unicode_nfc", "unicode_nfd", "cjk", "emoji", "camel_case", "snake_case", "go_code_block", "oov_gibberish", "long_text_exceeds_max"} {
		if !names[want] {
			t.Errorf("oracle lacks the AC-2 case %q", want)
		}
	}
	if o.DtypeInMemory != "float16" || !o.Normalize {
		t.Fatalf("oracle dtype %q normalize %v; the pipeline mirrors float16 + normalize", o.DtypeInMemory, o.Normalize)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(oraclePath), "gen_oracle.py")); err != nil {
		t.Fatalf("generator script is not checked in beside the fixture: %v", err)
	}
	// The fixture must self-record the versions of the four libraries whose
	// numerics the spike mirrors. Older fixtures that omit a key fail with a
	// clear "regenerate" message; mismatches fail with the expected vs got.
	for key, want := range pinnedReferenceVersions {
		got, ok := o.Reference[key]
		if !ok {
			t.Errorf("oracle reference.%s is missing — regenerate oracle.json with the current gen_oracle.py (which now serializes %s.__version__)", key, key)
			continue
		}
		if got != want {
			t.Errorf("oracle reference.%s = %q, want %q (PINNED.md §Oracle fixtures)", key, got, want)
		}
	}
}

func TestOracle_ModelShape(t *testing.T) {
	o := loadOracle(t)
	m := loadPinnedModel(t)
	if m.VocabSize() != o.Shape[0] || m.Dim() != o.Shape[1] {
		t.Fatalf("loaded %d×%d, oracle embedding_shape %v", m.VocabSize(), m.Dim(), o.Shape)
	}
	if !m.Normalize() {
		t.Fatal("config.json normalize not read as true")
	}
	if m.MedianTokenLength() != 7 {
		t.Fatalf("median token length %d, expected 7 for this vocabulary", m.MedianTokenLength())
	}
}

// AC-3: token ids are reproduced EXACTLY.
func TestOracle_TokenIDsExact(t *testing.T) {
	o := loadOracle(t)
	m := loadPinnedModel(t)
	for _, c := range o.Cases {
		t.Run(c.Name, func(t *testing.T) {
			got := m.TokenIDs(c.Text)
			if !reflect.DeepEqual(got, c.TokenIDs) {
				t.Fatalf("TokenIDs(%q)\n got  %v\n want %v\n got tokens %v", c.Text, got, c.TokenIDs, m.Tokenizer().Tokens(got))
			}
		})
	}
}

// The embedding table decodes to the reference rows exactly.
func TestOracle_EmbeddingRowsExact(t *testing.T) {
	o := loadOracle(t)
	m := loadPinnedModel(t)
	for key, want := range o.RowSamples {
		var id int
		if _, err := fmt.Sscanf(key, "%d", &id); err != nil {
			t.Fatal(err)
		}
		got := m.Row(id)
		if len(got) != len(want) {
			t.Fatalf("row %d: %d components, want %d", id, len(got), len(want))
		}
		for j := range got {
			if got[j] != want[j] {
				t.Fatalf("row %d component %d: %v, want %v (must be exact: float16 ⊂ float32)", id, j, got[j], want[j])
			}
		}
	}
}

// AC-3: vectors within |Δ| ≤ 1e-5 per component after normalisation. The
// observed maximum is logged for the GO/NO-GO record, for both the
// reference-faithful pipeline (asserted) and the clean float32 one (recorded).
func TestOracle_VectorsWithinEpsilon(t *testing.T) {
	o := loadOracle(t)
	m := loadPinnedModel(t)
	var maxFaithful, maxClean float64
	for _, c := range o.Cases {
		t.Run(c.Name, func(t *testing.T) {
			got := m.Embed([]string{c.Text})[0]
			d, j := maxAbsDiff(got, c.Vector)
			if d > maxFaithful {
				maxFaithful = d
			}
			if d > oracleEpsilon {
				t.Errorf("max |Δ| = %.3g at component %d (got %v, want %v) exceeds %g", d, j, got[j], c.Vector[j], oracleEpsilon)
			}
			if c.Norm == 0 {
				for k, v := range got {
					if v != 0 || math.IsNaN(float64(v)) {
						t.Fatalf("component %d = %v; reference is the all-zero vector", k, v)
					}
				}
			}
			clean := m.EmbedFloat32([]string{c.Text})[0]
			if d, _ := maxAbsDiff(clean, c.Vector); d > maxClean {
				maxClean = d
			}
		})
	}
	t.Logf("max |Δ| vs oracle over %d cases: reference-faithful pipeline %.3g (epsilon %g); clean float32 pipeline %.3g", len(o.Cases), maxFaithful, oracleEpsilon, maxClean)
}

// AC-2/3: the batch of ≥8 mixed texts, replayed through the PUBLIC Embed API.
//
// Embed is REFERENCE-FAITHFUL: it returns what model2vec.StaticModel.encode(list)
// returns, including the BatchLongest padding tokenizer.json declares and the
// pad id it reads from the file. The public call is asserted against
// batch.vectors for ALL 8 rows within oracleEpsilon — no private-helper detour,
// no hardcoded pad id. The batch-invariant form (encode([text]) semantics, the
// recommendation for SW-262) lives in EmbedEach and has its own test
// (TestEmbedEach_BatchInvariantAndDivergenceIsPadding).
//
// Recorded for the GO/NO-GO record: the per-row token count and pad contribution
// the public Embed pools in for the shorter texts. Only the longest text
// (path_segments, 15 tokens) equals its single-text vector; the others differ
// because every shorter text pools (longest − own) copies of the [PAD] row into
// its mean. The longest of the 8 batch texts is logged so the padding effect is
// visible without reading the fixture.
func TestOracle_BatchWithinEpsilon(t *testing.T) {
	o := loadOracle(t)
	m := loadPinnedModel(t)
	texts := o.Batch.Texts
	if len(o.Batch.Vectors) != len(texts) {
		t.Fatalf("%d vectors for %d texts", len(o.Batch.Vectors), len(texts))
	}
	enabled, padID := m.Tokenizer().Padding()
	if !enabled {
		t.Fatalf("tokenizer declares no padding section; the public Embed batch replay requires padding to match the oracle's batch.vectors (see PINNED.md §'Reference behaviours mirrored')")
	}
	if padID < 0 || padID >= m.VocabSize() {
		t.Fatalf("tokenizer pad id %d is outside the vocabulary 0..%d", padID, m.VocabSize()-1)
	}
	got := m.Embed(texts) // public API; no private helper, no hardcoded pad id
	var worst float64
	longest := 0
	for i := range texts {
		d, j := maxAbsDiff(got[i], o.Batch.Vectors[i])
		if d > worst {
			worst = d
		}
		if d > oracleEpsilon {
			t.Errorf("batch text %d (%q): public Embed max |Δ| = %.3g at component %d exceeds %g", i, texts[i], d, j, oracleEpsilon)
		}
		if l := len(m.InferenceIDs(texts[i])); l > longest {
			longest = l
		}
	}
	t.Logf("batch of %d (longest %d tokens) replayed through public Embed: max |Δ| vs batch.vectors = %.3g (epsilon %g)", len(texts), longest, worst, oracleEpsilon)
}

// The reference's normalised vectors are not exactly unit length (float16
// storage); the spike reproduces each case's norm, not an idealised 1.0.
func TestOracle_NormsReproduced(t *testing.T) {
	o := loadOracle(t)
	m := loadPinnedModel(t)
	for _, c := range o.Cases {
		got := m.Embed([]string{c.Text})[0]
		var sumsq float64
		for _, v := range got {
			sumsq += float64(v) * float64(v)
		}
		if n := math.Sqrt(sumsq); math.Abs(n-c.Norm) > 1e-6 {
			t.Errorf("%s: norm %.7f, reference %.7f", c.Name, n, c.Norm)
		}
	}
}

func maxAbsDiff(got, want []float32) (float64, int) {
	if len(got) != len(want) {
		return math.Inf(1), -1
	}
	var d float64
	at := 0
	for j := range got {
		if v := math.Abs(float64(got[j]) - float64(want[j])); v > d || math.IsNaN(v) {
			d, at = v, j
		}
	}
	return d, at
}
