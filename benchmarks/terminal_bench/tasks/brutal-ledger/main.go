package main

import "fmt"

func main() {
	l := NewLedger()
	l.Post("alice", 100)
	l.Post("bob", 50)
	l.Post("alice", 25)

	fmt.Println("alice balance:", l.Balance("alice"))
	fmt.Println("bob balance:", l.Balance("bob"))
	fmt.Println("total:", l.Total())
	fmt.Println("top 2:", l.TopN(2))
}
