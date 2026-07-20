package main

import (
	"fmt"
	"runtime"
	"time"
)

func startWorker() {
	ch := make(chan int, 1)
	go func() {
		val := <-ch
		fmt.Println(val)
	}()
	ch <- 42
}

func main() {
	before := runtime.NumGoroutine()
	for i := 0; i < 10; i++ {
		startWorker()
	}
	time.Sleep(10 * time.Millisecond)
	after := runtime.NumGoroutine()
	fmt.Printf("before=%d after=%d leaked=%d\n", before, after, after-before)
}
