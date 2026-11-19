//go:build !windows

package main

import "syscall"

// keepFDsFromChildren marks every inherited file descriptor above stderr close-on-exec, so programs
// payload starts don't inherit them. ssh runs the askpass helper with an extra copy of the pipe it
// reads the answer from, and reads until every copy is closed: a clipboard holder (payload's own,
// xclip or wl-copy) that inherited it and kept running would leave ssh waiting forever.
func keepFDsFromChildren() {
	for fd := 3; fd < 1024; fd++ {
		syscall.CloseOnExec(fd) // EBADF for unused numbers is fine
	}
}
