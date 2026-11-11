package tmux

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"
)

// fakeProc is a Proc backed by pipes; the test plays tmux.
type fakeProc struct {
	inR   *io.PipeReader
	inW   *io.PipeWriter
	outR  *io.PipeReader
	outW  *io.PipeWriter
	killc chan struct{}
}

func newFakeProc() *fakeProc {
	p := &fakeProc{killc: make(chan struct{}, 1)}
	p.inR, p.inW = io.Pipe()
	p.outR, p.outW = io.Pipe()
	return p
}

func (p *fakeProc) Stdin() io.WriteCloser { return p.inW }
func (p *fakeProc) Stdout() io.Reader     { return p.outR }
func (p *fakeProc) Wait() error           { return nil }
func (p *fakeProc) Kill() error           { p.killc <- struct{}{}; p.outW.Close(); return nil }

func TestControlClientFIFO(t *testing.T) {
	p := newFakeProc()
	c := NewControlClient(p, nil)
	defer c.Close()

	go func() { // fake tmux: unsolicited block, a notification, then replies
		br := bufio.NewReader(p.inR)
		io.WriteString(p.outW, "%begin 1 0 0\n%end 1 0 0\n")
		for i := 1; i <= 3; i++ {
			cmd, _ := br.ReadString('\n')
			if i == 2 {
				io.WriteString(p.outW, "%output %0 hi\n")
				fmt.Fprintf(p.outW, "%%begin 2 %d 1\nbad %s%%error 2 %d 1\n", i, cmd, i)
				continue
			}
			fmt.Fprintf(p.outW, "%%begin 2 %d 1\nreply to %s%%end 2 %d 1\n", i, cmd, i)
		}
	}()

	ctx := context.Background()
	if err := c.Send("first"); err != nil {
		t.Fatal(err)
	}
	_, err := c.Do(ctx, "second")
	var ce *CommandError
	if !errors.As(err, &ce) || ce.Lines[0] != "bad second" {
		t.Fatalf("second: %v", err)
	}
	lines, seq, err := c.DoSeq(ctx, "third")
	if err != nil || len(lines) != 1 || lines[0] != "reply to third" {
		t.Fatalf("third: %v %v", lines, err)
	}
	if seq != 1 {
		t.Errorf("seq = %d, want 1 (one notification before reply)", seq)
	}
	n := <-c.Notifications()
	if n.Name != "output" || n.Seq != 1 || string(n.Data) != "hi" {
		t.Errorf("notification %+v", n)
	}
}

func TestControlClientExitFailsPending(t *testing.T) {
	p := newFakeProc()
	c := NewControlClient(p, nil)
	errc := make(chan error, 1)
	go func() {
		_, err := c.Do(context.Background(), "never answered")
		errc <- err
	}()
	go io.Copy(io.Discard, p.inR)
	time.Sleep(50 * time.Millisecond)
	io.WriteString(p.outW, "%exit\n")
	p.outW.Close()

	select {
	case err := <-errc:
		if !errors.Is(err, ErrClosed) {
			t.Errorf("err = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pending Do not failed")
	}
	if n, ok := <-c.Notifications(); !ok || n.Name != "exit" {
		t.Errorf("want %%exit, got %+v ok=%v", n, ok)
	}
	if _, ok := <-c.Notifications(); ok {
		t.Error("notifications not closed")
	}
	if _, err := c.Do(context.Background(), "x"); !errors.Is(err, ErrClosed) {
		t.Errorf("Do after exit: %v", err)
	}
}

func TestControlClientCloseKillsStuckProcess(t *testing.T) {
	p := newFakeProc() // ignores stdin close: never exits by itself
	go io.Copy(io.Discard, p.inR)
	c := NewControlClient(p, nil)
	start := time.Now()
	c.Close()
	if d := time.Since(start); d < time.Second || d > 4*time.Second {
		t.Errorf("Close took %v", d)
	}
	select {
	case <-p.killc:
	default:
		t.Error("process not killed")
	}
}

func TestControlClientRejectsNewline(t *testing.T) {
	p := newFakeProc()
	c := NewControlClient(p, nil)
	defer c.Close()
	if err := c.Send("a\nb"); err == nil {
		t.Error("expected error")
	}
}
