package model

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// NodeId is the fixed-width 16-char lowercase-hex xxhash64 identity of a Node,
// derived from its versioned canonical identity form. It is a non-cryptographic
// content identifier (see package doc).
type NodeId string

// ErrInvalidNode is the typed sentinel wrapped by NewNode validation failures.
var ErrInvalidNode = errors.New("model: invalid node")

// Node is graphi's canonical, immutable graph node value type.
//
// Identity fields — Kind, QualifiedName and the normalized SourcePath — define
// the Node's identity and feed the NodeId derivation. Line, Column and Meta are
// NON-identity attributes (a node moving within a file, or gaining annotation
// metadata, keeps its ID). All fields are unexported with accessors so a
// constructed Node cannot be mutated; the only way to obtain one is via NewNode
// (which always derives a consistent ID) plus WithMeta for the non-identity
// metadata rider.
type Node struct {
	id            NodeId
	kind          string
	qualifiedName string
	sourcePath    string   // already normalized
	line          int      // non-identity
	column        int      // non-identity
	meta          NodeMeta // non-identity (annotations/flags); MUST NOT enter deriveID
}

// NodeMeta is a compact, JSON-serializable rider of NON-identity node metadata:
// source-level annotations (e.g. "Test", "Bean", "Configuration") and flags
// (e.g. "static", "main", "test_path"). It is a pure function of the node's
// source, so full and incremental ingests produce identical meta (byte-parity).
// Both slices are sorted+deduped at construction so the encoding is
// deterministic. The zero value carries no metadata.
type NodeMeta struct {
	Annotations []string `json:"annotations,omitempty"`
	Flags       []string `json:"flags,omitempty"`
}

// NewNodeMeta builds a NodeMeta, sorting and de-duplicating both slices (and
// dropping empty strings) so identical logical metadata always encodes to the
// same bytes regardless of discovery order.
func NewNodeMeta(annotations, flags []string) NodeMeta {
	return NodeMeta{
		Annotations: sortedDedupNonEmpty(annotations),
		Flags:       sortedDedupNonEmpty(flags),
	}
}

// IsZero reports whether the meta carries no annotations and no flags. A
// zero NodeMeta serializes to nothing (see the omitempty wire fields) so a
// node without metadata is byte-identical to a pre-meta node.
func (m NodeMeta) IsZero() bool { return len(m.Annotations) == 0 && len(m.Flags) == 0 }

// sortedDedupNonEmpty returns a sorted, de-duplicated copy of in with empty
// strings removed, or nil when nothing survives (so omitempty elides it).
func sortedDedupNonEmpty(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

// NodeKind identifies the category of a node (e.g. "function", "type",
// "package"). It is part of canonical identity.
type NodeKind = string

// NewNode constructs an immutable Node from its canonical identity fields. The
// supplied sourcePath is normalized to repo-relative POSIX form (pure string
// handling, no I/O) before it enters identity, so the same logical entity yields
// the same NodeId across machines. Kind and qualifiedName must be non-empty
// after trimming. The NodeId is derived deterministically from the versioned
// canonical identity form.
func NewNode(kind, qualifiedName, sourcePath string, line, column int) (Node, error) {
	if strings.TrimSpace(kind) == "" {
		return Node{}, fmt.Errorf("%w: kind must not be empty", ErrInvalidNode)
	}
	if strings.TrimSpace(qualifiedName) == "" {
		return Node{}, fmt.Errorf("%w: qualifiedName must not be empty", ErrInvalidNode)
	}
	if line < 0 {
		return Node{}, fmt.Errorf("%w: line must not be negative", ErrInvalidNode)
	}
	if column < 0 {
		return Node{}, fmt.Errorf("%w: column must not be negative", ErrInvalidNode)
	}
	normPath := NormalizePath(sourcePath)
	n := Node{
		kind:          kind,
		qualifiedName: qualifiedName,
		sourcePath:    normPath,
		line:          line,
		column:        column,
	}
	n.id = NodeId(deriveID(nodeIDDomainTag, n.kind, n.qualifiedName, n.sourcePath))
	return n, nil
}

// ID returns the deterministic NodeId.
func (n Node) ID() NodeId { return n.id }

// Kind returns the node kind (identity field).
func (n Node) Kind() string { return n.kind }

// QualifiedName returns the fully qualified name (identity field).
func (n Node) QualifiedName() string { return n.qualifiedName }

// SourcePath returns the normalized repo-relative POSIX source path (identity field).
func (n Node) SourcePath() string { return n.sourcePath }

// Line returns the 1-based line (non-identity attribute).
func (n Node) Line() int { return n.line }

// Column returns the 1-based column (non-identity attribute).
func (n Node) Column() int { return n.column }

// Meta returns the node's NON-identity metadata rider (annotations/flags). A
// node constructed without metadata returns the zero NodeMeta.
func (n Node) Meta() NodeMeta { return n.meta }

// WithMeta returns a COPY of the node with its metadata set to m. The NodeId is
// unchanged: meta is a non-identity attribute and never enters deriveID, so
// identity byte-parity holds before and after attaching meta.
func (n Node) WithMeta(m NodeMeta) Node {
	n.meta = m
	return n
}
