package main

// BTree node structure and main tree structure
type btreeNode struct {
	keys     []int
	children []*btreeNode
	leaf     bool
}

type BTree struct {
	root *btreeNode
	t    int // Minimum degree
}

// NewBTree creates a new B-tree of minimum degree t
func NewBTree(t int) *BTree {
	if t < 2 {
		panic("BTree minimum degree must be at least 2")
	}
	node := &btreeNode{leaf: true, keys: []int{}}
	return &BTree{root: node, t: t}
}

// Search checks if key is present in the B-tree
func (bt *BTree) Search(key int) bool {
	return search(bt.root, key)
}

func search(node *btreeNode, key int) bool {
	i := 0
	for i < len(node.keys) && key > node.keys[i] {
		i++
	}
	if i < len(node.keys) && key == node.keys[i] {
		return true
	}
	if node.leaf {
		return false
	}
	return search(node.children[i], key)
}

// Insert inserts key into the B-tree
func (bt *BTree) Insert(key int) {
	root := bt.root
	if len(root.keys) == 2*bt.t-1 {
		s := &btreeNode{children: []*btreeNode{root}, leaf: false}
		bt.splitChild(s, 0)
		bt.root = s
		bt.insertNonFull(s, key)
	} else {
		bt.insertNonFull(root, key)
	}
}

func (bt *BTree) insertNonFull(node *btreeNode, key int) {
	i := len(node.keys) - 1
	if node.leaf {
		node.keys = append(node.keys, 0)
		for i >= 0 && key < node.keys[i] {
			node.keys[i+1] = node.keys[i]
			i--
		}
		node.keys[i+1] = key
		return
	}
	for i >= 0 && key < node.keys[i] {
		i--
	}
	i++
	if len(node.children[i].keys) == 2*bt.t-1 {
		bt.splitChild(node, i)
		if key > node.keys[i] {
			i++
		}
	}
	bt.insertNonFull(node.children[i], key)
}

func (bt *BTree) splitChild(parent *btreeNode, i int) {
	t := bt.t
	full := parent.children[i]
	z := &btreeNode{leaf: full.leaf}
	// z gets full.keys[t:]
	z.keys = append(z.keys, full.keys[t:]...)
	// If not leaf, z gets full.children[t:]
	if !full.leaf {
		z.children = append(z.children, full.children[t:]...)
	}
	// Save middle key before truncating
	mid := full.keys[t-1]
	full.keys = full.keys[:t-1]
	if !full.leaf {
		full.children = full.children[:t]
	}
	// Insert z as a sibling after full
	parent.children = append(parent.children[:i+1], append([]*btreeNode{z}, parent.children[i+1:]...)...)
	// Push up the splitting key
	parent.keys = append(parent.keys[:i], append([]int{mid}, parent.keys[i:]...)...)
}


// InOrder returns all keys in sorted order
func (bt *BTree) InOrder() []int {
	var out []int
	inorder(bt.root, &out)
	return out
}

func inorder(node *btreeNode, out *[]int) {
	for i := 0; i < len(node.keys); i++ {
		if !node.leaf {
			inorder(node.children[i], out)
		}
		*out = append(*out, node.keys[i])
	}
	if !node.leaf {
		inorder(node.children[len(node.keys)], out)
	}
}

// Delete key from B-tree
func (bt *BTree) Delete(key int) {
	bt.delete(bt.root, key)
	if len(bt.root.keys) == 0 && !bt.root.leaf {
		bt.root = bt.root.children[0]
	}
}

func (bt *BTree) delete(node *btreeNode, key int) {
	t := bt.t
	i := 0
	for i < len(node.keys) && key > node.keys[i] {
		i++
	}
	if i < len(node.keys) && node.keys[i] == key {
		if node.leaf {
			// Case 1: Key in leaf
			node.keys = append(node.keys[:i], node.keys[i+1:]...)
			return
		}
		// Case 2: Key in internal node
		if len(node.children[i].keys) >= t {
			pred := node.children[i]
			for !pred.leaf {
				pred = pred.children[len(pred.keys)]
			}
			k := pred.keys[len(pred.keys)-1]
			node.keys[i] = k
			bt.delete(node.children[i], k)
			return
		} else if len(node.children[i+1].keys) >= t {
			succ := node.children[i+1]
			for !succ.leaf {
				succ = succ.children[0]
			}
			k := succ.keys[0]
			node.keys[i] = k
			bt.delete(node.children[i+1], k)
			return
		} else {
			// Merge
			bt.merge(node, i)
			bt.delete(node.children[i], key)
			return
		}
	}
	// Key not found in this node
	if node.leaf {
		return
	}
	child := node.children[i]
	if len(child.keys) == t-1 {
		var left *btreeNode
		var right *btreeNode
		if i > 0 {
			left = node.children[i-1]
		}
		if i < len(node.children)-1 {
			right = node.children[i+1]
		}
		if left != nil && len(left.keys) >= t {
			// Borrow from left
			child.keys = append([]int{node.keys[i-1]}, child.keys...)
			if !left.leaf {
				child.children = append([]*btreeNode{left.children[len(left.children)-1]}, child.children...)
				left.children = left.children[:len(left.children)-1]
			}
			node.keys[i-1] = left.keys[len(left.keys)-1]
			left.keys = left.keys[:len(left.keys)-1]
		} else if right != nil && len(right.keys) >= t {
			// Borrow from right
			child.keys = append(child.keys, node.keys[i])
			node.keys[i] = right.keys[0]
			right.keys = right.keys[1:]
			if !right.leaf {
				child.children = append(child.children, right.children[0])
				right.children = right.children[1:]
			}
		} else {
			// Merge with left or right
			if left != nil {
				bt.merge(node, i-1)
				child = node.children[i-1]
			} else if right != nil {
				bt.merge(node, i)
			}
		}
	}
	bt.delete(child, key)
}

func (bt *BTree) merge(parent *btreeNode, idx int) {
	child := parent.children[idx]
	sibling := parent.children[idx+1]
	child.keys = append(child.keys, parent.keys[idx])
	child.keys = append(child.keys, sibling.keys...)
	if !child.leaf {
		child.children = append(child.children, sibling.children...)
	}
	parent.keys = append(parent.keys[:idx], parent.keys[idx+1:]...)
	parent.children = append(parent.children[:idx+1], parent.children[idx+2:]...)
}
