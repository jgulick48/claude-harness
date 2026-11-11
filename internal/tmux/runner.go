// Package tmux is a small, transport-agnostic tmux client: a Runner abstraction
// for executing tmux, a control-mode (-C) protocol parser and a ControlClient.
package tmux

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Proc is a long-lived tmux process (typically a control-mode client).
type Proc interface {
	Stdin() io.WriteCloser
	Stdout() io.Reader
	// Wait blocks until the process exits. Read Stdout to EOF first.
	Wait() error
	Kill() error
}

// Runner executes tmux subcommands on some host.
type Runner interface {
	// Output runs one tmux command to completion and returns its stdout.
	// On failure the error includes the trimmed stderr.
	Output(ctx context.Context, args ...string) ([]byte, error)
	// Start launches a long-lived tmux process. The context only bounds the
	// launch; use Proc.Kill to stop the process.
	Start(ctx context.Context, args ...string) (Proc, error)
}

// Error is returned by runners when tmux exits unsuccessfully.
type Error struct {
	Stderr string // trimmed
	Err    error  // underlying exec error
}

func (e *Error) Error() string {
	if e.Stderr != "" {
		return e.Stderr
	}
	return e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

// LocalRunner runs the tmux binary on this machine.
type LocalRunner struct {
	Bin    string // default "tmux"
	Socket string // -L socket name; optional
	Config string // -f config file; optional
}

func (r *LocalRunner) command(args []string) (string, []string) {
	bin := r.Bin
	if bin == "" {
		bin = "tmux"
	}
	var full []string
	if r.Socket != "" {
		full = append(full, "-L", r.Socket)
	}
	if r.Config != "" {
		full = append(full, "-f", r.Config)
	}
	return bin, append(full, args...)
}

func (r *LocalRunner) Output(ctx context.Context, args ...string) ([]byte, error) {
	bin, full := r.command(args)
	cmd := exec.CommandContext(ctx, bin, full...)
	cmd.Env = TmuxEnv(os.Environ())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.Bytes(), &Error{Stderr: strings.TrimSpace(stderr.String()), Err: err}
	}
	return stdout.Bytes(), nil
}

func (r *LocalRunner) Start(ctx context.Context, args ...string) (Proc, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bin, full := r.command(args)
	cmd := exec.Command(bin, full...)
	cmd.Env = TmuxEnv(os.Environ())
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	p := &localProc{cmd: cmd, stdin: stdin, stdout: stdout}
	cmd.Stderr = &p.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start tmux: %w", err)
	}
	return p, nil
}

type localProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.Reader
	stderr bytes.Buffer
}

func (p *localProc) Stdin() io.WriteCloser { return p.stdin }
func (p *localProc) Stdout() io.Reader     { return p.stdout }
func (p *localProc) Kill() error           { return p.cmd.Process.Kill() }

func (p *localProc) Wait() error {
	if err := p.cmd.Wait(); err != nil {
		return &Error{Stderr: strings.TrimSpace(p.stderr.String()), Err: err}
	}
	return nil
}

// TmuxEnv returns env without TMUX and TMUX_PANE. When TMUX is set (the core
// or a test was started from inside a tmux session), a tmux command without
// -L/-S talks to *that* server instead of the one we mean, so a stray
// kill-server could take down the user's own tmux. Every tmux we start gets
// this environment.
func TmuxEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
