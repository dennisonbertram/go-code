package merkle

import (
	"crypto/sha256"
)

// MerkleTree represents a Merkle tree.
type MerkleTree struct {
	root   *node
	leaves []*node
}

type node struct {
	hash  []byte
	left  *node
	right *node
}

// Build constructs a MerkleTree from a slice of data blocks and returns a pointer to it.
func Build(data [][]byte) *MerkleTree {
	if len(data) == 0 {
		return &MerkleTree{root: nil, leaves: nil}
	}
	var leaves []*node
	for _, d := range data {
		h := sha256.Sum256(d)
		leaves = append(leaves, &node{hash: h[:]})
	}
	nodes := leaves
	for len(nodes) > 1 {
		var nextLevel []*node
		for i := 0; i < len(nodes); i += 2 {
			if i+1 == len(nodes) {
				nextLevel = append(nextLevel, nodes[i]) // promote odd last node
			} else {
				h := sha256.Sum256(append(nodes[i].hash, nodes[i+1].hash...))
				n := &node{hash: h[:], left: nodes[i], right: nodes[i+1]}
				nextLevel = append(nextLevel, n)
			}
		}
		nodes = nextLevel
	}
	return &MerkleTree{root: nodes[0], leaves: leaves}
}

// Root returns the root hash of the tree.
func (m *MerkleTree) Root() []byte {
	if m == nil || m.root == nil {
		return nil
	}
	return m.root.hash
}

// Proof returns the Merkle proof for the leaf at the given index as a slice of hashes.
func (m *MerkleTree) Proof(index int) [][]byte {
	if m == nil || index < 0 || index >= len(m.leaves) {
		return nil
	}
	var proof [][]byte
	pathIdx := index
	for nodes := m.leaves; len(nodes) > 1; {
		var nextLevel []*node
		for i := 0; i < len(nodes); i += 2 {
			var siblingHash []byte
			isPair := i+1 < len(nodes)
			if isPair {
				h := sha256.Sum256(append(nodes[i].hash, nodes[i+1].hash...))
				n := &node{hash: h[:], left: nodes[i], right: nodes[i+1]}
				nextLevel = append(nextLevel, n)
				if i == pathIdx || i+1 == pathIdx {
					if i == pathIdx {
						siblingHash = nodes[i+1].hash
					} else {
						siblingHash = nodes[i].hash
					}
				}
			} else {
				nextLevel = append(nextLevel, nodes[i])
				if i == pathIdx {
					// No sibling node; skip adding proof at this level.
					continue
				}
			}
			if siblingHash != nil {
				proof = append(proof, siblingHash)
			}
		}
		pathIdx = pathIdx / 2
		nodes = nextLevel
	}
	return proof
}

// Verify returns true if the provided proof validates the leaf against the given root hash.
func Verify(leaf []byte, proof [][]byte, root []byte) bool {
	h := sha256.Sum256(leaf)
	computed := h[:]
	for _, p := range proof {
		// By our construction, the current value is always on the left or right depending on index
		if bytesCompare(computed, p) < 0 {
			t := sha256.Sum256(append(computed, p...))
			computed = t[:]
		} else {
			t := sha256.Sum256(append(p, computed...))
			computed = t[:]
		}
	}
	return equal(computed, root)
}

func bytesCompare(a, b []byte) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return int(a[i]) - int(b[i])
		}
	}
	return len(a) - len(b)
}

func equal(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
