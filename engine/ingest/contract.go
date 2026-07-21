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
type Registry interface {
	Parse(ctx context.Context, path string, src []byte) (*parse.ParseResult, error)
}
