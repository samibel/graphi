package parse

import (
	"context"
	"encoding/json"
	"fmt"
)

// JSONParser is a second, distinct stdlib-only parser (encoding/json). It exists
// to PROVE open/closed pluggability: it is registered against the same Parser
// interface and becomes selectable for ".json" without any edit to GoParser or the
// Registry. It produces a structural tree (the decoded any value) rather than a
// rich AST, which is honest and sufficient for the parse boundary.
type JSONParser struct{}

// NewJSONParser returns a ready JSONParser. It is stateless and concurrency-safe.
func NewJSONParser() *JSONParser { return &JSONParser{} }

// Language implements Parser.
func (*JSONParser) Language() string { return "json" }

// Runtime implements Parser: JSONParser is a stdlib-only (encoding/json) parser.
func (*JSONParser) Runtime() Runtime { return RuntimeStdlib }

// Extensions implements Parser.
func (*JSONParser) Extensions() []string { return []string{".json"} }

// Parse implements Parser. It decodes src into a generic structural tree
// (map/slice/scalar) and returns it as ParseResult.Root. It recovers from any
// panic and honors ctx cancellation.
func (j *JSONParser) Parse(ctx context.Context, filename string, src []byte) (res *ParseResult, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	defer func() {
		if r := recover(); r != nil {
			res = nil
			err = fmt.Errorf("parse: recovered from panic parsing %q: %v", filename, r)
		}
	}()

	var root any
	if derr := json.Unmarshal(src, &root); derr != nil {
		return nil, fmt.Errorf("parse: json syntax error in %q: %w", filename, derr)
	}

	return &ParseResult{
		Meta: SourceMeta{
			Path:        filename,
			Language:    j.Language(),
			ContentHash: contentHash(src),
			Size:        len(src),
		},
		Root: root,
	}, nil
}
