package webserve

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"golang.org/x/crypto/ssh"
)

func init() { bcryptCost = 4 }

// ---- cookies ----

func TestSessionSignVerify(t *testing.T) {
	s := NewSigner([]byte("0123456789abcdef0123456789abcdef"))
	tok, err := s.SignSession("alice", "local", "g1")
	if err != nil {
		t.Fatal(err)
	}
	c, ok := s.VerifySession(tok)
	if !ok || c.User != "alice" || c.Method != "local" {
		t.Fatalf("verify = %+v %v", c, ok)
	}
	// Tampered payload and tampered MAC.
	p, m, _ := strings.Cut(tok, ".")
	forged := base64.RawURLEncoding.EncodeToString([]byte(`{"u":"root","m":"local","e":99999999999}`))
	for _, bad := range []string{forged + "." + m, p + "." + m[:len(m)-2] + "AA", p, "", "a.b.c", p + "."} {
		if _, ok := s.VerifySession(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
	// Different key.
	if _, ok := NewSigner([]byte("another key another key another!")).VerifySession(tok); ok {
		t.Error("accepted token signed with another key")
	}
	// Purpose separation.
	flow, _ := s.sealFlow(flowState{State: "x", Exp: time.Now().Add(time.Hour).Unix()})
	if _, ok := s.VerifySession(flow); ok {
		t.Error("flow token accepted as session")
	}
	// Expiry.
	s.now = func() time.Time { return time.Now().Add(sessionTTL + time.Minute) }
	if _, ok := s.VerifySession(tok); ok {
		t.Error("expired token accepted")
	}
}

func TestSessionKeyPersistsAndPerms(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "session.key")
	k1, err := LoadOrCreateKey(p)
	if err != nil || len(k1) != 32 {
		t.Fatal(err, len(k1))
	}
	k2, err := LoadOrCreateKey(p)
	if err != nil || !bytes.Equal(k1, k2) {
		t.Fatal("key not persisted")
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
}

// ---- users ----

func TestUsersStore(t *testing.T) {
	p := filepath.Join(t.TempDir(), "users.json")
	u, err := OpenUsers(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := u.Set("alice", "pw1"); err != nil {
		t.Fatal(err)
	}
	if !u.Verify("alice", "pw1") || u.Verify("alice", "nope") || u.Verify("bob", "pw1") || u.Verify("alice", "") {
		t.Error("verify results wrong")
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
	raw, _ := os.ReadFile(p)
	if strings.Contains(string(raw), "pw1") || !strings.Contains(string(raw), "$2a$") {
		t.Errorf("users.json = %s", raw)
	}
	if err := u.Set("x", strings.Repeat("a", 73)); err == nil {
		t.Error("accepted overlong password")
	}
	// Reload.
	u2, _ := OpenUsers(p)
	if !u2.Verify("alice", "pw1") || u2.Count() != 1 {
		t.Error("reload failed")
	}
	if ok, _ := u2.Remove("alice"); !ok || u2.Count() != 0 {
		t.Error("remove failed")
	}
	if ok, _ := u2.Remove("alice"); ok {
		t.Error("removed twice")
	}
}

func TestUsersBootstrap(t *testing.T) {
	u, _ := OpenUsers(filepath.Join(t.TempDir(), "users.json"))
	if ch, err := u.Bootstrap("admin", "secret"); err != nil || !ch {
		t.Fatal(ch, err)
	}
	if ch, _ := u.Bootstrap("admin", "secret"); ch {
		t.Error("rewrote identical password")
	}
	if ch, _ := u.Bootstrap("admin", "changed"); !ch || !u.Verify("admin", "changed") || u.Verify("admin", "secret") {
		t.Error("password not reset")
	}
}

func TestUserCmd(t *testing.T) {
	dir := t.TempDir()
	env := LookupEnv(func(string) (string, bool) { return "", false })
	var out, errb bytes.Buffer
	if rc := UserCmd([]string{"set", "bob", "--data", dir}, strings.NewReader("hunter2\n"), &out, &errb, env); rc != 0 {
		t.Fatalf("rc=%d %s", rc, errb.String())
	}
	u, _ := OpenUsers(filepath.Join(dir, "users.json"))
	if !u.Verify("bob", "hunter2") {
		t.Error("password not stored")
	}
	if rc := UserCmd([]string{"rm", "--data", dir, "bob"}, nil, &out, &errb, env); rc != 0 {
		t.Fatalf("rm rc=%d", rc)
	}
	if rc := UserCmd([]string{"rm", "bob", "--data", dir}, nil, &out, &errb, env); rc != 1 {
		t.Errorf("rm missing rc=%d", rc)
	}
	if rc := UserCmd([]string{"bogus"}, nil, &out, &errb, env); rc != 2 {
		t.Errorf("usage rc=%d", rc)
	}
}

// ---- rate limiter ----

func TestRateLimiter(t *testing.T) {
	now := time.Now()
	r := NewRateLimiter(5, 15*time.Minute)
	r.now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		if !r.Allowed("a") {
			t.Fatalf("blocked after %d failures", i)
		}
		r.Fail("a")
	}
	if r.Allowed("a") {
		t.Error("not blocked after 5 failures")
	}
	if !r.Allowed("b") {
		t.Error("other key blocked")
	}
	now = now.Add(16 * time.Minute)
	if !r.Allowed("a") {
		t.Error("still blocked after window")
	}
	r.Fail("a")
	r.Reset("a")
	if len(r.fails) != 0 {
		t.Error("reset left entries")
	}
}

// ---- next / origin ----

func TestSanitizeNext(t *testing.T) {
	for in, want := range map[string]string{
		"": "/", "/": "/", "/foo?x=1": "/foo?x=1", "//evil.com": "/", "/\\evil.com": "/",
		"https://evil.com": "/", "evil": "/", "/a\\b": "/", "/a\nb": "/", "/%2F%2Fx": "/%2F%2Fx",
	} {
		if got := sanitizeNext(in); got != want {
			t.Errorf("sanitizeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOriginOK(t *testing.T) {
	mk := func(origin, host string) *http.Request {
		r := httptest.NewRequest("POST", "/login", nil)
		r.Host = host
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}
	h := &handler{Options: Options{PublicURL: "https://harness.example.com"}, pubOrigin: "https://harness.example.com"}
	for _, c := range []struct {
		o    string
		want bool
	}{
		{"https://harness.example.com", true}, {"https://HARNESS.example.com", true},
		{"http://harness.example.com", false}, {"https://evil.com", false}, {"", false}, {"null", false},
		{"https://harness.example.com.evil.com", false},
	} {
		if got := h.originOK(mk(c.o, "harness.example.com")); got != c.want {
			t.Errorf("public: %q = %v", c.o, got)
		}
	}
	h2 := &handler{}
	if !h2.originOK(mk("http://box:8080", "box:8080")) || h2.originOK(mk("http://evil", "box:8080")) || h2.originOK(mk("", "box:8080")) {
		t.Error("own-host origin check wrong")
	}
}

// ---- handler ----

type fixture struct {
	h       http.Handler
	signer  *Signer
	gotTok  chan string
	users   *Users
	limiter *RateLimiter
}

func newFixture(t *testing.T, mod func(*Options)) *fixture {
	t.Helper()
	f := &fixture{gotTok: make(chan string, 4)}
	inner := http.NewServeMux()
	inner.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		f.gotTok <- r.URL.Query().Get("token")
		_, _ = io.WriteString(w, "inner-ws")
	})
	inner.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "inner-healthz") })
	f.signer = NewSigner([]byte("0123456789abcdef0123456789abcdef"))
	f.users, _ = OpenUsers(filepath.Join(t.TempDir(), "users.json"))
	_ = f.users.Set("alice", "wonderland")
	f.limiter = NewRateLimiter(5, 15*time.Minute)
	o := Options{
		LocalLogin: true, API: inner, InternalToken: "internal-secret", Signer: f.signer, Users: f.users,
		SSHPublicKey: "ssh-ed25519 AAAA test@box", Limiter: f.limiter,
		Static: fstest.MapFS{
			"index.html":    {Data: []byte("<html>INDEX</html>")},
			"assets/app.js": {Data: []byte("console.log(1)")},
		},
	}
	if mod != nil {
		mod(&o)
	}
	f.h = NewHandler(o)
	return f
}

func (f *fixture) do(method, target string, mod func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	r.RemoteAddr = "192.0.2.1:1234"
	if mod != nil {
		mod(r)
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}

func (f *fixture) cookie(t *testing.T) *http.Cookie {
	gen, _ := f.users.Gen("alice")
	tok, err := f.signer.SignSession("alice", "local", gen)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: sessionCookie, Value: tok}
}

func TestUnauthenticatedRedirectsToLogin(t *testing.T) {
	f := newFixture(t, nil)
	w := f.do("GET", "/some/page?x=1", nil)
	if w.Code != 303 || w.Header().Get("Location") != "/login?next=%2Fsome%2Fpage%3Fx%3D1" {
		t.Fatalf("%d %q", w.Code, w.Header().Get("Location"))
	}
	if w := f.do("GET", "/", nil); w.Code != 303 || w.Header().Get("Location") != "/login" {
		t.Errorf("root: %d %q", w.Code, w.Header().Get("Location"))
	}
}

func TestLivezPublicHealthzHidden(t *testing.T) {
	f := newFixture(t, nil)
	if w := f.do("GET", "/livez", nil); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "ok" {
		t.Errorf("livez %d %q", w.Code, w.Body.String())
	}
	for _, c := range []*http.Cookie{nil, f.cookie(t)} {
		w := f.do("GET", "/healthz", func(r *http.Request) {
			if c != nil {
				r.AddCookie(c)
			}
		})
		if w.Code != 404 || strings.Contains(w.Body.String(), "inner") {
			t.Errorf("healthz exposed: %d %q", w.Code, w.Body.String())
		}
	}
}

func TestAPIMe(t *testing.T) {
	f := newFixture(t, nil)
	w := f.do("GET", "/api/me", nil)
	if w.Code != 401 || !strings.Contains(w.Body.String(), "unauthorized") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	w = f.do("GET", "/api/me", func(r *http.Request) { r.AddCookie(f.cookie(t)) })
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	var me map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if me["user"] != "alice" || me["method"] != "local" || me["sshPublicKey"] != "ssh-ed25519 AAAA test@box" || me["version"] == "" {
		t.Errorf("me = %v", me)
	}
	// A forged cookie is 401.
	w = f.do("GET", "/api/me", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "x.y"}) })
	if w.Code != 401 {
		t.Error("forged cookie accepted")
	}
}

