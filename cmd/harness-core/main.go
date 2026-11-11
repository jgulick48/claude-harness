// Command harness-core is the backend process launched by the desktop UI.
// See docs/CORE_PROTOCOL.md.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jgulick48/claude-harness/internal/api"
	"github.com/jgulick48/claude-harness/internal/version"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("harness-core", flag.ContinueOnError)
	fs.SetOutput(stderr)
	listen := fs.String("listen", "127.0.0.1:0", "address to listen on (loopback only)")
	showVersion := fs.Bool("version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, version.Version)
		return 0
	}

	logger := slog.New(slog.NewTextHandler(stderr, nil))

	token := getenv("HARNESS_TOKEN")
	if token == "" {
		logger.Error("HARNESS_TOKEN is not set")
		return 2
	}
	host, _, err := net.SplitHostPort(*listen)
	if err != nil {
		logger.Error("invalid --listen", "err", err)
		return 2
	}
	switch host {
	case "127.0.0.1", "::1", "localhost":
	default:
		logger.Error("--listen host must be loopback", "host", host)
		return 2
	}

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		logger.Error("listen failed", "err", err)
		return 1
	}
	addr := ln.Addr().(*net.TCPAddr)

	backends := newBackends(logger, getenv)
	srv := &http.Server{Handler: api.NewServer(token, logger, backends), ReadHeaderTimeout: 10 * time.Second}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	_ = json.NewEncoder(stdout).Encode(map[string]any{
		"type":     "ready",
		"addr":     addr.String(),
		"port":     addr.Port,
		"version":  version.Version,
		"protocol": version.Protocol,
	})
	logger.Info("listening", "addr", net.JoinHostPort(addr.IP.String(), strconv.Itoa(addr.Port)))

	// The parent closing stdin (or dying) means shut down.
	stdinEOF := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, stdin)
		close(stdinEOF)
	}()

	code := 0
	select {
	case <-ctx.Done():
		logger.Info("shutting down", "reason", "signal")
	case <-stdinEOF:
		logger.Info("shutting down", "reason", "stdin closed")
	case err := <-serveErr:
		logger.Error("server failed", "err", err)
		for _, b := range backends {
			_ = b.Close()
		}
		return 1
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		// Hijacked WebSocket connections are not tracked by Shutdown; force close.
		logger.Warn("graceful shutdown incomplete", "err", err)
		_ = srv.Close()
	}
	for _, b := range backends {
		if err := b.Close(); err != nil {
			logger.Warn("backend close failed", "host", b.ID(), "err", err)
		}
	}
	return code
}
