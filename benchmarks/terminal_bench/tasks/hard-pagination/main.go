package main

import "fmt"

func main() {
	items := []string{"a", "b", "c", "d", "e"}
	pageItems, totalPages := Paginate(items, 2, 1)
	fmt.Println("page 1:", pageItems, "totalPages:", totalPages)
}