func post(form url.Values, origin string) func(*http.Request) {
	return func(r *http.Request) {
		r.Body = io.NopCloser(strings.NewReader(form.Encode()))
		r.ContentLength = int64(len(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Host = "box:8080"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
	}
}

func TestLoginFlow(t *testing.T) {
	f := newFixture(t, nil)
	ok := url.Values{"username": {"alice"}, "password": {"wonderland"}, "next": {"/deep?a=b"}}
	w := f.do("POST", "/login", post(ok, "http://box:8080"))
	if w.Code != 303 || w.Header().Get("Location") != "/deep?a=b" {
		t.Fatalf("%d %q", w.Code, w.Header().Get("Location"))
	}
	var sc *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			sc = c
		}
	}
	if sc == nil || !sc.HttpOnly || sc.SameSite != http.SameSiteLaxMode || sc.Path != "/" || sc.Secure {
		t.Fatalf("cookie = %+v", sc)
	}
	if c, ok := f.signer.VerifySession(sc.Value); !ok || c.User != "alice" || c.Method != "local" {
		t.Error("cookie doesn't verify")
	}
	// Secure over a trusted proxy.
	fp := newFixture(t, func(o *Options) { o.TrustProxy = true })
	w = fp.do("POST", "/login", func(r *http.Request) {
		post(ok, "http://box:8080")(r)
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("Origin", "https://box:8080")
	})
	if cs := w.Result().Cookies(); len(cs) != 1 || !cs[0].Secure {
		t.Errorf("proxy https cookie not Secure: %v", cs)
	}

	// Open redirect attempt.
	evil := url.Values{"username": {"alice"}, "password": {"wonderland"}, "next": {"//evil.com"}}
	if w := f.do("POST", "/login", post(evil, "http://box:8080")); w.Header().Get("Location") != "/" {
		t.Errorf("open redirect: %q", w.Header().Get("Location"))
	}
	// Bad password.
	bad := url.Values{"username": {"alice"}, "password": {"nope"}}
	w = f.do("POST", "/login", post(bad, "http://box:8080"))
	if w.Code != 303 || w.Header().Get("Location") != "/login?error=invalid" || len(w.Result().Cookies()) != 0 {
		t.Errorf("bad password: %d %q", w.Code, w.Header().Get("Location"))
	}
	// Wrong / missing origin.
	for _, o := range []string{"http://evil.com", ""} {
		if w := f.do("POST", "/login", post(ok, o)); w.Code != 403 {
			t.Errorf("origin %q: %d", o, w.Code)
		}
	}
}

