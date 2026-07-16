package main

import "fmt"

func main() {
	c := NewCache()

	v := c.GetOrCompute("answer", func() int {
		fmt.Println("computing...")
		return 42
	})
	fmt.Println("value:", v)

	// Second call for the same key must not compute again.
	v2 := c.GetOrCompute("answer", func() int {
		fmt.Println("this should never print")
		return -1
	})
	fmt.Println("value:", v2)
}
