package main

import "fmt"

func main() {
	a := NewAccount(1, 1000)
	b := NewAccount(2, 500)

	if err := Transfer(a, b, 200); err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("a balance:", a.Balance())
	fmt.Println("b balance:", b.Balance())
}
