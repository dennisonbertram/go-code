package main

import "time"

func SlowOp() string {
	time.Sleep(50 * time.Millisecond)
	return "done"
}
