package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeAccess is Cloudflare's side: a key set to serve and a key to sign with.
type fakeAccess struct {
	key     *rsa.PrivateKey
	kid     string
	fetches atomic.Int32
	srv     *httptest.Server
}

func newFakeAccess(t *testing.T) *fakeAccess {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeAccess{key: key, kid: "k1"}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.fetches.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kid": f.kid, "kty": "RSA", "alg": "RS256",
			"n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAccess) access(now time.Time) *Access {
	a := NewAccess("team.cloudflareaccess.com", "aud-1", f.srv.Client())
	a.certsURL = f.srv.URL
	a.now = func() time.Time { return now }
	return a
}

func (f *fakeAccess) token(t *testing.T, head, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(head)
	c, _ := json.Marshal(claims)
	signed := b64(h) + "." + b64(c)
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return signed + "." + b64(sig)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

var now = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func goodClaims() map[string]any {
	return map[string]any{
		"iss": "https://team.cloudflareaccess.com", "aud": []string{"aud-1"},
		"exp": now.Add(time.Hour).Unix(), "nbf": now.Add(-time.Minute).Unix(),
		"email": "host@example.com",
	}
}

func verify(a *Access, tok string) (string, error) {
	r := httptest.NewRequest(http.MethodGet, "/api/booth/admin/events", nil)
	if tok != "" {
		r.Header.Set(accessHeader, tok)
	}
	return a.Verify(context.Background(), r)
}

func TestAccessAcceptsAGoodToken(t *testing.T) {
	f := newFakeAccess(t)
	email, err := verify(f.access(now), f.token(t, map[string]any{"alg": "RS256", "kid": "k1"}, goodClaims()))
	if err != nil || email != "host@example.com" {
		t.Fatalf("Verify = %q, %v", email, err)
	}
}

func TestAccessRefuses(t *testing.T) {
	f := newFakeAccess(t)
	head := map[string]any{"alg": "RS256", "kid": "k1"}
	with := func(k string, v any) map[string]any { c := goodClaims(); c[k] = v; return c }

	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	forged := func() string {
		g := &fakeAccess{key: other, kid: "k1"}
		return g.token(t, head, goodClaims())
	}

	cases := map[string]string{
		"no token":         "",
		"garbage":          "not.a.jwt.at.all",
		"wrong audience":   f.token(t, head, with("aud", "someone-else")),
		"wrong issuer":     f.token(t, head, with("iss", "https://evil.cloudflareaccess.com")),
		"expired":          f.token(t, head, with("exp", now.Add(-time.Second).Unix())),
		"no expiry":        f.token(t, head, with("exp", 0)),
		"not yet valid":    f.token(t, head, with("nbf", now.Add(time.Hour).Unix())),
		"alg none":         f.token(t, map[string]any{"alg": "none", "kid": "k1"}, goodClaims()),
		"alg HS256":        f.token(t, map[string]any{"alg": "HS256", "kid": "k1"}, goodClaims()),
		"unknown key":      f.token(t, map[string]any{"alg": "RS256", "kid": "k9"}, goodClaims()),
		"someone else key": forged(),
	}

	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			if email, err := verify(f.access(now), tok); err == nil {
				t.Errorf("accepted, as %q", email)
			}
		})
	}
}

// Keys are fetched once and reused; a flood of tokens naming unknown keys
// mustn't turn into a flood of fetches.
func TestAccessCachesKeys(t *testing.T) {
	f := newFakeAccess(t)
	a := f.access(now)
	good := f.token(t, map[string]any{"alg": "RS256", "kid": "k1"}, goodClaims())
	unknown := f.token(t, map[string]any{"alg": "RS256", "kid": "nope"}, goodClaims())

	for range 5 {
		verify(a, good)
		verify(a, unknown)
	}
	if n := f.fetches.Load(); n != 1 {
		t.Errorf("%d key fetches, want 1", n)
	}
}

func TestNoAccessWhenUnconfigured(t *testing.T) {
	if NewAccess("", "aud", http.DefaultClient) != nil || NewAccess("team", "", http.DefaultClient) != nil {
		t.Error("admin on with half a configuration")
	}
	if a := NewAccess("https://team.cloudflareaccess.com/", "x", http.DefaultClient); !strings.HasSuffix(a.certsURL, "team.cloudflareaccess.com/cdn-cgi/access/certs") {
		t.Errorf("certs URL %s", a.certsURL)
	}
}
