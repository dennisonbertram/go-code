package main

import "fmt"

func main() {
	data := []int{1, 2, 3, 4, 5}
	windows := Windows(data, 3)
	fmt.Println("windows:", windows)
}