func TestLoginRateLimit(t *testing.T) {
	f := newFixture(t, nil)
	bad := url.Values{"username": {"alice"}, "password": {"nope"}}
	for i := 0; i < 5; i++ {
		if w := f.do("POST", "/login", post(bad, "http://box:8080")); w.Code != 303 {
			t.Fatalf("attempt %d: %d", i, w.Code)
		}
	}
	good := url.Values{"username": {"alice"}, "password": {"wonderland"}}
	if w := f.do("POST", "/login", post(good, "http://box:8080")); w.Code != 429 {
		t.Errorf("not rate limited: %d", w.Code)
	}
}

func TestLoginPageRenderingAndEscaping(t *testing.T) {
	f := newFixture(t, nil)
	w := f.do("GET", `/login?error=%3Cscript%3E&next=%2F%22%3E%3Cscript%3Ealert(1)%3C%2Fscript%3E`, nil)
	body := w.Body.String()
	if w.Code != 200 || strings.Contains(body, "<script") || strings.Contains(body, "alert(1)</") {
		t.Fatalf("unsafe page: %s", body)
	}
	if !strings.Contains(body, `name="password"`) || strings.Contains(body, "Sign in with") {
		t.Error("expected local form only")
	}
	if got := w.Header().Get("Content-Security-Policy"); got != csp {
		t.Errorf("csp = %q", got)
	}
	// Known error code.
	if w := f.do("GET", "/login?error=invalid", nil); !strings.Contains(w.Body.String(), "Invalid username or password") {
		t.Error("error not shown")
	}
	// OIDC-only.
	fo := newFixture(t, func(o *Options) {
		o.LocalLogin = false
		o.OIDC = NewOIDC(OIDCConfig{Issuer: "http://x", Name: "Authelia"}, nil)
	})
	body = fo.do("GET", "/login", nil).Body.String()
	if strings.Contains(body, `name="password"`) || !strings.Contains(body, "Sign in with Authelia") {
		t.Errorf("oidc-only page wrong: %s", body)
	}
	if w := fo.do("POST", "/login", post(url.Values{}, "http://box:8080")); w.Code != 404 {
		t.Errorf("local login not disabled: %d", w.Code)
	}
}

