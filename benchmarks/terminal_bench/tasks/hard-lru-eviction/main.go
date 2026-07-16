package main

import "fmt"

func main() {
	c := NewLRU(2)
	c.Put(1, 100)
	c.Put(2, 200)
	if v, ok := c.Get(1); ok {
		fmt.Println("get 1:", v)
	}
	c.Put(3, 300)
	fmt.Println("len:", c.Len())
}
