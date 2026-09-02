package retrieval

import (
	"context"
	"sort"

	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/internal/rootfile"
)

// spanMethodShare computes the SW-260 AC-9 `span_method_share` of a report:
// the fraction of SemanticDocument v3 documents per span method over the
// indexed files. The lexical index keeps no spans (they are consumed only on
// the `--semantic` path), so the share is measured the way that path measures
// it — every indexed file re-read through the root-confined reader, parsed
// with the default registry, and run through embed.BuildDocuments, whose
// window fallback applies to parsers without an exact adapter. A file that
// fails to read or parse contributes no documents (the same skip the index
// applied). Files are visited in sorted order; the result is a pure function
// of the checkout and byte-reproducible.
func spanMethodShare(ctx context.Context, root string, files []string) (map[string]float64, error) {
	reg := parse.NewDefaultRegistry()
	bounds := parse.DefaultResourceBounds()
	paths := append([]string(nil), files...)
	sort.Strings(paths)
	var stats embed.DocumentStats
	for _, rel := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		src, err := rootfile.Read(root, rel, bounds.MaxFileSize)
		if err != nil {
			continue
		}
		res, err := reg.Parse(ctx, rel, src)
		if err != nil || res == nil {
			continue
		}
		_, st, _ := embed.BuildDocuments(embed.FileSource{
			Source: embed.Source{Language: res.Meta.Language, Bytes: src},
			Path:   rel,
			Nodes:  res.Nodes,
			Spans:  res.Spans,
		})
		parse.ReleaseRoot(res)
		stats.Merge(st)
	}
	return stats.SpanMethodShare(), nil
}
