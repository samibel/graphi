package extpack

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/samibel/graphi/internal/rootfile"
	"gopkg.in/yaml.v3"
)

// SW-230 (AX-10) — the standalone manifest/schema linter.
//
// # What it adds over `validate`
//
// `graphi extension validate` answers one question — would this pack install? —
// and answers it the way installation does: fail-closed, first violation, exit
// non-zero. That is the right shape for a gate and the wrong shape for an author
// mid-edit, who wants EVERY problem and wants each one located.
//
// Lint is the author-facing half:
//
//   - It reports every violation it can, not the first (Manifest.Checks and the
//     artifact check lists). Validate is still the head of those same lists, so
//     the two cannot disagree about validity — only about how much they say.
//   - It attributes each violation to a FIELD (FieldError) and resolves that
//     field to a LINE and COLUMN in the file that declares it, so a diagnostic
//     reads `pack.yaml:9:11: extpack: artifact.sha256: …`.
//   - It covers BOTH files. The manifest schema and the artifact schema for the
//     pack's kind are separate documents with separate field vocabularies, and a
//     rule with an empty `from` is an artifact diagnostic at the rule's own line.
//
// # Positions come from the bytes, not from the struct
//
// The decoded Manifest has no idea where it came from. The position index is
// built by walking the YAML node tree of the same bytes the validators saw, and
// a field path that the index does not know degrades to the nearest ancestor it
// does — a diagnostic on the right key is better than a diagnostic on line 1, and
// a diagnostic with no position at all is still a diagnostic rather than a
// dropped finding.
//
// # Still offline, still no execution
//
// Lint reads two local files through internal/rootfile under the same bounds
// installation uses. It opens no socket, follows no pack-supplied path, and
// evaluates nothing a pack ships.

// PackManifestName is the manifest file name `graphi extension init` scaffolds
// and the name Lint looks for when it is pointed at a directory.
const PackManifestName = "pack.yaml"

// Diagnostic is one positionful linter finding.
type Diagnostic struct {
	// File is the path the finding was found in, as the caller would type it.
	File string `json:"file"`
	// Line and Column are 1-based, or 0 when the finding could not be located.
	Line   int `json:"line"`
	Column int `json:"column"`
	// Field is the dotted/indexed schema path, when the check named one.
	Field string `json:"field,omitempty"`
	// Message is the validator's own sentence, unchanged.
	Message string `json:"message"`
}

// String renders the diagnostic in the conventional compiler form.
func (d Diagnostic) String() string {
	if d.Line > 0 {
		return fmt.Sprintf("%s:%d:%d: %s", d.File, d.Line, d.Column, d.Message)
	}
	return fmt.Sprintf("%s: %s", d.File, d.Message)
}

// ResolveManifestPath accepts either a manifest file or a pack directory and
// returns the manifest path. A directory resolves to PackManifestName inside it,
// which is what `graphi extension init` writes.
func ResolveManifestPath(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("extpack: %s: %w", path, err)
	}
	if !info.IsDir() {
		return path, nil
	}
	candidate := filepath.Join(path, PackManifestName)
	if _, err := os.Stat(candidate); err != nil {
		return "", fmt.Errorf("extpack: %s is a directory with no %s in it", path, PackManifestName)
	}
	return candidate, nil
}

// Lint checks a pack manifest and its artifact and returns every diagnostic it
// can produce, manifest findings first.
//
// It never returns an error: a file it cannot read is itself a diagnostic. A
// linter that failed instead of reporting would make its worst case — a pack so
// broken it cannot be opened — the one case with no output.
func Lint(path string) []Diagnostic {
	manifestPath, err := ResolveManifestPath(path)
	if err != nil {
		return []Diagnostic{{File: path, Message: err.Error()}}
	}
	dir := filepath.Dir(manifestPath)
	manifestFile := filepath.Base(manifestPath)

	manifestBytes, err := rootfile.Read(dir, manifestFile, MaxManifestBytes)
	if err != nil {
		var tooLarge *rootfile.TooLargeError
		if errors.As(err, &tooLarge) {
			return []Diagnostic{{File: manifestPath, Message: fmt.Sprintf(
				"extpack: manifest %s is %d bytes, the limit is %d", manifestPath, tooLarge.Size, MaxManifestBytes)}}
		}
		return []Diagnostic{{File: manifestPath, Message: fmt.Sprintf("extpack: read manifest %s: %v", manifestPath, err)}}
	}

	manifestIndex := indexYAMLPositions(manifestBytes)
	m, perr := ParseManifest(manifestBytes)
	if perr != nil {
		return []Diagnostic{parseDiagnostic(manifestPath, perr)}
	}

	out := diagnosticsFor(manifestPath, manifestIndex, m.Checks())
	if len(out) > 0 {
		// The artifact is located and bounded BY the manifest. Reading it against
		// a manifest that does not validate would report findings derived from
		// fields the author still has to fix, which is noise dressed as detail.
		return out
	}

	artifactPath := filepath.Join(dir, m.Artifact.Path)
	limit := m.Limits.MaxOutputBytes
	if limit > MaxArtifactBytes {
		limit = MaxArtifactBytes
	}
	artifactBytes, err := rootfile.Read(dir, m.Artifact.Path, limit)
	if err != nil {
		var tooLarge *rootfile.TooLargeError
		if errors.As(err, &tooLarge) {
			return []Diagnostic{{File: artifactPath, Field: "limits.max_output_bytes", Message: fmt.Sprintf(
				"extpack: artifact %q is %d bytes but the pack declares limits.max_output_bytes = %d: "+
					"a pack that ships more than it declared is refused",
				Bound(m.Artifact.Path), tooLarge.Size, m.Limits.MaxOutputBytes)}}
		}
		return []Diagnostic{{File: artifactPath, Message: fmt.Sprintf(
			"extpack: read artifact %q next to %s: %v", Bound(m.Artifact.Path), manifestPath, err)}}
	}

	if got := HashBytes(artifactBytes); got != m.Artifact.SHA256 {
		out = append(out, diagnosticFor(manifestPath, manifestIndex, manifestErrf("artifact.sha256",
			"extpack: artifact %q sha256 mismatch: the manifest pins %s, the bytes hash to %s",
			Bound(m.Artifact.Path), m.Artifact.SHA256, got)))
	}

	artifactIndex := indexYAMLPositions(artifactBytes)
	p, aerrs := lintArtifact(m.Kind, artifactBytes)
	out = append(out, diagnosticsFor(artifactPath, artifactIndex, aerrs)...)
	if len(aerrs) == 0 {
		if err := checkProvides(m, p); err != nil {
			out = append(out, diagnosticFor(manifestPath, manifestIndex,
				withField(ScopeManifest, "capabilities.provides", err)))
		}
	}
	return out
}

