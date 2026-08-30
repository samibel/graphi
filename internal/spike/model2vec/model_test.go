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
	dir := writeSyntheticModel(t, 4)
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
	dir := writeSyntheticModel(t, 4)
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

// A text's vector never depends on the other texts in its batch, and two
// runs are bit-identical.
func TestEmbed_BatchInvariantAndDeterministic(t *testing.T) {
	m := loadSyntheticModel(t)
	texts := []string{"hello world", "", "unaffable, hello", "認 hellos"}
	batch := m.Embed(texts)
	for i, text := range texts {
		single := m.Embed([]string{text})[0]
		for j := range single {
			if single[j] != batch[i][j] {
				t.Fatalf("text %d component %d: batch %v, single %v", i, j, batch[i][j], single[j])
			}
		}
	}
	again := m.Embed(texts)
	for i := range texts {
		for j := range again[i] {
			if again[i][j] != batch[i][j] {
				t.Fatalf("run-to-run difference at text %d component %d", i, j)
			}
		}
	}
}
