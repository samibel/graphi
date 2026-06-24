package embed_test

import (
	"testing"

	"github.com/samibel/graphi/engine/embed"
)

// ONNX build-tag isolation (default build): the DEFAULT constructor table has no
// "onnx" scheme, so the default binary can never construct a CGO ONNX embedder.
// Under `//go:build embed_onnx` the engine/embed/onnx package self-registers the
// scheme; this test runs in the DEFAULT build (no tag) and asserts its absence.
func TestDefaultConstructors_NoOnnxScheme(t *testing.T) {
	ctors := embed.DefaultConstructors()
	if _, ok := ctors["onnx"]; ok {
		t.Fatal("default constructor table contains the CGO \"onnx\" scheme; it must be embed_onnx-tagged only")
	}
	// Even an explicit onnx selector gracefully skips in the default build.
	e, err := embed.Constructor("onnx:/some/model.onnx", ctors)
	if err != nil {
		t.Fatalf("onnx selector in default build errored: %v", err)
	}
	if e != nil {
		t.Fatal("default build constructed an onnx embedder; CGO embedder must be tag-gated")
	}
}
