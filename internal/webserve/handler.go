package webserve

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/jgulick48/claude-harness/internal/version"
)

//go:embed all:dist
var embeddedDist embed.FS

const csp = "default-src 'self'; connect-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'"

// Options configures NewHandler.
type Options struct {
	PublicURL  string // external URL, used for the Origin check and Secure cookies
	TrustProxy bool
	LocalLogin bool
	WebDir     string // serve the UI from here instead of the embedded copy
	Static     fs.FS  // overrides both (tests)

	API           http.Handler // the unchanged api.NewServer handler
	InternalToken string
	Signer        *Signer
	Users         *Users
	OIDC          *OIDC // nil when OIDC is off
	SSHPublicKey  string
	Logger        *slog.Logger
	Limiter       *RateLimiter // default: 5 failures / 15 minutes
}

type handler struct {
	Options
	pubOrigin string // scheme://host of PublicURL, or ""
	static    fs.FS
	hasIndex  bool
	now       func() time.Time
}

// NewHandler returns the public-facing handler: authentication, login pages,
// /api/me, the /ws proxy to the api handler, and the static UI.
func NewHandler(o Options) http.Handler {
	h := &handler{Options: o, now: time.Now}
	if h.Logger == nil {
		h.Logger = slog.New(slog.DiscardHandler)
	}
	if h.Limiter == nil {
		h.Limiter = NewRateLimiter(5, 15*time.Minute)
	}
	if u, err := url.Parse(o.PublicURL); err == nil && u.Scheme != "" && u.Host != "" {
		h.pubOrigin = strings.ToLower(u.Scheme + "://" + u.Host)
	}
	switch {
	case o.Static != nil:
		h.static = o.Static
	case o.WebDir != "":
		h.static = os.DirFS(o.WebDir)
	default:
		sub, err := fs.Sub(embeddedDist, "dist/web")
		if err == nil {
			h.static = sub
		}
	}
	if h.static != nil {
		if st, err := fs.Stat(h.static, "index.html"); err == nil && !st.IsDir() {
			h.hasIndex = true
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /login", h.loginPage)
	mux.HandleFunc("POST /login", h.loginSubmit)
	mux.HandleFunc("POST /logout", h.logout)
	mux.HandleFunc("GET /auth/oidc/start", h.oidcStart)
	mux.HandleFunc("GET /auth/oidc/callback", h.oidcCallback)
	mux.HandleFunc("GET /api/me", h.me)
	mux.HandleFunc("GET /ws", h.ws)
	// The api handler's own /healthz is token-only and internal; never public.
	mux.HandleFunc("/healthz", http.NotFound)
	mux.HandleFunc("/", h.staticUI)
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Security-Policy", csp)
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// ---- request helpers ----

func (h *handler) isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if h.TrustProxy && strings.EqualFold(firstToken(r.Header.Get("X-Forwarded-Proto")), "https") {
		return true
	}
	return false
}

func firstToken(s string) string {
	t, _, _ := strings.Cut(s, ",")
	return strings.TrimSpace(t)
}

// clientIP is the rate-limit key. Behind a trusted proxy it is the right-most
// X-Forwarded-For entry (the one the proxy itself appended).
func (h *handler) clientIP(r *http.Request) string {
	if h.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// originOK implements the CSRF check for POSTs and the /ws upgrade: Origin
// must equal the public URL's origin, or the request's own host when no public
// URL is configured. A missing Origin is rejected.
func (h *handler) originOK(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" || o == "null" {
		return false
	}
	ou, err := url.Parse(o)
	if err != nil || ou.Scheme == "" || ou.Host == "" {
		return false
	}
	if h.pubOrigin != "" {
		return strings.ToLower(ou.Scheme+"://"+ou.Host) == h.pubOrigin
	}
	return strings.EqualFold(ou.Host, r.Host)
}

func (h *handler) session(r *http.Request) (Claims, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return Claims{}, false
	}
	cl, ok := h.Signer.VerifySession(c.Value)
	if !ok {
		return Claims{}, false
	}
	if cl.Method == "local" {
		// A local session ends when local login is turned off, the account
		// is removed, or its password changes.
		if !h.LocalLogin || h.Users == nil {
			return Claims{}, false
		}
		gen, exists := h.Users.Gen(cl.User)
		if !exists || subtle.ConstantTimeCompare([]byte(gen), []byte(cl.Gen)) != 1 {
			return Claims{}, false
		}
	}
	return cl, true
}

func (h *handler) secureCookie(r *http.Request) bool {
	return h.isHTTPS(r) || strings.HasPrefix(h.pubOrigin, "https://")
}

func (h *handler) startSession(w http.ResponseWriter, r *http.Request, user, method, gen string) error {
	v, err := h.Signer.SignSession(user, method, gen)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: v, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: h.secureCookie(r), MaxAge: int(sessionTTL / time.Second),
	})
	return nil
}

