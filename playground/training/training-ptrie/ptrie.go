package ptrie

// Node is an immutable trie node for persistent trie
// children map is immutable - each insert creates a new node with updated children
// isWord marks end of a word

type Node struct {
	children map[rune]*Node
	isWord   bool
}

// newNode returns a new node with given children and isWord
func newNode(children map[rune]*Node, isWord bool) *Node {
	copyChildren := make(map[rune]*Node, len(children))
	for k, v := range children {
		copyChildren[k] = v
	}
	return &Node{children: copyChildren, isWord: isWord}
}

// Insert returns a new root with word inserted, without modifying the old trie
func Insert(root *Node, word string) *Node {
	if root == nil {
		root = &Node{children: make(map[rune]*Node)}
	}
	return insertHelper(root, []rune(word), 0)
}

func insertHelper(node *Node, runes []rune, idx int) *Node {
	if idx == len(runes) {
		return newNode(node.children, true)
	}
	c := runes[idx]
	child, ok := node.children[c]
	if !ok {
		child = &Node{children: make(map[rune]*Node)}
	}
	newChild := insertHelper(child, runes, idx+1)
	newChildren := make(map[rune]*Node, len(node.children))
	for k, v := range node.children {
		newChildren[k] = v
	}
	newChildren[c] = newChild
	return &Node{children: newChildren, isWord: node.isWord}
}

// Search returns true if word exists in the trie rooted at root
func Search(root *Node, word string) bool {
	node := root
	for _, c := range word {
		if node == nil {
			return false
		}
		child, ok := node.children[c]
		if !ok {
			return false
		}
		node = child
	}
	return node != nil && node.isWord
}
