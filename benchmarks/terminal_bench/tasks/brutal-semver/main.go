package main

import "fmt"

func main() {
	a, b := "1.0.0-alpha", "1.0.0"
	fmt.Printf("Compare(%q, %q) = %d\n", a, b, Compare(a, b))
}