// sanitizeNext returns next if it is a same-origin path, else "/".
func sanitizeNext(next string) string {
	if next == "" || next[0] != '/' || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	for _, c := range next {
		if c < 0x20 || c == 0x7f || c == '\\' {
			return "/"
		}
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return "/"
	}
	return next
}

func noStore(w http.ResponseWriter) { w.Header().Set("Cache-Control", "no-store") }

// ---- endpoints ----

func (h *handler) me(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	w.Header().Set("Content-Type", "application/json")
	c, ok := h.session(r)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{
		"user": c.User, "method": c.Method, "sshPublicKey": h.SSHPublicKey, "version": version.Version,
	})
}

func (h *handler) ws(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.session(r); !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !h.originOK(r) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	r2 := r.Clone(r.Context())
	r2.URL.RawQuery = url.Values{"token": {h.InternalToken}}.Encode()
	h.API.ServeHTTP(w, r2)
}

func (h *handler) logout(w http.ResponseWriter, r *http.Request) {
	if !h.originOK(r) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: h.secureCookie(r), MaxAge: -1,
	})
	noStore(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *handler) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if !h.LocalLogin {
		http.NotFound(w, r)
		return
	}
	if !h.originOK(r) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	next := sanitizeNext(r.PostForm.Get("next"))
	ip := h.clientIP(r)
	if !h.Limiter.Allowed(ip) {
		h.renderLogin(w, r, http.StatusTooManyRequests, "ratelimit", next)
		return
	}
	user, pass := r.PostForm.Get("username"), r.PostForm.Get("password")
	if !h.Users.Verify(user, pass) {
		h.Limiter.Fail(ip)
		h.Logger.Warn("login failed", "ip", ip)
		noStore(w)
		http.Redirect(w, r, loginURL("invalid", next), http.StatusSeeOther)
		return
	}
	h.Limiter.Reset(ip)
	gen, _ := h.Users.Gen(user)
	if err := h.startSession(w, r, user, "local", gen); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	noStore(w)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func loginURL(errCode, next string) string {
	q := url.Values{}
	if errCode != "" {
		q.Set("error", errCode)
	}
	if next != "" && next != "/" {
		q.Set("next", next)
	}
	if len(q) == 0 {
		return "/login"
	}
	return "/login?" + q.Encode()
}

// ---- login page ----

var errorMessages = map[string]string{
	"invalid":   "Invalid username or password.",
	"ratelimit": "Too many failed attempts. Try again later.",
	"forbidden": "Your account is not allowed to use this server.",
	"oidc":      "Single sign-on failed. Try again.",
}

type loginData struct {
	Error     string
	Next      string
	Local     bool
	OIDC      bool
	OIDCName  string
	OIDCLink  string
	Forbidden bool
}

