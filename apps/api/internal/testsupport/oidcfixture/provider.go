// Package oidcfixture supplies signed OIDC transport for functional tests only.
package oidcfixture

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
)

type Provider struct {
	Server      *httptest.Server
	mu          sync.Mutex
	subject     string
	codes       map[string]grant
	key         *rsa.PrivateKey
	callback    string
	interactive bool
}
type grant struct{ nonce, challenge string }

func New(t testing.TB, callback string) *Provider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := &Provider{subject: "fixture-operator", codes: map[string]grant{}, key: key, callback: callback}
	p.Server = httptest.NewServer(http.HandlerFunc(p.respond))
	t.Cleanup(p.Server.Close)
	return p
}

func (p *Provider) SetSubject(subject string) { p.mu.Lock(); defer p.mu.Unlock(); p.subject = subject }

// Browser tests must stop at the IdP until explicit sign-in, like real prompt=login.
// Protocol-only tests retain their automatic redirect without a credential UI.
func (p *Provider) RequireSignIn() { p.mu.Lock(); defer p.mu.Unlock(); p.interactive = true }

func (p *Provider) respond(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	issuer := p.Server.URL
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}})
	case "/keys":
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &p.key.PublicKey, KeyID: "fixture", Algorithm: "RS256", Use: "sig"}}})
	case "/authorize":
		q := r.URL.Query()
		if q.Get("redirect_uri") != p.callback || q.Get("code_challenge_method") != "S256" || q.Get("nonce") == "" || q.Get("state") == "" || q.Get("client_id") != "fixture-client" || q.Get("prompt") != "login" && q.Get("prompt") != "create" {
			http.Error(w, "invalid authorization contract", 400)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		p.mu.Lock()
		interactive := p.interactive
		p.mu.Unlock()
		if interactive && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<!doctype html><title>OIDC test provider</title><h1>Fixture sign in</h1><form method="post"><button type="submit">Sign in</button></form>`)
			return
		}
		code := rand.Text()
		p.mu.Lock()
		p.codes[code] = grant{nonce: q.Get("nonce"), challenge: q.Get("code_challenge")}
		p.mu.Unlock()
		http.Redirect(w, r, p.callback+"?code="+code+"&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	case "/token":
		_ = r.ParseForm()
		p.mu.Lock()
		g, found := p.codes[r.Form.Get("code")]
		delete(p.codes, r.Form.Get("code"))
		subject := p.subject
		p.mu.Unlock()
		hash := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !found || base64.RawURLEncoding.EncodeToString(hash[:]) != g.challenge || r.Form.Get("redirect_uri") != p.callback || r.Form.Get("grant_type") != "authorization_code" {
			http.Error(w, "invalid exchange contract", 400)
			return
		}
		signer, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: p.key}, (&jose.SignerOptions{}).WithHeader("kid", "fixture"))
		payload, _ := json.Marshal(map[string]any{"iss": issuer, "sub": subject, "aud": "fixture-client", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": g.nonce, "name": "Fixture operator"})
		signed, _ := signer.Sign(payload)
		token, _ := signed.CompactSerialize()
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fixture-only", "token_type": "Bearer", "id_token": token})
	default:
		http.NotFound(w, r)
	}
}
