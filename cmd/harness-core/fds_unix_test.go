//go:build !windows

package main

import (
	"syscall"
	"testing"
)

func TestMarkInheritedFDsCloseOnExec(t *testing.T) {
	var p [2]int
	if err := syscall.Pipe(p[:]); err != nil { // plain pipe(2): inheritable
		t.Fatal(err)
	}
	defer syscall.Close(p[0])
	defer syscall.Close(p[1])
	if p[0] < 3 || p[0] >= 1024 {
		t.Skipf("unexpected fd %d", p[0])
	}
	markInheritedFDsCloseOnExec()
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(p[0]), syscall.F_GETFD, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
	if flags&syscall.FD_CLOEXEC == 0 {
		t.Fatal("fd is still inheritable")
	}
}
