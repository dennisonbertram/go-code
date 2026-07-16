package main

import "fmt"

func main() {
	u := User{Name: "Ada", Age: 37}
	if err := Validate(u); err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("valid user:", u.Name)
}
