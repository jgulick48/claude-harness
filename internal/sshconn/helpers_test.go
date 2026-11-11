package sshconn

import (
	"net"
	"testing"
)

// newBlackhole listens and accepts TCP connections but never writes; returns host:port.
func newBlackhole(t *testing.T) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	var conns []net.Conn
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns = append(conns, c)
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		<-done
		for _, c := range conns {
			c.Close()
		}
	})
	return ln.Addr().String(), nil
}