var loginTmpl = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign in - Claude Harness</title>
<style>
:root{color-scheme:light dark;--bg:#f5f5f7;--fg:#1d1d1f;--card:#fff;--bd:#d2d2d7;--ac:#c15f3c;--er:#b00020}
@media (prefers-color-scheme:dark){:root{--bg:#161618;--fg:#ececf0;--card:#202024;--bd:#3a3a40;--ac:#d97757;--er:#ff6b6b}}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:var(--bg);color:var(--fg);font:15px/1.4 system-ui,sans-serif}
main{width:min(360px,90vw);background:var(--card);border:1px solid var(--bd);border-radius:10px;padding:28px}
h1{font-size:20px;margin:0 0 18px}
label{display:block;font-size:13px;margin:12px 0 4px}
input{box-sizing:border-box;width:100%;padding:9px 10px;border:1px solid var(--bd);border-radius:6px;background:transparent;color:inherit;font:inherit}
button,a.btn{display:block;box-sizing:border-box;width:100%;margin-top:16px;padding:10px;border:0;border-radius:6px;background:var(--ac);color:#fff;font:inherit;font-weight:600;text-align:center;text-decoration:none;cursor:pointer}
a.btn.alt{background:transparent;color:var(--fg);border:1px solid var(--bd)}
.err{color:var(--er);margin:0 0 8px;font-size:14px}
hr{border:0;border-top:1px solid var(--bd);margin:20px 0 4px}
</style></head><body><main>
<h1>Claude Harness</h1>
{{if .Error}}<p class="err" role="alert">{{.Error}}</p>{{end}}
{{if .Forbidden}}{{else}}
{{if .Local}}<form method="post" action="/login">
<input type="hidden" name="next" value="{{.Next}}">
<label for="u">Username</label><input id="u" name="username" autocomplete="username" autofocus required>
<label for="p">Password</label><input id="p" name="password" type="password" autocomplete="current-password" required>
<button type="submit">Sign in</button>
</form>{{end}}
{{if and .Local .OIDC}}<hr>{{end}}
{{if .OIDC}}<a class="btn{{if .Local}} alt{{end}}" href="{{.OIDCLink}}">Sign in with {{.OIDCName}}</a>{{end}}
{{end}}
</main></body></html>
`))

func (h *handler) renderLogin(w http.ResponseWriter, r *http.Request, status int, errCode, next string) {
	d := loginData{
		Error: errorMessages[errCode], Next: next, Local: h.LocalLogin,
		OIDC: h.OIDC != nil, Forbidden: errCode == "forbidden",
	}
	if h.OIDC != nil {
		d.OIDCName = h.OIDC.cfg.Name
		d.OIDCLink = "/auth/oidc/start"
	}
	noStore(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = loginTmpl.Execute(w, d)
}

func (h *handler) loginPage(w http.ResponseWriter, r *http.Request) {
	next := sanitizeNext(r.URL.Query().Get("next"))
	if _, ok := h.session(r); ok {
		noStore(w)
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}
	h.renderLogin(w, r, http.StatusOK, r.URL.Query().Get("error"), next)
}

// ---- static UI ----

const notBuiltPage = `<!doctype html><meta charset="utf-8"><title>Claude Harness</title>
<body style="font:15px system-ui,sans-serif;max-width:40em;margin:3em auto;padding:0 1em">
<h1>Web UI not built</h1><p>This binary has no embedded web UI. Run <code>make web</code> and rebuild, or pass <code>--web-dir</code>.</p></body>`

func (h *handler) staticUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, ok := h.session(r); !ok {
		noStore(w)
		http.Redirect(w, r, loginURL("", sanitizeNext(r.URL.RequestURI())), http.StatusSeeOther)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	if !h.hasIndex {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(notBuiltPage))
		return
	}
	p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if p == "" {
		p = "index.html"
	}
	if st, err := fs.Stat(h.static, p); err != nil || st.IsDir() {
		if path.Ext(p) != "" {
			http.NotFound(w, r)
			return
		}
		p = "index.html" // SPA fallback
	}
	f, err := h.static.Open(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	rs, ok := f.(readSeeker)
	if err != nil || !ok {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if p == "index.html" {
		w.Header().Set("Cache-Control", "no-cache")
	} else if strings.HasPrefix(p, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeContent(w, r, p, st.ModTime(), rs)
}

type readSeeker interface {
	Read([]byte) (int, error)
	Seek(int64, int) (int64, error)
}
