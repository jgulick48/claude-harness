package webserve

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	sessionCookie = "harness_session"
	sessionTTL    = 7 * 24 * time.Hour

	purposeSession = "session"
	purposeOIDC    = "oidc-flow"
)

// Claims is the signed content of a session cookie.
type Claims struct {
	User   string `json:"u"`
	Method string `json:"m"`
	Exp    int64  `json:"e"`
	// Gen ties a local session to the account's current password hash
	// (Users.Gen): removing the account or changing its password ends it.
	Gen string `json:"g,omitempty"`
}

// Signer produces and checks HMAC-SHA256 signed, self-contained tokens. The
// purpose is mixed into the MAC so a token of one kind is never valid as
// another.
type Signer struct {
	key []byte
	now func() time.Time
}

// NewSigner returns a Signer using key.
func NewSigner(key []byte) *Signer { return &Signer{key: key, now: time.Now} }

// LoadOrCreateKey reads the 32-byte session key from path, creating it (0600)
// when missing.
func LoadOrCreateKey(path string) ([]byte, error) {
	if b, err := os.ReadFile(path); err == nil {
		if len(b) >= 32 {
			return b, nil
		}
		return nil, fmt.Errorf("%s: key too short", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) { // raced with another starter
			return os.ReadFile(path)
		}
		return nil, err
	}
	_, werr := f.Write(key)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return key, werr
}

func (s *Signer) mac(purpose string, payload []byte) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(purpose))
	m.Write([]byte{0})
	m.Write(payload)
	return m.Sum(nil)
}

func (s *Signer) seal(purpose string, v any) (string, error) {
	payload, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString(payload) + "." + enc.EncodeToString(s.mac(purpose, payload)), nil
}

func (s *Signer) open(purpose, token string, v any) bool {
	p, m, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	enc := base64.RawURLEncoding
	payload, err := enc.DecodeString(p)
	if err != nil {
		return false
	}
	got, err := enc.DecodeString(m)
	if err != nil || !hmac.Equal(got, s.mac(purpose, payload)) {
		return false
	}
	return json.Unmarshal(payload, v) == nil
}

// SignSession returns a cookie value for user, valid for sessionTTL.
func (s *Signer) SignSession(user, method, gen string) (string, error) {
	return s.seal(purposeSession, Claims{User: user, Method: method, Gen: gen, Exp: s.now().Add(sessionTTL).Unix()})
}

// VerifySession checks signature and expiry.
func (s *Signer) VerifySession(token string) (Claims, bool) {
	var c Claims
	if !s.open(purposeSession, token, &c) || c.User == "" || s.now().Unix() >= c.Exp {
		return Claims{}, false
	}
	return c, true
}

// flowState is the short-lived OIDC login state kept in a cookie.
type flowState struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Exp      int64  `json:"e"`
}

func (s *Signer) sealFlow(f flowState) (string, error) { return s.seal(purposeOIDC, f) }

func (s *Signer) openFlow(token string) (flowState, bool) {
	var f flowState
	if !s.open(purposeOIDC, token, &f) || s.now().Unix() >= f.Exp {
		return flowState{}, false
	}
	return f, true
}

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
