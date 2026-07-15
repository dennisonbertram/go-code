package main

import (
	"encoding/json"
	"fmt"
)

func main() {
	m := Money{Cents: 2999, Currency: "USD"}
	b, err := json.Marshal(m)
	if err != nil {
		fmt.Println("marshal error:", err)
		return
	}
	fmt.Println("json:", string(b))

	var back Money
	if err := json.Unmarshal(b, &back); err != nil {
		fmt.Println("unmarshal error:", err)
		return
	}
	fmt.Println("round-tripped:", back)
}
