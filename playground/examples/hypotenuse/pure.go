package main

import (
	"fmt"
	"math"
)

func Hypotenuse(a, b float64) float64 {
	return math.Sqrt(a*a + b*b)
}

func main() {
	fmt.Printf("%.4f\n", Hypotenuse(3, 4))
}
