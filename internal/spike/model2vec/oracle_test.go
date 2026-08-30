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

// AC-2/3: the batch of ≥8 mixed texts.
//
// FINDING (see PINNED.md, "batch padding quirk"): the reference's batch
// vectors are NOT its single-text vectors. tokenizer.json carries a
// padding section (BatchLongest, pad id 0), Tokenizer.encode_batch_fast
// applies it, and model2vec removes only the unk id afterwards — so every
// text shorter than the longest in its batch pools (longest − own) copies of
// the [PAD] embedding row into its mean. Only the longest text of the batch
// (path_segments, 15 tokens) equals its single encoding. Embed is
// deliberately batch-invariant (a vector must not depend on which other
// nodes share its chunk), so this test replays the fixture in two ways: the
// reference's padded arithmetic must match within epsilon (proving the quirk
// is understood exactly), and the batch-invariant Embed's distance from it is
// recorded as the size of the quirk.
func TestOracle_BatchWithinEpsilon(t *testing.T) {
	o := loadOracle(t)
	m := loadPinnedModel(t)
	texts := o.Batch.Texts
	if len(o.Batch.Vectors) != len(texts) {
		t.Fatalf("%d vectors for %d texts", len(o.Batch.Vectors), len(texts))
	}
	longest := 0
	raw := make([][]int, len(texts))
	for i, txt := range texts {
		raw[i] = m.TokenIDs(m.cutChars(txt))
		longest = max(longest, len(raw[i]))
	}
	padID := 0 // tokenizer.json padding.pad_id
	var worstPadded, worstInvariant float64
	invariant := m.Embed(texts)
	for i := range texts {
		ids := append([]int(nil), raw[i]...)
		for len(ids) < longest {
			ids = append(ids, padID)
		}
		padded := m.embedOne(m.dropUnkAndCap(ids), true)
		d, j := maxAbsDiff(padded, o.Batch.Vectors[i])
		worstPadded = math.Max(worstPadded, d)
		if d > oracleEpsilon {
			t.Errorf("batch text %d (%q) with %d pad ids: max |Δ| = %.3g at component %d exceeds %g", i, texts[i], longest-len(raw[i]), d, j, oracleEpsilon)
		}
		d, _ = maxAbsDiff(invariant[i], o.Batch.Vectors[i])
		worstInvariant = math.Max(worstInvariant, d)
		if len(raw[i]) == longest && d > oracleEpsilon {
			t.Errorf("batch text %d is the longest (no padding) yet Embed differs from the batch vector by %.3g", i, d)
		}
	}
	t.Logf("batch of %d (longest %d tokens): padded-reference replay max |Δ| = %.3g; batch-invariant Embed vs padded reference max |Δ| = %.3g (the padding quirk)", len(texts), longest, worstPadded, worstInvariant)
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
