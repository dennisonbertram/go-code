package main

import "fmt"

func main() {
	lines := Wrap("go is a small, fast, and fun language for building tools.", 20)
	for _, l := range lines {
		fmt.Println(l)
	}
}
