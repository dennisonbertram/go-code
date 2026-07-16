package main

// Reference solution: each returned window is a freshly allocated, fully
// independent copy of the corresponding slice of data, so mutating or
// appending to a window can never affect data or any other window.
func Windows(data []int, size int) [][]int {
	if size <= 0 || size > len(data) {
		return [][]int{}
	}
	out := make([][]int, 0, len(data)-size+1)
	for i := 0; i+size <= len(data); i++ {
		w := make([]int, size)
		copy(w, data[i:i+size])
		out = append(out, w)
	}
	return out
}
