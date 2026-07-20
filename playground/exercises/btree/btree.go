package main

import "fmt"

// BTreeNode represents a node in the B-Tree
type BTreeNode struct {
	keys     []int
	children []*BTreeNode
	leaf     bool
}

// BTree with root node and minimum degree t
type BTree struct {
	root *BTreeNode
	t    int // min degree
}

// NewBTree creates a B-Tree of order t (min degree)
func NewBTree(t int) *BTree {
	if t < 2 {
		panic("B-tree must have min degree >= 2")
	}
	return &BTree{
		root: &BTreeNode{leaf: true},
		t:    t,
	}
}

// Search checks if key is present in the BTree
func (b *BTree) Search(key int) bool {
	return b.root.search(key)
}

func (n *BTreeNode) search(key int) bool {
	i := 0
	for i < len(n.keys) && key > n.keys[i] {
		i++
	}
	if i < len(n.keys) && key == n.keys[i] {
		return true
	}
	if n.leaf {
		return false
	}
	return n.children[i].search(key)
}

// Insert inserts a key into the BTree
func (b *BTree) Insert(key int) {
	r := b.root
	if len(r.keys) == 2*b.t-1 {
		s := &BTreeNode{leaf: false, children: []*BTreeNode{r}}
		// pad children so root has enough room for all possible splits
		for len(s.children) < 2*b.t {
			s.children = append(s.children, nil)
		}
		b.root = s
		s.splitChild(0, b.t)
		s.insertNonFull(key, b.t)
	} else {
		r.insertNonFull(key, b.t)
	}
}

// insertNonFull inserts a key in a non-full node
func (n *BTreeNode) insertNonFull(key int, t int) {
	i := len(n.keys) - 1
	if n.leaf {
		n.keys = append(n.keys, 0)
		for i >= 0 && key < n.keys[i] {
			n.keys[i+1] = n.keys[i]
			i--
		}
		n.keys[i+1] = key
		return
	}

	for i >= 0 && key < n.keys[i] {
		i--
	}
	i++
	if len(n.children[i].keys) == 2*t-1 {
		for len(n.children) < i+2 {
			n.children = append(n.children, nil)
		}
		n.splitChild(i, t)
		if key > n.keys[i] {
			i++
		}
	}
	n.children[i].insertNonFull(key, t)
	// After any potential split or insert, ensure children has correct length for future splits
	for len(n.children) < len(n.keys)+1 {
		n.children = append(n.children, nil)
	}
}

// splitChild splits the full child y of n at index i
func (n *BTreeNode) splitChild(i int, t int) {
	y := n.children[i]
	z := &BTreeNode{leaf: y.leaf}
	z.keys = append(z.keys, y.keys[t:]...)
	midKey := y.keys[t-1]
	y.keys = y.keys[:t-1]
	if !y.leaf {
		z.children = append(z.children, y.children[t:]...)
		y.children = y.children[:t]
	}

	n.keys = append(n.keys, 0)
	copy(n.keys[i+1:], n.keys[i:])
	n.keys[i] = midKey

	n.children = append(n.children, nil)
	copy(n.children[i+2:], n.children[i+1:])
	n.children[i+1] = z
}

// InOrder returns the keys in sorted order
func (b *BTree) InOrder() []int {
	var res []int
	b.root.inorder(&res)
	return res
}

func (n *BTreeNode) inorder(res *[]int) {
	for i := 0; i < len(n.keys); i++ {
		if !n.leaf {
			n.children[i].inorder(res)
		}
		*res = append(*res, n.keys[i])
	}
	if !n.leaf && len(n.children) > 0 {
		n.children[len(n.keys)].inorder(res)
	}
}

// Delete deletes a key from the BTree
func (b *BTree) Delete(key int) {
	b.root.delete(key, b.t)
	if len(b.root.keys) == 0 && !b.root.leaf {
		b.root = b.root.children[0]
	}
}

func (n *BTreeNode) delete(key int, t int) {
	idx := 0
	for idx < len(n.keys) && key > n.keys[idx] {
		idx++
	}
	if idx < len(n.keys) && n.keys[idx] == key {
		if n.leaf {
			n.keys = append(n.keys[:idx], n.keys[idx+1:]...)
			return
		}
		if len(n.children[idx].keys) >= t {
			pred := n.children[idx].getPredecessor()
			n.keys[idx] = pred
			n.children[idx].delete(pred, t)
		} else if len(n.children[idx+1].keys) >= t {
			succ := n.children[idx+1].getSuccessor()
			n.keys[idx] = succ
			n.children[idx+1].delete(succ, t)
		} else {
			n.merge(idx, t)
			n.children[idx].delete(key, t)
		}
		return
	}
	if n.leaf {
		return // Key not found
	}
	flag := idx == len(n.keys)
	if len(n.children[idx].keys) < t {
		n.fill(idx, t)
	}
	if flag && idx > len(n.keys) {
		n.children[idx-1].delete(key, t)
	} else {
		n.children[idx].delete(key, t)
	}
}

func (n *BTreeNode) getPredecessor() int {
	cur := n
	for !cur.leaf {
		cur = cur.children[len(cur.children)-1]
	}
	return cur.keys[len(cur.keys)-1]
}

func (n *BTreeNode) getSuccessor() int {
	cur := n
	for !cur.leaf {
		cur = cur.children[0]
	}
	return cur.keys[0]
}

func (n *BTreeNode) fill(idx, t int) {
	if idx > 0 && len(n.children[idx-1].keys) >= t {
		n.borrowFromPrev(idx)
	} else if idx < len(n.children)-1 && len(n.children[idx+1].keys) >= t {
		n.borrowFromNext(idx)
	} else {
		if idx < len(n.children)-1 {
			n.merge(idx, t)
		} else {
			n.merge(idx-1, t)
		}
	}
}

func (n *BTreeNode) borrowFromPrev(idx int) {
	child := n.children[idx]
	sibling := n.children[idx-1]
	child.keys = append([]int{n.keys[idx-1]}, child.keys...)
	if !child.leaf {
		child.children = append([]*BTreeNode{sibling.children[len(sibling.children)-1]}, child.children...)
		sibling.children = sibling.children[:len(sibling.children)-1]
	}
	n.keys[idx-1] = sibling.keys[len(sibling.keys)-1]
	sibling.keys = sibling.keys[:len(sibling.keys)-1]
}

func (n *BTreeNode) borrowFromNext(idx int) {
	child := n.children[idx]
	sibling := n.children[idx+1]
	child.keys = append(child.keys, n.keys[idx])
	if !child.leaf {
		child.children = append(child.children, sibling.children[0])
		sibling.children = sibling.children[1:]
	}
	n.keys[idx] = sibling.keys[0]
	sibling.keys = sibling.keys[1:]
}

func (n *BTreeNode) merge(idx int, t int) {
	child := n.children[idx]
	sibling := n.children[idx+1]
	child.keys = append(child.keys, n.keys[idx])
	child.keys = append(child.keys, sibling.keys...)
	if !child.leaf {
		child.children = append(child.children, sibling.children...)
	}
	n.keys = append(n.keys[:idx], n.keys[idx+1:]...)
	n.children = append(n.children[:idx+1], n.children[idx+2:]...)
}
