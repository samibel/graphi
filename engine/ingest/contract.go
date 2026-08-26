package ingest

import (
	"context"

	"github.com/samibel/graphi/core/parse"
)

// Parser abstracts the parse operation so tests can count invocations and
// inject deterministic ASTs.
type Parser interface {
	Parse(ctx context.Context, path string, src []byte) (*parse.ParseResult, error)
}

// Registry maps extensions to parsers. It satisfies the Parser interface for a
// whole repository walk.
//
// NOT a registry-lifecycle participant (SW-222 / AX-02), deliberately: this is
// the CONSUMER-side port ingest holds, and it exposes only Parse. It has no
// registration entry point, therefore no collision policy and nothing to
// freeze. The lifecycle lives with the concrete registry on the other side of
// this interface — core/parse.Registry, which NewDefaultRegistry returns frozen.
type Registry interface {
	Parse(ctx context.Context, path string, src []byte) (*parse.ParseResult, error)
}