// lintArtifact decodes and checks one artifact against its kind's schema,
// returning every violation rather than the first.
func lintArtifact(kind Kind, data []byte) (payload, []error) {
	var p payload
	switch kind {
	case KindArchitectureRules:
		if err := decodeStrict(data, &p.arch); err != nil {
			return p, []error{err}
		}
		return p, checkArchPayload(p.arch)
	case KindTaintRules:
		if err := decodeStrict(data, &p.taint); err != nil {
			return p, []error{err}
		}
		return p, checkTaintPayload(p.taint)
	default:
		return p, []error{artifactErrf("", "extpack: no artifact decoder for kind %q", Bound(string(kind)))}
	}
}

// diagnosticsFor renders a check list against one file's position index.
func diagnosticsFor(file string, index map[string]position, errs []error) []Diagnostic {
	out := make([]Diagnostic, 0, len(errs))
	for _, err := range errs {
		out = append(out, diagnosticFor(file, index, err))
	}
	return out
}

func diagnosticFor(file string, index map[string]position, err error) Diagnostic {
	d := Diagnostic{File: file, Message: err.Error()}
	fe, ok := AsFieldError(err)
	if !ok {
		// A YAML decode error carries its own line, which is the only position
		// available when the document never became a struct.
		if line := yamlErrorLine(err.Error()); line > 0 {
			d.Line = line
			d.Column = 1
		}
		return d
	}
	d.Field = fe.Field
	if pos, found := lookupPosition(index, fe.Field); found {
		d.Line, d.Column = pos.line, pos.column
	}
	return d
}

// parseDiagnostic renders a manifest that never decoded.
func parseDiagnostic(file string, err error) Diagnostic {
	d := Diagnostic{File: file, Message: err.Error()}
	if line := yamlErrorLine(err.Error()); line > 0 {
		d.Line = line
		d.Column = 1
	}
	return d
}

// yamlErrorLine extracts the line number gopkg.in/yaml.v3 puts in its messages
// ("yaml: line 7: …", "yaml: unmarshal errors:\n  line 7: …"). It is a parse of
// a third-party message and therefore best-effort by construction: a miss costs
// a position, never a dropped finding.
func yamlErrorLine(msg string) int {
	const marker = "line "
	i := strings.Index(msg, marker)
	if i < 0 {
		return 0
	}
	rest := msg[i+len(marker):]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	n, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0
	}
	return n
}

// position is a 1-based location in a YAML document.
type position struct{ line, column int }

// indexYAMLPositions maps every dotted/indexed field path in a YAML document to
// the position of the key (for mapping entries) or the item (for sequences).
//
// It walks the node tree of the SAME bytes the validators decoded, so a path the
// validators produced and a path this index holds describe the same field by
// construction rather than by a second parse of a different shape.
func indexYAMLPositions(data []byte) map[string]position {
	out := map[string]position{}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return out
	}
	if len(doc.Content) == 0 {
		return out
	}
	indexNode(doc.Content[0], "", out)
	return out
}

func indexNode(node *yaml.Node, path string, out map[string]position) {
	if node == nil {
		return
	}
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			child := key.Value
			if path != "" {
				child = path + "." + key.Value
			}
			out[child] = position{line: key.Line, column: key.Column}
			indexNode(value, child, out)
		}
	case yaml.SequenceNode:
		for i, item := range node.Content {
			child := fmt.Sprintf("%s[%d]", path, i)
			out[child] = position{line: item.Line, column: item.Column}
			indexNode(item, child, out)
		}
	}
}

// lookupPosition resolves a field path, falling back to the nearest ancestor the
// index knows. A missing leaf (the commonest case — a field the author simply
// did not write) resolves to its parent, which is where they need to look.
func lookupPosition(index map[string]position, field string) (position, bool) {
	for candidate := field; candidate != ""; candidate = parentPath(candidate) {
		if pos, ok := index[candidate]; ok {
			return pos, true
		}
	}
	return position{}, false
}

func parentPath(field string) string {
	if i := strings.LastIndexByte(field, '.'); i >= 0 {
		return field[:i]
	}
	if i := strings.LastIndexByte(field, '['); i > 0 {
		return field[:i]
	}
	return ""
}
