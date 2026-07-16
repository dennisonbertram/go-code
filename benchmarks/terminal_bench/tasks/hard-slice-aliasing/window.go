package main

// Windows returns all consecutive windows of length `size` over data, i.e.
// data[i:i+size] for every i in 0..len(data)-size.
//
// Contract:
//   - Each returned window has length `size` and contains the values
//     data[i], data[i+1], ..., data[i+size-1].
//   - The returned windows must be independent: mutating or appending to any
//     returned window must not change data or any other returned window.
//   - If size <= 0 or size > len(data), Windows returns an empty [][]int.
//
// BUG: this implementation returns data[i:i+size] directly. Every returned
// window therefore aliases data's backing array (and, because it uses a
// two-index slice expression, also shares leftover capacity with it). Writing
// to or appending to one returned window can silently corrupt the input slice
// and/or other windows.
func Windows(data []int, size int) [][]int {
	if size <= 0 || size > len(data) {
		return [][]int{}
	}
	out := make([][]int, 0, len(data)-size+1)
	for i := 0; i+size <= len(data); i++ {
		out = append(out, data[i:i+size])
	}
	return out
}
