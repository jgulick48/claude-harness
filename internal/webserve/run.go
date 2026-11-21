// Package webserve implements `harness-core serve`: the browser-facing mode
// (login, sessions, static UI, WebSocket proxy). See docs/WEB.md.
package webserve

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/jgulick48/claude-harness/internal/version"
)

// Deps is what serve mode needs from the host program.
type Deps struct {
	// NewAPI builds the unchanged api handler (and the thing to Close at
	// shutdown). withLocal says whether the local tmux host is included.
	NewAPI func(token string, logger *slog.Logger, withLocal bool) (http.Handler, io.Closer, error)
}

// LookupEnv is os.LookupEnv-shaped, so "unset" and "empty" can differ.
type LookupEnv func(string) (string, bool)

func (l LookupEnv) get(k string) string { v, _ := l(k); return v }

// Config is the parsed serve configuration.
type Config struct {
	Listen, Data, PublicURL, TLSCert, TLSKey, WebDir string
	TrustProxy, Local, LocalLogin                    bool
	AdminUser, AdminPassword                         string
	OIDC                                             *OIDCConfig
}

func envBool(v string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// secret reads NAME, or the file named by NAME_FILE.
func secret(env LookupEnv, name string) (string, error) {
	if f := env.get(name + "_FILE"); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return "", fmt.Errorf("%s_FILE: %w", name, err)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	return env.get(name), nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ParseConfig reads flags and environment. Flags win over environment.
func ParseConfig(args []string, env LookupEnv, stderr io.Writer) (Config, error) {
	var c Config
	fs := flag.NewFlagSet("harness-core serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&c.Listen, "listen", orDefault(env.get("HARNESS_LISTEN"), ":8080"), "address to listen on")
	fs.StringVar(&c.Data, "data", orDefault(env.get("HARNESS_DATA_DIR"), "/data"), "state directory")
	fs.StringVar(&c.PublicURL, "public-url", env.get("HARNESS_PUBLIC_URL"), "external URL (required for OIDC)")
	fs.StringVar(&c.TLSCert, "tls-cert", env.get("HARNESS_TLS_CERT"), "TLS certificate file")
	fs.StringVar(&c.TLSKey, "tls-key", env.get("HARNESS_TLS_KEY"), "TLS key file")
	fs.StringVar(&c.WebDir, "web-dir", env.get("HARNESS_WEB_DIR"), "serve the UI from this directory")
	fs.BoolVar(&c.TrustProxy, "trust-proxy", envBool(env.get("HARNESS_TRUST_PROXY"), false), "trust X-Forwarded-* headers")
	fs.BoolVar(&c.Local, "local", envBool(env.get("HARNESS_LOCAL"), false), "include the local host")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	if fs.NArg() > 0 {
		return c, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return c, errors.New("--tls-cert and --tls-key must be given together")
	}
	c.LocalLogin = envBool(env.get("HARNESS_LOCAL_LOGIN"), true)
	c.AdminUser = orDefault(env.get("HARNESS_ADMIN_USER"), "admin")
	var err error
	if c.AdminPassword, err = secret(env, "HARNESS_ADMIN_PASSWORD"); err != nil {
		return c, err
	}
	if issuer := env.get("HARNESS_OIDC_ISSUER"); issuer != "" {
		sec, err := secret(env, "HARNESS_OIDC_CLIENT_SECRET")
		if err != nil {
			return c, err
		}
		groups := []string{"admins"}
		if v, set := env("HARNESS_OIDC_ALLOWED_GROUPS"); set {
			groups = splitList(v)
		}
		oc := &OIDCConfig{
			Issuer: issuer, ClientID: env.get("HARNESS_OIDC_CLIENT_ID"), ClientSecret: sec,
			Name: env.get("HARNESS_OIDC_NAME"), AllowedGroups: groups,
			Scopes: strings.Fields(env.get("HARNESS_OIDC_SCOPES")),
		}
		if oc.ClientID == "" {
			return c, errors.New("HARNESS_OIDC_ISSUER is set but HARNESS_OIDC_CLIENT_ID is not")
		}
		if c.PublicURL == "" {
			return c, errors.New("OIDC needs --public-url / HARNESS_PUBLIC_URL (the redirect URI)")
		}
		oc.RedirectURL = c.PublicURL + "/auth/oidc/callback"
		c.OIDC = oc
	}
	return c, nil
}

// PrepareEnv points HOME and XDG_CONFIG_HOME at the data directory unless they
// already live inside it, so config stores and ~/.ssh land there.
func PrepareEnv(data string, getenv func(string) string, setenv func(k, v string) error) error {
	inside := func(p string) bool {
		if p == "" {
			return false
		}
		rel, err := filepath.Rel(data, p)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	if !inside(getenv("HOME")) {
		if err := setenv("HOME", data); err != nil {
			return err
		}
	}
	if !inside(getenv("XDG_CONFIG_HOME")) {
		if err := setenv("XDG_CONFIG_HOME", filepath.Join(data, ".config")); err != nil {
			return err
		}
	}
	return nil
}

// Serve runs `harness-core serve` and returns the process exit code.
func Serve(ctx context.Context, args []string, stderr io.Writer, env LookupEnv, deps Deps) int {
	logger := slog.New(slog.NewTextHandler(stderr, nil))
	cfg, err := ParseConfig(args, env, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		logger.Error("invalid configuration", "err", err)
		return 2
	}
	data, err := filepath.Abs(cfg.Data)
	if err != nil {
		logger.Error("invalid --data", "err", err)
		return 2
	}
	cfg.Data = data
	if err := os.MkdirAll(filepath.Join(data, ".ssh"), 0o700); err != nil {
		logger.Error("can't create data directory", "err", err)
		return 1
	}
	if err := PrepareEnv(data, os.Getenv, os.Setenv); err != nil {
		logger.Error("can't set HOME", "err", err)
		return 1
	}

	users, err := OpenUsers(filepath.Join(data, "users.json"))
	if err != nil {
		logger.Error("users store", "err", err)
		return 1
	}
	if cfg.LocalLogin && cfg.AdminPassword != "" {
		changed, err := users.Bootstrap(cfg.AdminUser, cfg.AdminPassword)
		if err != nil {
			logger.Error("admin bootstrap", "err", err)
			return 2
		}
		if changed {
			logger.Info("local account set from environment", "user", cfg.AdminUser)
		}
	}
	localOK := cfg.LocalLogin && users.Count() > 0
	if !localOK && cfg.OIDC == nil {
		logger.Error("no login method configured: set HARNESS_ADMIN_PASSWORD (or run `harness-core user set <name>`) and/or HARNESS_OIDC_ISSUER")
		return 2
	}
	if cfg.TLSCert == "" && !strings.HasPrefix(strings.ToLower(cfg.PublicURL), "https://") {
		logger.Warn("serving without TLS and public URL is not https: session cookies travel in clear text and browsers block clipboard paste; put a TLS-terminating proxy in front or use --tls-cert/--tls-key")
	}

	host, _ := os.Hostname()
	if host == "" {
		host = "server"
	}
	pub, created, err := EnsureSSHKey(filepath.Join(data, ".ssh"), host)
	if err != nil {
		logger.Error("ssh key", "err", err)
		return 1
	}
	if created {
		logger.Info("generated server SSH key", "public", pub)
	}
	key, err := LoadOrCreateKey(filepath.Join(data, "session.key"))
	if err != nil {
		logger.Error("session key", "err", err)
		return 1
	}

	tb := make([]byte, 32)
	_, _ = rand.Read(tb)
	internal := hex.EncodeToString(tb)
	apiH, closer, err := deps.NewAPI(internal, logger, cfg.Local)
	if err != nil {
		logger.Error("core setup", "err", err)
		return 1
	}
	defer closer.Close()

	opts := Options{
		PublicURL: cfg.PublicURL, TrustProxy: cfg.TrustProxy, LocalLogin: cfg.LocalLogin, WebDir: cfg.WebDir,
		API: apiH, InternalToken: internal, Signer: NewSigner(key), Users: users,
		SSHPublicKey: pub, Logger: logger,
	}
	if cfg.OIDC != nil {
		opts.OIDC = NewOIDC(*cfg.OIDC, nil)
	}
	srv := &http.Server{Handler: NewHandler(opts), ReadHeaderTimeout: 10 * time.Second}

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		logger.Error("listen failed", "err", err)
		return 1
	}
	logger.Info("listening", "addr", ln.Addr().String(), "version", version.Version, "tls", cfg.TLSCert != "")
	serveErr := make(chan error, 1)
	go func() {
		if cfg.TLSCert != "" {
			serveErr <- srv.ServeTLS(ln, cfg.TLSCert, cfg.TLSKey)
		} else {
			serveErr <- srv.Serve(ln)
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutting down", "reason", "signal")
	case err := <-serveErr:
		logger.Error("server failed", "err", err)
		return 1
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		logger.Warn("graceful shutdown incomplete", "err", err)
		_ = srv.Close()
	}
	return 0
}

// UserCmd implements `harness-core user set|rm <name> [--data dir]`.
func UserCmd(args []string, stdin io.Reader, stdout, stderr io.Writer, env LookupEnv) int {
	usage := func() int {
		fmt.Fprintln(stderr, "usage: harness-core user set|rm <name> [--data dir]")
		return 2
	}
	if len(args) < 1 {
		return usage()
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("harness-core user "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	data := fs.String("data", orDefault(env.get("HARNESS_DATA_DIR"), "/data"), "state directory")
	var names []string
	for len(rest) > 0 { // allow the name before or after flags
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 2
		}
		rest = fs.Args()
		if len(rest) > 0 {
			names = append(names, rest[0])
			rest = rest[1:]
		}
	}
	if (sub != "set" && sub != "rm") || len(names) != 1 {
		return usage()
	}
	name := names[0]
	users, err := OpenUsers(filepath.Join(*data, "users.json"))
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if sub == "rm" {
		ok, err := users.Remove(name)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		if !ok {
			fmt.Fprintf(stderr, "no such user: %s\n", name)
			return 1
		}
		fmt.Fprintf(stdout, "removed %s\n", name)
		return 0
	}
	pw, err := readPassword(stdin, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if err := users.Set(name, pw); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	fmt.Fprintf(stdout, "password set for %s\n", name)
	return 0
}

func readPassword(stdin io.Reader, stderr io.Writer) (string, error) {
	if f, ok := stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(stderr, "Password: ")
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(stderr)
		if err != nil {
			return "", err
		}
		fmt.Fprint(stderr, "Again: ")
		b2, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(stderr)
		if err != nil {
			return "", err
		}
		if string(b) != string(b2) {
			return "", errors.New("passwords differ")
		}
		return string(b), nil
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("no password on stdin")
	}
	return strings.TrimRight(line, "\r\n"), nil
}
