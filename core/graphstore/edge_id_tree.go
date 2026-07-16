package graphstore

import "github.com/samibel/graphi/core/model"

// edgeIDTree is a deterministic treap used by MemStore's bounded adjacency
// indexes. Writes are expected O(log n), prefix reads are O(log n + limit), and
// no full adjacency scan is needed. A treap is used instead of sorted slices:
// inserting random content-addressed EdgeIds into a high-degree slice would be
// O(n²) over ingest and directly violate the speed goal this index serves.
//
// Priority is a stable avalanche hash independent of EdgeId's lexical order.
// This gives a reproducible balanced shape without global randomness or an
// external dependency. The store lock owns all mutation and traversal.
type edgeIDTree struct {
	root *edgeIDTreeNode
	size int
}

type edgeIDTreeNode struct {
	id       model.EdgeId
	priority uint64
	left     *edgeIDTreeNode
	right    *edgeIDTreeNode
}

func (t *edgeIDTree) len() int {
	if t == nil {
		return 0
	}
	return t.size
}

func (t *edgeIDTree) insert(id model.EdgeId) {
	var added bool
	t.root, added = insertEdgeIDNode(t.root, id)
	if added {
		t.size++
	}
}

func insertEdgeIDNode(root *edgeIDTreeNode, id model.EdgeId) (*edgeIDTreeNode, bool) {
	if root == nil {
		return &edgeIDTreeNode{id: id, priority: edgeIDPriority(id)}, true
	}
	if id == root.id {
		return root, false
	}
	var added bool
	if id < root.id {
		root.left, added = insertEdgeIDNode(root.left, id)
		if root.left.priority < root.priority {
			root = rotateEdgeIDRight(root)
		}
	} else {
		root.right, added = insertEdgeIDNode(root.right, id)
		if root.right.priority < root.priority {
			root = rotateEdgeIDLeft(root)
		}
	}
	return root, added
}

func (t *edgeIDTree) delete(id model.EdgeId) {
	var removed bool
	t.root, removed = deleteEdgeIDNode(t.root, id)
	if removed {
		t.size--
	}
}

func deleteEdgeIDNode(root *edgeIDTreeNode, id model.EdgeId) (*edgeIDTreeNode, bool) {
	if root == nil {
		return nil, false
	}
	if id < root.id {
		var removed bool
		root.left, removed = deleteEdgeIDNode(root.left, id)
		return root, removed
	}
	if id > root.id {
		var removed bool
		root.right, removed = deleteEdgeIDNode(root.right, id)
		return root, removed
	}
	return mergeEdgeIDNodes(root.left, root.right), true
}

func mergeEdgeIDNodes(left, right *edgeIDTreeNode) *edgeIDTreeNode {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	if left.priority < right.priority {
		left.right = mergeEdgeIDNodes(left.right, right)
		return left
	}
	right.left = mergeEdgeIDNodes(left, right.left)
	return right
}

func rotateEdgeIDRight(root *edgeIDTreeNode) *edgeIDTreeNode {
	next := root.left
	root.left = next.right
	next.right = root
	return next
}

func rotateEdgeIDLeft(root *edgeIDTreeNode) *edgeIDTreeNode {
	next := root.right
	root.right = next.left
	next.left = root
	return next
}

// first returns at most limit IDs in canonical order. Its iterative traversal
// touches only the left spine plus emitted nodes, avoiding recursion and a
// degree-sized result allocation.
func (t *edgeIDTree) first(limit int) []model.EdgeId {
	if t == nil || limit <= 0 || t.root == nil {
		return nil
	}
	if limit > t.size {
		limit = t.size
	}
	out := make([]model.EdgeId, 0, limit)
	stack := make([]*edgeIDTreeNode, 0, 32)
	cur := t.root
	for (cur != nil || len(stack) > 0) && len(out) < limit {
		for cur != nil {
			stack = append(stack, cur)
			cur = cur.left
		}
		cur = stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		out = append(out, cur.id)
		cur = cur.right
	}
	return out
}

func edgeIDPriority(id model.EdgeId) uint64 {
	return stringPriority(string(id))
}
