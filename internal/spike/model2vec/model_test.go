package model2vec

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadModel_Synthetic(t *testing.T) {
	m := loadSyntheticModel(t)
	if m.Dim() != 8 || m.VocabSize() != len(syntheticVocab) || !m.Normalize() {
		t.Fatalf("dim=%d vocab=%d normalize=%v", m.Dim(), m.VocabSize(), m.Normalize())
	}
	row := m.Row(3)
	if row[0] != 3 || row[1] != -1.5 || row[2] != 0.25 || row[3] != f16ToF32(f32ToF16(0.003)) {
		t.Fatalf("Row(3) = %v", row)
	}
	if m.ArtifactBytes <= 0 {
		t.Fatalf("ArtifactBytes = %d", m.ArtifactBytes)
	}
}

func TestLoadModel_RowCountMustMatchVocabulary(t *testing.T) {
	dir := writeSyntheticModel(t, 4, nil)
	raw, err := os.ReadFile(filepath.Join(dir, FileTokenizer))
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(string(raw), `"##er":29`, `"##er":29,"extra":30`, 1)
	if mutated == string(raw) {
		t.Fatal("mutation not applied")
	}
	if err := os.WriteFile(filepath.Join(dir, FileTokenizer), []byte(mutated), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadModel(dir); err == nil || !strings.Contains(err.Error(), "embedding rows") {
		t.Fatalf("LoadModel error = %v, want a row-count mismatch", err)
	}
}

func TestLoadModel_RejectsWrongDtype(t *testing.T) {
	dir := writeSyntheticModel(t, 4, nil)
	p := filepath.Join(dir, FileSafetensors)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(string(raw), `"F16"`, `"F32"`, 1)
	if err := os.WriteFile(p, []byte(mutated), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadModel(dir); err == nil || !strings.Contains(err.Error(), "F16 only") {
		t.Fatalf("LoadModel error = %v, want an F16-only refusal", err)
	}
}

// Empty token lists pool to the zero vector: no NaN, norm 0.
func TestEmbed_EmptyIsZeroVector(t *testing.T) {
	m := loadSyntheticModel(t)
	for _, text := range []string{"", "   \n\t ", "zzz qqq"} {
		for _, v := range m.Embed([]string{text})[0] {
			if v != 0 || math.IsNaN(float64(v)) {
				t.Fatalf("Embed(%q) = non-zero/NaN component %v", text, v)
			}
		}
	}
}

// Mean pooling in index order with float32 accumulators, then the float16
// rounding points of the reference — checked by hand on the synthetic table.
func TestEmbed_PoolingArithmetic(t *testing.T) {
	m := loadSyntheticModel(t)
	// "hello world" = rows 2 and 3.
	got := m.Embed([]string{"hello world"})[0]
	var mean [8]float32
	for _, id := range []int{2, 3} {
		for j, v := range m.Row(id) {
			mean[j] += v
		}
	}
	for j := range mean {
		mean[j] = roundF16(mean[j] / 2)
	}
	sq := make([]uint16, 8)
	for j, v := range mean {
		sq[j] = f32ToF16(v * v)
	}
	norm := roundF16(float32(math.Sqrt(float64(roundF16(pairwiseSumF16(sq))))))
	for j := range mean {
		if want := roundF16(mean[j] / norm); got[j] != want {
			t.Fatalf("component %d = %v, want %v", j, got[j], want)
		}
	}
	// The clean float32 pipeline is unit-norm to float32 precision.
	var sumsq float64
	for _, v := range m.EmbedFloat32([]string{"hello world"})[0] {
		sumsq += float64(v) * float64(v)
	}
	if n := math.Sqrt(sumsq); math.Abs(n-1) > 1e-6 {
		t.Fatalf("EmbedFloat32 norm = %v", n)
	}
}

// Embed is REFERENCE-FAITHFUL: a single text's batch vector equals its
// single-text vector (BatchLongest padding is a no-op for a batch of one);
// running Embed twice on the same texts is bit-identical. Note that mixing
// texts whose inference ids are empty with non-empty ones changes the
// arithmetic for the whole batch (the reference promotes to float64 when
// np.zeros joins the stack) — this test keeps the texts non-empty so it
// isolates the BatchLongest effect, which is what we want.
func TestEmbed_SingleTextBatchMatchesSingleAndDeterministic(t *testing.T) {
	m := loadSyntheticModel(t)
	texts := []string{"hello world", "unaffable, hello", "認 hellos", "ValidateToken"}
	batch := m.Embed(texts)
	for i, text := range texts {
		single := m.Embed([]string{text})[0]
		for j := range single {
			if single[j] != batch[i][j] {
				t.Fatalf("text %d component %d: batch %v, single %v (a batch of one must not pool pad ids)", i, j, batch[i][j], single[j])
			}
		}
	}
	again := m.Embed(texts)
	for i := range again {
		for j := range again[i] {
			if again[i][j] != batch[i][j] {
				t.Fatalf("run-to-run difference at text %d component %d", i, j)
			}
		}
	}
}

// EmbedEach is BATCH-INVARIANT: EmbedEach(texts)[i] is bit-identical to
// Embed([]string{texts[i]})[0] for every i, regardless of which texts share
// the call. EmbedEach is the encode([text]) semantics recommended for SW-262.
func TestEmbedEach_BatchInvariant(t *testing.T) {
	m := loadSyntheticModel(t)
	texts := []string{"hello world", "", "unaffable, hello", "認 hellos", "ValidateToken"}
	batch := m.EmbedEach(texts)
	for i, text := range texts {
		single := m.Embed([]string{text})[0]
		for j := range single {
			if batch[i][j] != single[j] {
				t.Fatalf("text %d component %d: EmbedEach(batch)[i] = %v, Embed([text])[0] = %v (must be bit-identical)", i, j, batch[i][j], single[j])
			}
		}
	}
}

// Embed (reference-faithful, BatchLongest padded) and EmbedEach (batch-invariant)
// agree on the longest text in a batch (BatchLongest pads the shorter texts
// only) and diverge on every shorter text by the pad-row contribution to their
// mean. The synthetic tokenizer.json has padding=null, so on the synthetic
// model Embed and EmbedEach are bit-identical — that is the correct behaviour
// for a tokenizer that does not declare padding, and it is the reason this
// test runs on the PINNED artifact (which declares BatchLongest, pad id 0).
// Without the artifact the divergence is "0 (BatchLongest is a no-op when the
// tokenizer declares no padding section)" — recorded, not asserted.
func TestEmbedEach_DivergenceFromEmbedIsPadding(t *testing.T) {
	dir := artifactDir()
	if !artifactPresent(dir) {
		t.Skip(skipMessage)
	}
	m := loadPinnedModel(t)
	enabled, _ := m.Tokenizer().Padding()
	if !enabled {
		t.Skip("pinned tokenizer declares no padding section; Embed == EmbedEach by construction")
	}
	// Pick three texts that produce different token counts so the pad
	// contribution differs row-by-row and the divergence is informative.
	texts := []string{"hello world", "hello", "認"}
	batch := m.Embed(texts)
	each := m.EmbedEach(texts)
	// Longest text: Embed and EmbedEach must agree (no pad pooled).
	longest := 0
	for _, txt := range texts {
		if l := len(m.InferenceIDs(txt)); l > longest {
			longest = l
		}
	}
	for i, txt := range texts {
		if len(m.InferenceIDs(txt)) != longest {
			continue
		}
		for j := range batch[i] {
			if batch[i][j] != each[i][j] {
				t.Fatalf("longest text %d (%q) component %d: Embed %v, EmbedEach %v (must agree)", i, txt, j, batch[i][j], each[i][j])
			}
		}
	}
	var worst float64
	divergent := 0
	for i := range texts {
		if len(m.InferenceIDs(texts[i])) == longest {
			continue
		}
		d, _ := maxAbsDiff(batch[i], each[i])
		if d > worst {
			worst = d
		}
		if d > 0 {
			divergent++
		}
	}
	if divergent == 0 {
		t.Errorf("no shorter text diverged between Embed and EmbedEach; BatchLongest had no effect — a configuration drift")
	}
	t.Logf("pinned batch (longest %d tokens): max |Δ| between Embed and EmbedEach over the %d padded rows = %.3g — the padding effect", longest, divergent, worst)
}