func TestLogout(t *testing.T) {
	f := newFixture(t, nil)
	mod := func(origin string) func(*http.Request) {
		return func(r *http.Request) { r.Host = "box"; r.Header.Set("Origin", origin); r.AddCookie(f.cookie(t)) }
	}
	if w := f.do("POST", "/logout", mod("http://evil")); w.Code != 403 {
		t.Errorf("csrf logout: %d", w.Code)
	}
	w := f.do("POST", "/logout", mod("http://box"))
	cs := w.Result().Cookies()
	if w.Code != 303 || w.Header().Get("Location") != "/login" || len(cs) != 1 || cs[0].MaxAge >= 0 {
		t.Errorf("logout: %d %v", w.Code, cs)
	}
}

func TestWSForwarding(t *testing.T) {
	f := newFixture(t, nil)
	ws := func(c *http.Cookie, origin string) *httptest.ResponseRecorder {
		return f.do("GET", "/ws?token=attacker", func(r *http.Request) {
			r.Host = "box:8080"
			if c != nil {
				r.AddCookie(c)
			}
			if origin != "" {
				r.Header.Set("Origin", origin)
			}
		})
	}
	if w := ws(nil, "http://box:8080"); w.Code != 401 {
		t.Errorf("no cookie: %d", w.Code)
	}
	if w := ws(f.cookie(t), "http://evil.com"); w.Code != 403 {
		t.Errorf("bad origin: %d", w.Code)
	}
	if w := ws(f.cookie(t), ""); w.Code != 403 {
		t.Errorf("no origin: %d", w.Code)
	}
	select {
	case tok := <-f.gotTok:
		t.Fatalf("inner handler reached unauthenticated, token %q", tok)
	default:
	}
	w := ws(f.cookie(t), "http://box:8080")
	if w.Code != 200 || w.Body.String() != "inner-ws" {
		t.Fatalf("%d %q", w.Code, w.Body)
	}
	if tok := <-f.gotTok; tok != "internal-secret" {
		t.Errorf("inner saw token %q", tok)
	}
}

