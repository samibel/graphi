package model

import (
	"errors"
	"fmt"
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
// the Node's identity and feed the NodeId derivation. Line and Column are
// NON-identity attributes (a node moving within a file keeps its ID). All fields
// are unexported with accessors so a constructed Node cannot be mutated; the
// only way to obtain one is via NewNode, which always derives a consistent ID.
type Node struct {
	id            NodeId
	kind          string
	qualifiedName string
	sourcePath    string // already normalized
	line          int    // non-identity
	column        int    // non-identity
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
