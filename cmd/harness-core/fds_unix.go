//go:build !windows

package main

import "syscall"

// markInheritedFDsCloseOnExec sets close-on-exec on every file descriptor the
// core inherited beyond stdio. A parent such as Electron leaks descriptors
// (pipes, files) that would otherwise pass through to every process the core
// starts, notably the long-lived tmux server, keeping the parent's pipes open
// after both the parent and the core are gone.
func markInheritedFDsCloseOnExec() {
	for fd := 3; fd < 1024; fd++ {
		syscall.CloseOnExec(fd) // EBADF for unused descriptors is fine
	}
}
