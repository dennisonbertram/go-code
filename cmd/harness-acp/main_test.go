package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestMain_SuccessNoExit(t *testing.T) {
	withMain(t, func() {
		runMain = func() error { return nil }
		called := false
		exitFunc = func(int) { called = true }
		main()
		if called {
			t.Fatal("unexpected exit")
		}
	})
}
func TestMain_ExitsOnError(t *testing.T) {
	withMain(t, func() {
		runMain = func() error { return errors.New("boom") }
		code := 0
		exitFunc = func(v int) { code = v }
		main()
		if code != 1 {
			t.Fatalf("exit=%d", code)
		}
	})
}
func TestMain_NoExitOnEOF(t *testing.T) {
	withMain(t, func() {
		runMain = func() error { return io.EOF }
		called := false
		exitFunc = func(int) { called = true }
		main()
		if called {
			t.Fatal("unexpected exit")
		}
	})
}
func TestRun(t *testing.T) {
	withMain(t, func() {
		getenvFunc = func(string) string { return "http://example.test" }
		stdinReader = strings.NewReader("")
		stdoutWriter = io.Discard
		if err := run(); err != nil {
			t.Fatal(err)
		}
	})
}
func TestRunWithIO(t *testing.T) {
	if err := runWithIO(context.Background(), strings.NewReader(""), io.Discard, "http://example.test"); err != nil {
		t.Fatal(err)
	}
}
func TestRunDefaultAddr(t *testing.T) {
	withMain(t, func() {
		getenvFunc = func(string) string { return "" }
		stdinReader = strings.NewReader("")
		stdoutWriter = io.Discard
		if err := run(); err != nil {
			t.Fatal(err)
		}
	})
}
func withMain(t *testing.T, fn func()) {
	t.Helper()
	a, b, c, d, e := runMain, exitFunc, getenvFunc, stdinReader, stdoutWriter
	defer func() { runMain, exitFunc, getenvFunc, stdinReader, stdoutWriter = a, b, c, d, e }()
	fn()
}