func TestStatic(t *testing.T) {
	f := newFixture(t, nil)
	get := func(p string) *httptest.ResponseRecorder {
		return f.do("GET", p, func(r *http.Request) { r.AddCookie(f.cookie(t)) })
	}
	if w := get("/"); w.Code != 200 || !strings.Contains(w.Body.String(), "INDEX") {
		t.Errorf("/: %d", w.Code)
	}
	if w := get("/assets/app.js"); w.Code != 200 || !strings.Contains(w.Body.String(), "console.log") {
		t.Errorf("asset: %d", w.Code)
	}
	if w := get("/some/spa/route"); w.Code != 200 || !strings.Contains(w.Body.String(), "INDEX") {
		t.Errorf("fallback: %d", w.Code)
	}
	if w := get("/assets/missing.js"); w.Code != 404 {
		t.Errorf("missing asset: %d", w.Code)
	}
	if w := get("/api/other"); w.Code != 404 {
		t.Errorf("unknown api: %d", w.Code)
	}
	w := get("/")
	for _, h := range []string{"Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy"} {
		if w.Header().Get(h) == "" {
			t.Errorf("missing %s", h)
		}
	}
	// Not built.
	fn := newFixture(t, func(o *Options) { o.Static = fstest.MapFS{} })
	w = fn.do("GET", "/", func(r *http.Request) { r.AddCookie(fn.cookie(t)) })
	if w.Code != 503 || !strings.Contains(w.Body.String(), "make web") {
		t.Errorf("not built page: %d %s", w.Code, w.Body)
	}
}

// ---- ssh key ----

func TestEnsureSSHKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".ssh")
	pub, created, err := EnsureSSHKey(dir, "box")
	if err != nil || !created {
		t.Fatal(created, err)
	}
	pk, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(pub))
	if err != nil || pk.Type() != "ssh-ed25519" || comment != "claude-harness@box" {
		t.Fatalf("pub %q: %v %q", pub, err, comment)
	}
	priv := filepath.Join(dir, "id_ed25519")
	if st, _ := os.Stat(priv); st.Mode().Perm() != 0o600 {
		t.Errorf("private mode %v", st.Mode())
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", st.Mode())
	}
	b, _ := os.ReadFile(priv)
	signer, err := ssh.ParsePrivateKey(b)
	if err != nil || !bytes.Equal(signer.PublicKey().Marshal(), pk.Marshal()) {
		t.Fatalf("private key doesn't match: %v", err)
	}
	// Doesn't overwrite.
	pub2, created, err := EnsureSSHKey(dir, "other")
	b2, _ := os.ReadFile(priv)
	if err != nil || created || pub2 != pub || !bytes.Equal(b, b2) {
		t.Errorf("second call changed things: %v %v", created, err)
	}
	// Missing .pub is re-derived, private key untouched.
	_ = os.Remove(priv + ".pub")
	pub3, created, err := EnsureSSHKey(dir, "box")
	b3, _ := os.ReadFile(priv)
	if err != nil || created || !strings.HasPrefix(pub3, strings.Fields(pub)[0]+" "+strings.Fields(pub)[1]) || !bytes.Equal(b, b3) {
		t.Errorf("rederive: %q %v %v", pub3, created, err)
	}
}

// ---- config ----

