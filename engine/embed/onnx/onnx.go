//go:build embed_onnx

// Package onnx is graphi's OPT-IN, CGO-backed local ONNX embedder. It is compiled
// ONLY under the `embed_onnx` build tag and is NEVER part of the default binary
// (which stays CGo-free; SW-059 / OQ6). The default build sees no file in this
// package, so the import graph and the CGo-free conformance gate never observe it.
//
// This file is a minimal, syntactically-isolated stub of the CGO seam: it
// implements embed.Embedder AND embed.CgoEmbedder (the marker the registration-
// level no-CGO guard detects) so the two-layer defense (import-graph scan +
// registration guard) both have a real CGO embedder to reason about under the
// tag. A production build would wire onnxruntime here behind the same tag.
package onnx

import (
	"context"
	"fmt"

	"github.com/samibel/graphi/engine/embed"
)

// Scheme is the selector scheme handled under the embed_onnx tag.
const Scheme = "onnx"

func init() {
	// Self-register the CGO ONNX scheme so an explicit GRAPHI_EMBEDDER=onnx:<model>
	// selector can construct it — but ONLY under the embed_onnx tag (this file does
	// not compile otherwise). The embed leaf never imports this package, so the
	// default build cannot register or construct a CGO embedder.
	embed.RegisterScheme(Scheme, func(arg string) (embed.Embedder, error) {
		return New(arg)
	})
}

// Embedder is the CGO-backed local ONNX embedder (tag-gated).
type Embedder struct {
	modelPath string
	dim       int
}

// New constructs the ONNX embedder for the given model path. Under the tag this
// would initialize the onnxruntime session; the stub records the path.
func New(modelPath string) (*Embedder, error) {
	if modelPath == "" {
		return nil, fmt.Errorf("onnx: model path required")
	}
	return &Embedder{modelPath: modelPath, dim: 384}, nil
}

// ID implements embed.Embedder.
func (e *Embedder) ID() string { return Scheme + ":" + e.modelPath }

// Dim implements embed.Embedder.
func (e *Embedder) Dim() int { return e.dim }

// Embed implements embed.Embedder. The stub returns zero vectors of the declared
// dimension; a production build runs the onnxruntime inference here.
func (e *Embedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = make([]float32, e.dim)
	}
	return out, nil
}

// IsCgoEmbedder marks this as a CGO embedder so embed.AssertNoCgoEmbedder detects
// it (the registration-layer complement to the import-graph CGo scan).
func (e *Embedder) IsCgoEmbedder() {}
