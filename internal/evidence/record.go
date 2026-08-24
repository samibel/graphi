package evidence

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Rerecord is one sha this run re-blessed: which row, which artifact, old → new.
// AC-12 exists because failure mode 1 happened silently; every re-record is
// printed, and the story that runs it pastes the report into its record.
type Rerecord struct {
	GateID string
	Path   string
	Field  string // "sha" or "provenance"
	Old    string
	New    string
}

// RecordCitations rewrites stale recorded blob shas in the evidence index from the
// current HEAD blobs and returns the list of rows it touched. It is a TEXTUAL
// rewrite of the checked-in YAML — comments, ordering and formatting survive —
// and it changes nothing but the sha characters themselves.
//
// The declared scope, deliberately narrow:
//
//   - a gate row's `sha:` is re-recorded only when it is a GIT BLOB sha that does
//     not match any cited path; the new value is the HEAD blob of the row's first
//     cited repository file. A commit sha, a sha256 content digest and a blank are
//     left alone — they are different kinds of claim and re-recording them would
//     be a re-blessing nobody asked for.
//   - a `<path> @ blob <sha>` binding in `provenance:` is re-recorded when it does
//     not match HEAD.
//   - a 64-hex sha256 CONTENT DIGEST that is the digest of nothing the row cites
//     is re-recorded to the digest of the row's first cited repository file. This
//     kind is included because it has the same failure shape as a stale blob sha
//     (GA-LANG-python-G4 records a digest that matches no version of the file it
//     cites, on any branch), and leaving it out would leave a class of unresolvable
//     sha with no mechanical way to drain it.
//
// dryRun computes the plan without writing.
func RecordCitations(root string, dryRun bool) ([]Rerecord, error) {
	yamlPath := filepath.Join(root, filepath.FromSlash(EvidenceYAMLPath))
	raw, err := os.ReadFile(yamlPath)
	if err != nil {
		return nil, fmt.Errorf("evidence: read %s: %w", EvidenceYAMLPath, err)
	}
	idx, err := parseIndexYAML(string(raw))
	if err != nil {
		return nil, fmt.Errorf("evidence: parse %s: %w", EvidenceYAMLPath, err)
	}

	g := NewGit(root)
	plan := map[string]string{} // gate id -> new sha
	for _, gate := range idx.Gates {
		newSHA, ok, err := plannedRowSHA(g, gate)
		if err != nil {
			return nil, err
		}
		if ok {
			plan[gate.ID] = newSHA
		}
	}

	var touched []Rerecord
	lines := strings.Split(string(raw), "\n")
	curID := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(stripComment(line))
		if strings.HasPrefix(trimmed, "- id:") || strings.HasPrefix(trimmed, "id:") {
			if _, v, err := splitField(strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))); err == nil {
				curID = v
			}
			continue
		}
		if strings.HasPrefix(trimmed, "sha:") {
			want, ok := plan[curID]
			if !ok {
				continue
			}
			_, old, err := splitField(trimmed)
			if err != nil || old == want {
				continue
			}
			lines[i] = strings.Replace(line, old, want, 1)
			touched = append(touched, Rerecord{GateID: curID, Field: "sha", Old: old, New: want})
			continue
		}
		if strings.HasPrefix(trimmed, "provenance:") {
			updated := line
			for _, b := range ProvenanceBindings(line) {
				path, old := b[0], b[1]
				if !hasCitationRoot(path) || !hasCitationExtension(path) {
					continue
				}
				isFile, err := g.IsFileAtHEAD(path)
				if err != nil || !isFile {
					continue
				}
				actual, err := g.BlobSHA(path)
				if err != nil || shaPrefixMatch(old, actual) {
					continue
				}
				updated = strings.Replace(updated, "@ blob "+old, "@ blob "+actual[:len(old)], 1)
				touched = append(touched, Rerecord{GateID: curID, Field: "provenance", Path: path, Old: old, New: actual[:len(old)]})
			}
			lines[i] = updated
		}
	}
	if len(touched) == 0 || dryRun {
		return touched, nil
	}
	if err := os.WriteFile(yamlPath, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return touched, fmt.Errorf("evidence: write %s: %w", EvidenceYAMLPath, err)
	}
	return touched, nil
}

// plannedRowSHA decides whether a row's sha: needs re-recording and to what.
func plannedRowSHA(g *Git, gate Gate) (string, bool, error) {
	sha := strings.TrimSpace(gate.SHA)
	if sha == "" {
		return "", false, nil
	}
	typ, err := g.ObjectType(sha)
	if err == nil && typ != "blob" {
		return "", false, nil // a commit or tag sha names a candidate, not bytes: not ours to re-bless
	}
	digest := err != nil && len(sha) == 64
	if err != nil && !digest {
		return "", false, nil
	}
	var first string
	for _, c := range ClassifyURI(gate.EvidenceURI) {
		if c.Kind != KindRepoPath && c.Kind != KindTestSymbol && c.Kind != KindDocAnchor {
			continue
		}
		isFile, ferr := g.IsFileAtHEAD(c.Path)
		if ferr != nil || !isFile {
			continue
		}
		var (
			actual string
			berr   error
		)
		if digest {
			actual, berr = g.ContentDigest(c.Path)
		} else {
			actual, berr = g.BlobSHA(c.Path)
		}
		if berr != nil {
			continue
		}
		if shaPrefixMatch(sha, actual) {
			return "", false, nil // already correct
		}
		if first == "" {
			first = actual
		}
	}
	if first == "" {
		return "", false, nil
	}
	return first[:len(sha)], true, nil
}

// FormatRerecords renders the AC-12 audit report: every row touched, old → new.
func FormatRerecords(rs []Rerecord) string {
	if len(rs) == 0 {
		return "record-citations: nothing to re-record — every recorded blob sha is already the sha of what it cites.\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "record-citations: re-recorded %d sha(s) from HEAD:\n", len(rs))
	for _, r := range rs {
		if r.Path != "" {
			fmt.Fprintf(&b, "    - [%s] %s (%s): %s -> %s\n", r.GateID, r.Field, r.Path, r.Old, r.New)
			continue
		}
		fmt.Fprintf(&b, "    - [%s] %s: %s -> %s\n", r.GateID, r.Field, r.Old, r.New)
	}
	b.WriteString("\nEvery line above is a re-blessing. Paste this report into the story record.\n")
	return b.String()
}