func TestParseConfigAndPrepareEnv(t *testing.T) {
	env := map[string]string{
		"HARNESS_LISTEN": ":9999", "HARNESS_DATA_DIR": "/srv/h", "HARNESS_PUBLIC_URL": "https://h.example.com/",
		"HARNESS_TRUST_PROXY": "1", "HARNESS_OIDC_ISSUER": "https://auth", "HARNESS_OIDC_CLIENT_ID": "cid",
		"HARNESS_OIDC_ALLOWED_GROUPS": "",
	}
	look := LookupEnv(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	c, err := ParseConfig(nil, look, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":9999" || c.Data != "/srv/h" || c.PublicURL != "https://h.example.com" || !c.TrustProxy || c.Local ||
		c.OIDC == nil || len(c.OIDC.AllowedGroups) != 0 || c.OIDC.RedirectURL != "https://h.example.com/auth/oidc/callback" {
		t.Errorf("config = %+v oidc=%+v", c, c.OIDC)
	}
	c, _ = ParseConfig([]string{"--listen", ":1", "--local", "--data", "/x"}, look, io.Discard)
	if c.Listen != ":1" || !c.Local || c.Data != "/x" {
		t.Errorf("flags: %+v", c)
	}
	delete(env, "HARNESS_OIDC_ALLOWED_GROUPS")
	if c, _ := ParseConfig(nil, look, io.Discard); len(c.OIDC.AllowedGroups) != 1 || c.OIDC.AllowedGroups[0] != "admins" {
		t.Error("default group not admins")
	}
	delete(env, "HARNESS_PUBLIC_URL")
	if _, err := ParseConfig(nil, look, io.Discard); err == nil {
		t.Error("OIDC without public URL accepted")
	}

	set := map[string]string{"HOME": "/root", "XDG_CONFIG_HOME": "/data/.config"}
	err = PrepareEnv("/data", func(k string) string { return set[k] }, func(k, v string) error { set[k] = v; return nil })
	if err != nil || set["HOME"] != "/data" || set["XDG_CONFIG_HOME"] != "/data/.config" {
		t.Errorf("env = %v %v", set, err)
	}
}

func TestServeRefusesWithoutLogin(t *testing.T) {
	look := LookupEnv(func(k string) (string, bool) {
		if k == "HARNESS_DATA_DIR" {
			return t.TempDir(), true
		}
		return "", false
	})
	home := os.Getenv("HOME")
	xdg := os.Getenv("XDG_CONFIG_HOME")
	t.Cleanup(func() { os.Setenv("HOME", home); os.Setenv("XDG_CONFIG_HOME", xdg) })
	var errb bytes.Buffer
	if rc := Serve(context.Background(), nil, &errb, look, Deps{}); rc != 2 || !strings.Contains(errb.String(), "no login method") {
		t.Errorf("rc=%d %s", rc, errb.String())
	}
}

// ---- OIDC against an in-process issuer ----

type fakeIDP struct {
	srv                                  *httptest.Server
	key                                  *rsa.PrivateKey
	claims                               map[string]any // extra id_token claims
	userinf                              map[string]any
	nonce                                string // captured from the authorize request
	gotVerifier, gotChallenge, gotMethod string
}

func newFakeIDP(t *testing.T) *fakeIDP {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeIDP{key: k}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": p.srv.URL, "authorization_endpoint": p.srv.URL + "/authorize", "token_endpoint": p.srv.URL + "/token",
			"jwks_uri": p.srv.URL + "/jwks", "userinfo_endpoint": p.srv.URL + "/userinfo",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "k1",
			"n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		p.gotVerifier = r.PostForm.Get("code_verifier")
		user, pass, _ := r.BasicAuth()
		if r.PostForm.Get("code") != "good-code" || (user != "cid" && r.PostForm.Get("client_id") != "cid") || (pass != "csecret" && r.PostForm.Get("client_secret") != "csecret") {
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		cl := map[string]any{
			"iss": p.srv.URL, "aud": "cid", "sub": "sub-1", "nonce": p.nonce,
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
		}
		for k, v := range p.claims {
			cl[k] = v
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": p.sign(cl)})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(p.userinf) })
	p.srv = httptest.NewServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func (p *fakeIDP) sign(claims map[string]any) string {
	enc := base64.RawURLEncoding
	hb, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "k1"})
	cb, _ := json.Marshal(claims)
	in := enc.EncodeToString(hb) + "." + enc.EncodeToString(cb)
	sum := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, sum[:])
	if err != nil {
		panic(err)
	}
	return in + "." + enc.EncodeToString(sig)
}

// login runs start then callback and returns the callback response.
func (p *fakeIDP) login(t *testing.T, f *fixture, code string) *httptest.ResponseRecorder {
	t.Helper()
	w := f.do("GET", "/auth/oidc/start", nil)
	if w.Code != 303 {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	q := loc.Query()
	if !strings.HasPrefix(loc.String(), p.srv.URL+"/authorize") || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" ||
		q.Get("state") == "" || q.Get("nonce") == "" || q.Get("client_id") != "cid" || q.Get("redirect_uri") != "https://h.example.com/auth/oidc/callback" ||
		!strings.Contains(q.Get("scope"), "groups") {
		t.Fatalf("authorize url: %s", loc)
	}
	p.nonce = q.Get("nonce")
	p.gotChallenge = q.Get("code_challenge")
	var fc *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == flowCookie {
			fc = c
		}
	}
	if fc == nil || !fc.HttpOnly || !fc.Secure {
		t.Fatalf("flow cookie: %+v", fc)
	}
	cb := url.Values{"code": {code}, "state": {q.Get("state")}}
	return f.do("GET", "/auth/oidc/callback?"+cb.Encode(), func(r *http.Request) { r.AddCookie(fc) })
}

func oidcFixture(t *testing.T, p *fakeIDP, groups []string) *fixture {
	return newFixture(t, func(o *Options) {
		o.PublicURL = "https://h.example.com"
		o.OIDC = NewOIDC(OIDCConfig{
			Issuer: p.srv.URL, ClientID: "cid", ClientSecret: "csecret", Name: "Authelia",
			AllowedGroups: groups, RedirectURL: "https://h.example.com/auth/oidc/callback",
		}, nil)
	})
}

func sessionFrom(t *testing.T, f *fixture, w *httptest.ResponseRecorder) Claims {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie && c.MaxAge > 0 {
			cl, ok := f.signer.VerifySession(c.Value)
			if !ok {
				t.Fatal("bad session cookie")
			}
			return cl
		}
	}
	t.Fatalf("no session cookie (status %d, loc %q)", w.Code, w.Header().Get("Location"))
	return Claims{}
}

func TestOIDCAllowedGroup(t *testing.T) {
	p := newFakeIDP(t)
	p.claims = map[string]any{"preferred_username": "jane", "email": "jane@example.com", "groups": []string{"users", "admins"}}
	f := oidcFixture(t, p, []string{"admins"})
	w := p.login(t, f, "good-code")
	if w.Code != 303 || w.Header().Get("Location") != "/" {
		t.Fatalf("%d %q %s", w.Code, w.Header().Get("Location"), w.Body)
	}
	if c := sessionFrom(t, f, w); c.User != "jane" || c.Method != "oidc" {
		t.Errorf("claims %+v", c)
	}
	// PKCE: challenge = S256(verifier).
	sum := sha256.Sum256([]byte(p.gotVerifier))
	if p.gotVerifier == "" || base64.RawURLEncoding.EncodeToString(sum[:]) != p.gotChallenge {
		t.Error("PKCE verifier doesn't match challenge")
	}
}

func TestOIDCDeniedGroup(t *testing.T) {
	p := newFakeIDP(t)
	p.claims = map[string]any{"preferred_username": "mallory", "groups": []string{"users"}}
	f := oidcFixture(t, p, []string{"admins"})
	w := p.login(t, f, "good-code")
	if w.Code != 403 || !strings.Contains(w.Body.String(), "not allowed") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie && c.MaxAge > 0 {
			t.Error("session issued to denied user")
		}
	}
}

