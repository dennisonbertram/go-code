package main

import (
	"context"
	"fmt"
)

func main() {
	jobs := []int{1, 2, 3, 4, 5}
	out, err := Process(context.Background(), jobs, 3, func(x int) int { return x * x })
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("results:", out)
}
