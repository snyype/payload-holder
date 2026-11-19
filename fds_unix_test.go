//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// Like ssh with its askpass helper: we hold a pipe's write end without close-on-exec, start a
// long-running child, close our copy, and expect the reader to see EOF at once — not when the
// child exits.
func TestKeepFDsFromChildren(t *testing.T) {
	var p [2]int
	if err := syscall.Pipe(p[:]); err != nil {
		t.Fatal(err)
	}
	r := os.NewFile(uintptr(p[0]), "r")
	defer r.Close()

	keepFDsFromChildren()
	child := exec.Command("sleep", "5")
	if err := child.Start(); err != nil {
		t.Skip("no sleep command:", err)
	}
	defer child.Process.Kill()
	syscall.Close(p[1])

	done := make(chan struct{})
	go func() {
		buf := make([]byte, 1)
		r.Read(buf) // returns at EOF
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the child inherited the pipe: the reader waits until it exits")
	}
}
