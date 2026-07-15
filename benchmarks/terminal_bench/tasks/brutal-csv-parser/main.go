package main

import "fmt"

func main() {
	input := "name,age\n\"Doe, Jane\",30\n"
	rows, err := ParseCSV(input)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("rows:", rows)
}
