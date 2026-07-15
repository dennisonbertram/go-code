package main

import "fmt"

func main() {
	nodes := []string{"a", "b", "c", "d", "e"}
	edges := [][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}, {"d", "e"}}

	order, err := TopoSort(nodes, edges)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("order:", order)
}
