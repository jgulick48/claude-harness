package sshconn

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kevinburke/ssh_config"
)

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

func localUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		name := u.Username
		if i := strings.LastIndex(name, `\`); i >= 0 { // Windows DOMAIN\user
			name = name[i+1:]
		}
		return name
	}
	for _, k := range []string{"USER", "USERNAME", "LOGNAME"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func configPath(opts Options) string {
	if opts.ConfigFile != "" {
		return opts.ConfigFile
	}
	if h := homeDir(); h != "" {
		return filepath.Join(h, ".ssh", "config")
	}
	return ""
}

func knownHostsPath(opts Options) string {
	if opts.KnownHostsFile != "" {
		return opts.KnownHostsFile
	}
	return filepath.Join(homeDir(), ".ssh", "known_hosts")
}

// loadConfig parses the ssh config; a missing file yields an empty config.
func loadConfig(opts Options) (*ssh_config.Config, error) {
	p := configPath(opts)
	if p == "" {
		return &ssh_config.Config{}, nil
	}
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &ssh_config.Config{}, nil
		}
		return nil, err
	}
	defer f.Close()
	cfg, err := ssh_config.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("parse ssh config %s: %w", p, err)
	}
	return cfg, nil
}

// splitTarget splits [user@]host[:port] (host may be a bracketed IPv6 literal).
func splitTarget(t string) (user, host string, port int, err error) {
	rest := t
	if i := strings.LastIndex(rest, "@"); i >= 0 {
		user, rest = rest[:i], rest[i+1:]
	}
	rest = strings.TrimPrefix(rest, "ssh://")
	portStr := ""
	switch {
	case strings.HasPrefix(rest, "["):
		j := strings.Index(rest, "]")
		if j < 0 {
			return "", "", 0, fmt.Errorf("invalid ssh target %q", t)
		}
		host = rest[1:j]
		if tail := rest[j+1:]; tail != "" {
			if !strings.HasPrefix(tail, ":") {
				return "", "", 0, fmt.Errorf("invalid ssh target %q", t)
			}
			portStr = tail[1:]
		}
	case strings.Count(rest, ":") == 1:
		i := strings.Index(rest, ":")
		host, portStr = rest[:i], rest[i+1:]
	default:
		host = rest
	}
	if host == "" {
		return "", "", 0, fmt.Errorf("invalid ssh target %q: empty host", t)
	}
	if portStr != "" {
		p, perr := strconv.Atoi(portStr)
		if perr != nil || p < 1 || p > 65535 {
			return "", "", 0, fmt.Errorf("invalid ssh target %q: bad port", t)
		}
		port = p
	}
	return user, host, port, nil
}

// expandTokens handles ~ and the %d %u %h %r %% tokens.
func expandTokens(s, host, remoteUser string) string {
	if s == "~" || strings.HasPrefix(s, "~/") || strings.HasPrefix(s, `~\`) {
		s = homeDir() + s[1:]
	}
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '%' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case '%':
			b.WriteByte('%')
		case 'd':
			b.WriteString(homeDir())
		case 'u':
			b.WriteString(localUser())
		case 'h':
			b.WriteString(host)
		case 'r':
			b.WriteString(remoteUser)
		default:
			b.WriteByte('%')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// Resolve applies ~/.ssh/config to target.
func Resolve(target string, opts Options) (Resolved, error) {
	cfg, err := loadConfig(opts)
	if err != nil {
		return Resolved{}, err
	}
	return resolveWith(cfg, target)
}

func resolveWith(cfg *ssh_config.Config, target string) (Resolved, error) {
	tUser, alias, tPort, err := splitTarget(target)
	if err != nil {
		return Resolved{}, err
	}
	get := func(key string) string {
		v, _ := cfg.Get(alias, key)
		return strings.TrimSpace(v)
	}
	r := Resolved{Target: target}

	r.User = tUser
	if r.User == "" {
		r.User = get("User")
	}
	if r.User == "" {
		r.User = localUser()
	}

	r.HostName = alias
	if hn := get("HostName"); hn != "" {
		r.HostName = expandTokens(hn, alias, r.User)
	}

	r.Port = tPort
	if r.Port == 0 {
		if ps := get("Port"); ps != "" {
			p, perr := strconv.Atoi(ps)
			if perr != nil || p < 1 || p > 65535 {
				return Resolved{}, fmt.Errorf("ssh config: bad Port %q for %s", ps, alias)
			}
			r.Port = p
		}
	}
	if r.Port == 0 {
		r.Port = 22
	}

	ids, _ := cfg.GetAll(alias, "IdentityFile")
	for _, id := range ids {
		id = strings.Trim(strings.TrimSpace(id), `"`)
		if id != "" && !strings.EqualFold(id, "none") {
			// IdentityFile is a local path: normalize separators for this OS.
			r.IdentityFiles = append(r.IdentityFiles, filepath.Clean(expandTokens(id, r.HostName, r.User)))
		}
	}

	if pj := get("ProxyJump"); pj != "" && !strings.EqualFold(pj, "none") {
		for _, h := range strings.Split(pj, ",") {
			if h = strings.TrimSpace(h); h != "" {
				r.ProxyJump = append(r.ProxyJump, strings.TrimPrefix(h, "ssh://"))
			}
		}
	}
	return r, nil
}
