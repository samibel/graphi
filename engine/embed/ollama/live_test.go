package ollama_test

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"testing"

	"github.com/samibel/graphi/engine/embed"
)

// Opt-in, no fixture fallback. Exact vectors, not rounded values, are compared.
func TestPinnedLiveDeterminism(t *testing.T) {
	selector := os.Getenv("GRAPHI_OLLAMA_TEST_SELECTOR")
	if selector == "" {
		t.Skip("set GRAPHI_OLLAMA_TEST_SELECTOR for live local determinism probe")
	}
	e, err := embed.Constructor(selector, embed.DefaultConstructors())
	if err != nil {
		t.Fatal(err)
	}
	if err := e.(embed.DimDiscoverer).ProbeDim(t.Context()); err != nil {
		t.Fatal(err)
	}
	inputs := []string{"ExecuteC", "where are required flags validated before a command runs", "func Example() { return }"}
	var runs [][][][]float32
	for run := 0; run < 5; run++ {
		var values [][][]float32
		for _, q := range inputs {
			v, err := embed.EmbedQuery(t.Context(), e, q)
			if err != nil {
				t.Fatal(err)
			}
			values = append(values, v)
		}
		runs = append(runs, values)
	}
	identical, maxDelta := true, float64(0)
	for _, run := range runs[1:] {
		identical = identical && reflect.DeepEqual(run, runs[0])
		for i, vectors := range run {
			for j, v := range vectors[0] {
				maxDelta = math.Max(maxDelta, math.Abs(float64(v-runs[0][i][0][j])))
			}
		}
	}
	if path := os.Getenv("GRAPHI_OLLAMA_TEST_OUT"); path != "" {
		report := struct {
			Selector  string          `json:"selector"`
			Inputs    []string        `json:"inputs"`
			Identical bool            `json:"identical"`
			MaxDelta  float64         `json:"max_delta"`
			Runs      [][][][]float32 `json:"runs"`
		}{selector, inputs, identical, maxDelta, runs}
		raw, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(raw, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("5 repetitions x 3 inputs: identical=%t max_abs_delta=%g", identical, maxDelta)
	if !identical {
		t.Fatal("same model inputs produced different vectors")
	}
}
