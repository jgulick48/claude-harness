package webserve

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const flowCookie = "harness_oidc"

// OIDCConfig configures single sign-on.
type OIDCConfig struct {
	Issuer        string
	ClientID      string
	ClientSecret  string
	Name          string   // button label
	AllowedGroups []string // empty: any authenticated user
	Scopes        []string
	RedirectURL   string // <public-url>/auth/oidc/callback
}

// OIDC performs the authorization code flow with PKCE. Discovery is lazy so
// the server can start before the identity provider is reachable.
type OIDC struct {
	cfg    OIDCConfig
	client *http.Client

	mu       sync.Mutex
	provider *oidc.Provider
	oauth    *oauth2.Config
}

// NewOIDC returns an OIDC flow; client may be nil.
func NewOIDC(cfg OIDCConfig, client *http.Client) *OIDC {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	if cfg.Name == "" {
		cfg.Name = "Authelia"
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{oidc.ScopeOpenID, "profile", "email", "groups"}
	}
	return &OIDC{cfg: cfg, client: client}
}

func (o *OIDC) ctx(ctx context.Context) context.Context {
	return oidc.ClientContext(ctx, o.client)
}

func (o *OIDC) init(ctx context.Context) (*oidc.Provider, *oauth2.Config, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.provider != nil {
		return o.provider, o.oauth, nil
	}
	p, err := oidc.NewProvider(o.ctx(ctx), o.cfg.Issuer)
	if err != nil {
		return nil, nil, err
	}
	o.provider = p
	o.oauth = &oauth2.Config{
		ClientID: o.cfg.ClientID, ClientSecret: o.cfg.ClientSecret,
		Endpoint: p.Endpoint(), RedirectURL: o.cfg.RedirectURL, Scopes: o.cfg.Scopes,
	}
	return o.provider, o.oauth, nil
}

func (h *handler) flowCookie(r *http.Request, v string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name: flowCookie, Value: v, Path: "/auth/oidc", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: h.secureCookie(r), MaxAge: maxAge,
	}
}

func (h *handler) oidcFail(w http.ResponseWriter, r *http.Request, code string) {
	http.SetCookie(w, h.flowCookie(r, "", -1))
	noStore(w)
	http.Redirect(w, r, loginURL(code, ""), http.StatusSeeOther)
}

func (h *handler) oidcStart(w http.ResponseWriter, r *http.Request) {
	if h.OIDC == nil {
		http.NotFound(w, r)
		return
	}
	_, oc, err := h.OIDC.init(r.Context())
	if err != nil {
		h.Logger.Error("oidc discovery failed", "err", err)
		h.oidcFail(w, r, "oidc")
		return
	}
	fl := flowState{
		State: randomString(24), Nonce: randomString(24), Verifier: oauth2.GenerateVerifier(),
		Exp: h.now().Add(10 * time.Minute).Unix(),
	}
	cv, err := h.Signer.sealFlow(fl)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, h.flowCookie(r, cv, 600))
	u := oc.AuthCodeURL(fl.State, oauth2.S256ChallengeOption(fl.Verifier), oidc.Nonce(fl.Nonce))
	noStore(w)
	http.Redirect(w, r, u, http.StatusSeeOther)
}

func (h *handler) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if h.OIDC == nil {
		http.NotFound(w, r)
		return
	}
	ck, err := r.Cookie(flowCookie)
	if err != nil {
		h.oidcFail(w, r, "oidc")
		return
	}
	fl, ok := h.Signer.openFlow(ck.Value)
	q := r.URL.Query()
	if !ok || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(fl.State)) != 1 {
		h.oidcFail(w, r, "oidc")
		return
	}
	if q.Get("error") != "" || q.Get("code") == "" {
		h.Logger.Warn("oidc provider returned an error", "error", q.Get("error"))
		h.oidcFail(w, r, "oidc")
		return
	}
	user, groups, err := h.OIDC.exchange(r.Context(), q.Get("code"), fl)
	if err != nil {
		h.Logger.Warn("oidc login failed", "err", err)
		h.oidcFail(w, r, "oidc")
		return
	}
	if !h.OIDC.groupAllowed(groups) {
		h.Logger.Warn("oidc user not in an allowed group", "user", user)
		http.SetCookie(w, h.flowCookie(r, "", -1))
		h.renderLogin(w, r, http.StatusForbidden, "forbidden", "/")
		return
	}
	http.SetCookie(w, h.flowCookie(r, "", -1))
	if err := h.startSession(w, r, user, "oidc", ""); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	noStore(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (o *OIDC) groupAllowed(groups []string) bool {
	if len(o.cfg.AllowedGroups) == 0 {
		return true
	}
	for _, g := range groups {
		if slices.Contains(o.cfg.AllowedGroups, g) {
			return true
		}
	}
	return false
}

type idClaims struct {
	PreferredUsername string   `json:"preferred_username"`
	Email             string   `json:"email"`
	Groups            []string `json:"groups"`
}

// exchange trades the code for tokens, validates the ID token (signature,
// issuer, audience, expiry, nonce) and returns the user name and groups.
func (o *OIDC) exchange(ctx context.Context, code string, fl flowState) (string, []string, error) {
	p, oc, err := o.init(ctx)
	if err != nil {
		return "", nil, err
	}
	octx := o.ctx(ctx)
	tok, err := oc.Exchange(octx, code, oauth2.VerifierOption(fl.Verifier))
	if err != nil {
		return "", nil, fmt.Errorf("token exchange: %w", err)
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return "", nil, errors.New("no id_token in token response")
	}
	idt, err := p.Verifier(&oidc.Config{ClientID: o.cfg.ClientID}).Verify(octx, raw)
	if err != nil {
		return "", nil, fmt.Errorf("verify id_token: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(fl.Nonce)) != 1 {
		return "", nil, errors.New("nonce mismatch")
	}
	var c idClaims
	if err := idt.Claims(&c); err != nil {
		return "", nil, err
	}
	// Groups (and sometimes the name) may only be in userinfo.
	if len(c.Groups) == 0 || (c.PreferredUsername == "" && c.Email == "") {
		if ui, err := p.UserInfo(octx, oauth2.StaticTokenSource(tok)); err == nil {
			var u idClaims
			if ui.Claims(&u) == nil {
				if len(c.Groups) == 0 {
					c.Groups = u.Groups
				}
				if c.PreferredUsername == "" {
					c.PreferredUsername = u.PreferredUsername
				}
				if c.Email == "" {
					c.Email = u.Email
				}
			}
		}
	}
	user := c.PreferredUsername
	if user == "" {
		user = c.Email
	}
	if user == "" {
		user = idt.Subject
	}
	if user == "" {
		return "", nil, errors.New("no usable user name claim")
	}
	return user, c.Groups, nil
}
