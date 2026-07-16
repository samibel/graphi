package graphstore

// kindTree is the string-key counterpart to edgeIDTree. MemStore uses it to
// enumerate only the first N distinct incident kinds for an unfiltered bounded
// read. A map+sort would inspect every distinct kind and let an adversarial
// one-edge-per-kind endpoint turn limit=1 back into degree-shaped work.
type kindTree struct {
	root *kindTreeNode
	size int
}

type kindTreeNode struct {
	value    string
	priority uint64
	left     *kindTreeNode
	right    *kindTreeNode
}

func (t *kindTree) len() int {
	if t == nil {
		return 0
	}
	return t.size
}

func (t *kindTree) insert(value string) {
	var added bool
	t.root, added = insertKindNode(t.root, value)
	if added {
		t.size++
	}
}

func insertKindNode(root *kindTreeNode, value string) (*kindTreeNode, bool) {
	if root == nil {
		return &kindTreeNode{value: value, priority: stringPriority(value)}, true
	}
	if value == root.value {
		return root, false
	}
	var added bool
	if value < root.value {
		root.left, added = insertKindNode(root.left, value)
		if root.left.priority < root.priority {
			root = rotateKindRight(root)
		}
	} else {
		root.right, added = insertKindNode(root.right, value)
		if root.right.priority < root.priority {
			root = rotateKindLeft(root)
		}
	}
	return root, added
}

func (t *kindTree) delete(value string) {
	var removed bool
	t.root, removed = deleteKindNode(t.root, value)
	if removed {
		t.size--
	}
}

func deleteKindNode(root *kindTreeNode, value string) (*kindTreeNode, bool) {
	if root == nil {
		return nil, false
	}
	if value < root.value {
		var removed bool
		root.left, removed = deleteKindNode(root.left, value)
		return root, removed
	}
	if value > root.value {
		var removed bool
		root.right, removed = deleteKindNode(root.right, value)
		return root, removed
	}
	return mergeKindNodes(root.left, root.right), true
}

func mergeKindNodes(left, right *kindTreeNode) *kindTreeNode {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	if left.priority < right.priority {
		left.right = mergeKindNodes(left.right, right)
		return left
	}
	right.left = mergeKindNodes(left, right.left)
	return right
}

func rotateKindRight(root *kindTreeNode) *kindTreeNode {
	next := root.left
	root.left = next.right
	next.right = root
	return next
}

func rotateKindLeft(root *kindTreeNode) *kindTreeNode {
	next := root.right
	root.right = next.left
	next.left = root
	return next
}

func (t *kindTree) first(limit int) []string {
	if t == nil || limit <= 0 || t.root == nil {
		return nil
	}
	if limit > t.size {
		limit = t.size
	}
	out := make([]string, 0, limit)
	stack := make([]*kindTreeNode, 0, 32)
	cur := t.root
	for (cur != nil || len(stack) > 0) && len(out) < limit {
		for cur != nil {
			stack = append(stack, cur)
			cur = cur.left
		}
		cur = stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		out = append(out, cur.value)
		cur = cur.right
	}
	return out
}

func stringPriority(value string) uint64 {
	// FNV-1a followed by SplitMix64's avalanche. The priority is deterministic
	// but intentionally decorrelated from lexical key ordering.
	var hash uint64 = 1469598103934665603
	for i := 0; i < len(value); i++ {
		hash ^= uint64(value[i])
		hash *= 1099511628211
	}
	hash += 0x9e3779b97f4a7c15
	hash = (hash ^ (hash >> 30)) * 0xbf58476d1ce4e5b9
	hash = (hash ^ (hash >> 27)) * 0x94d049bb133111eb
	return hash ^ (hash >> 31)
}
