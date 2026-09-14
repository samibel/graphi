package v9

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/shape"
)

// compactTaskContextHydrateExactFile answers a one-token question that is the
// path of a repository file the discovery snapshot never scans — a module
// file, a Makefile, a licence, a configuration file — with the file itself.
// Retrieval indexes none of these, so the projector used to receive only
// unrelated rows for such a question. The file's first FollowupMaxLines lines
// become one candidate region ranked ahead of every retrieval row; the
// selector then trims it like any other lead. Go paths are outlined by
// compactTaskContextHydrateExactPath and are not repeated here. Only the
// query text and repository bytes decide anything; a missing, binary,
// excluded or unreadable path yields nothing rather than an error.
func compactTaskContextHydrateExactFile(ctx context.Context, repository fs.FS, query string, items []contract.Item) ([]contract.Evidence, []contract.Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	trimmed := strings.TrimSpace(query)
	if repository == nil || trimmed == "" || strings.ContainsAny(trimmed, " \t\r\n") {
		return nil, nil, nil
	}
	clean := cleanGrepReadPath(trimmed)
	if clean == "." || !fs.ValidPath(clean) || strings.HasSuffix(strings.ToLower(clean), ".go") {
		return nil, nil, nil
	}
	for _, part := range strings.Split(clean, "/") {
		if grepReadExcludedDirectory(part) {
			return nil, nil, nil
		}
	}
	info, err := fs.Stat(repository, clean)
	if err != nil || !info.Mode().IsRegular() {
		return nil, nil, nil
	}
	raw, err := readSourceFile(repository, clean)
	if err != nil || !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return nil, nil, nil
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return nil, nil, nil
	}
	if len(lines) > FollowupMaxLines {
		lines = lines[:FollowupMaxLines]
	}
	rank := 0
	for _, item := range items {
		rank = max(rank, item.Rank)
	}
	text := strings.Join(lines, "\n")
	ref := "path-file-001"
	evidence := []contract.Evidence{{
		RefID: ref, Path: clean, Line: 1, Span: fmt.Sprintf("1-%d", len(lines)),
		Role: "snippet", Snippet: text, TextHash: shape.TextHash(text),
	}}
	linked := []contract.Item{{
		RefID: "path-file-item-" + ref, Rank: rank + 1,
		Reason:         fmt.Sprintf("candidate: file %s (%s:1) score 0", path.Base(clean), clean),
		EvidenceRefIDs: []string{ref},
	}}
	return evidence, linked, nil
}