func TestOIDCFallbacks(t *testing.T) {
	p := newFakeIDP(t)
	// No groups in the token: userinfo supplies them; no username: email.
	p.claims = map[string]any{"email": "kim@example.com"}
	p.userinf = map[string]any{"sub": "sub-1", "groups": []string{"admins"}}
	f := oidcFixture(t, p, []string{"admins"})
	if c := sessionFrom(t, f, p.login(t, f, "good-code")); c.User != "kim@example.com" {
		t.Errorf("user = %q", c.User)
	}
	// Nothing but sub; any group allowed.
	p.claims = map[string]any{}
	p.userinf = map[string]any{"sub": "sub-1"}
	f = oidcFixture(t, p, nil)
	if c := sessionFrom(t, f, p.login(t, f, "good-code")); c.User != "sub-1" {
		t.Errorf("user = %q", c.User)
	}
}

func TestOIDCRejections(t *testing.T) {
	p := newFakeIDP(t)
	p.claims = map[string]any{"preferred_username": "jane", "groups": []string{"admins"}}
	f := oidcFixture(t, p, []string{"admins"})

	// Bad code at the token endpoint.
	if w := p.login(t, f, "bad-code"); w.Code != 303 || w.Header().Get("Location") != "/login?error=oidc" {
		t.Errorf("bad code: %d %q", w.Code, w.Header().Get("Location"))
	}
	// Wrong nonce in the id_token.
	p.claims["nonce"] = "something-else"
	if w := p.login(t, f, "good-code"); w.Header().Get("Location") != "/login?error=oidc" {
		t.Errorf("nonce: %d %q", w.Code, w.Header().Get("Location"))
	}
	delete(p.claims, "nonce")
	// Wrong audience.
	p.claims["aud"] = "other"
	if w := p.login(t, f, "good-code"); w.Header().Get("Location") != "/login?error=oidc" {
		t.Errorf("aud: %d", w.Code)
	}
	delete(p.claims, "aud")
	// State mismatch and missing flow cookie.
	w := f.do("GET", "/auth/oidc/start", nil)
	var fc *http.Cookie
	for _, c := range w.Result().Cookies() {
		fc = c
	}
	if w := f.do("GET", "/auth/oidc/callback?code=good-code&state=wrong", func(r *http.Request) { r.AddCookie(fc) }); w.Header().Get("Location") != "/login?error=oidc" {
		t.Errorf("state: %d", w.Code)
	}
	if w := f.do("GET", "/auth/oidc/callback?code=good-code&state=x", nil); w.Header().Get("Location") != "/login?error=oidc" {
		t.Errorf("no cookie: %d", w.Code)
	}
	// OIDC off: 404.
	if w := newFixture(t, nil).do("GET", "/auth/oidc/start", nil); w.Code != 404 {
		t.Errorf("oidc off: %d", w.Code)
	}
}

