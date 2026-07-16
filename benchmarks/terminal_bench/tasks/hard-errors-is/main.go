package main

import "fmt"

func main() {
	s := NewStore()
	s.Put("a", "1")

	v, err := s.Get("a")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("got:", v)

	if _, err := s.Get("missing"); err != nil {
		fmt.Println("error:", err)
	}
}