func TestLocalSessionEndsOnPasswordChangeOrRemoval(t *testing.T) {
	f := newFixture(t, nil)
	me := func(c *http.Cookie) int {
		return f.do("GET", "/api/me", func(r *http.Request) { r.AddCookie(c) }).Code
	}
	c := f.cookie(t)
	if got := me(c); got != 200 {
		t.Fatalf("fresh session: %d", got)
	}
	if err := f.users.Set("alice", "new-password"); err != nil {
		t.Fatal(err)
	}
	if got := me(c); got != 401 {
		t.Errorf("after password change: %d, want 401", got)
	}
	c = f.cookie(t)
	if got := me(c); got != 200 {
		t.Fatalf("new session: %d", got)
	}
	if _, err := f.users.Remove("alice"); err != nil {
		t.Fatal(err)
	}
	if got := me(c); got != 401 {
		t.Errorf("after removal: %d, want 401", got)
	}
}

func TestUsersReloadsWhenFileChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	a, err := OpenUsers(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Set("alice", "one"); err != nil {
		t.Fatal(err)
	}
	// Another process (the `user set` CLI) changes the file.
	b, err := OpenUsers(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Set("bob", "two"); err != nil {
		t.Fatal(err)
	}
	// Make sure the mtime differs even on coarse-timestamp filesystems.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	if !a.Verify("bob", "two") {
		t.Error("store did not pick up an account added on disk")
	}
	if _, ok := a.Gen("bob"); !ok {
		t.Error("Gen: bob missing after reload")
	}
}
