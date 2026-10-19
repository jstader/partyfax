package main

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// Access checks the token Cloudflare Access adds to every request it lets
// through to /admin. Access at the edge is the lock; this is the second one,
// so an Access application that was deleted or misconfigured leaves admin
// closed, not open. Anything inside the compose network could call the booth
// directly, and only Cloudflare can sign these tokens.
//
// The token is a JWT in the Cf-Access-Jwt-Assertion header, RS256-signed
// with keys published at https://<team>/cdn-cgi/access/certs.
type Access struct {
	issuer   string // https://<team>.cloudflareaccess.com
	audience string // the Access application's AUD tag
	certsURL string
	http     *http.Client
	now      func() time.Time

	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	fetched time.Time
}

const accessHeader = "Cf-Access-Jwt-Assertion"

// keysTTL is how long fetched keys are trusted before asking again. A token
// signed with a key we don't have also triggers a fetch, rate-limited by
// keysRetry so junk tokens can't make us hammer Cloudflare.
const (
	keysTTL   = time.Hour
	keysRetry = time.Minute
)

// NewAccess returns nil when team or audience is empty: admin is then off.
func NewAccess(team, audience string, hc *http.Client) *Access {
	if team == "" || audience == "" {
		return nil
	}
	team = strings.TrimPrefix(strings.TrimSuffix(team, "/"), "https://")
	return &Access{
		issuer:   "https://" + team,
		audience: audience,
		certsURL: "https://" + team + "/cdn-cgi/access/certs",
		http:     hc,
		now:      time.Now,
	}
}

var errNoToken = errors.New("no Access token")

// Verify returns the signed-in email if the request carries a valid token
// for this application.
func (a *Access) Verify(ctx context.Context, r *http.Request) (string, error) {
	tok := r.Header.Get(accessHeader)
	if tok == "" {
		return "", errNoToken
	}

	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return "", errors.New("malformed token")
	}

	var head struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodeSegment(parts[0], &head); err != nil {
		return "", fmt.Errorf("token header: %w", err)
	}
	// Only RS256: accepting whatever alg the token names is the classic hole.
	if head.Alg != "RS256" {
		return "", fmt.Errorf("token algorithm %q", head.Alg)
	}

	key, err := a.key(ctx, head.Kid)
	if err != nil {
		return "", err
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", errors.New("token signature is not base64url")
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig); err != nil {
		return "", errors.New("token signature does not verify")
	}

	var claims struct {
		Iss   string   `json:"iss"`
		Aud   audience `json:"aud"`
		Exp   int64    `json:"exp"`
		Nbf   int64    `json:"nbf"`
		Email string   `json:"email"`
	}
	if err := decodeSegment(parts[1], &claims); err != nil {
		return "", fmt.Errorf("token claims: %w", err)
	}

	now := a.now().Unix()
	switch {
	case claims.Iss != a.issuer:
		return "", fmt.Errorf("token issuer %q", claims.Iss)
	case !slices.Contains(claims.Aud, a.audience):
		return "", errors.New("token is for another application")
	case claims.Exp == 0 || now >= claims.Exp:
		return "", errors.New("token expired")
	case claims.Nbf != 0 && now < claims.Nbf:
		return "", errors.New("token not valid yet")
	}

	return claims.Email, nil
}

// audience is the aud claim, which may be one string or a list.
type audience []string

func (a *audience) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*a = []string{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

func decodeSegment(seg string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func (a *Access) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	age := a.now().Sub(a.fetched)
	k, ok := a.keys[kid]
	if ok && age < keysTTL {
		return k, nil
	}
	if (ok || a.keys != nil) && age < keysRetry {
		if ok {
			return k, nil
		}
		return nil, fmt.Errorf("unknown signing key %q", kid)
	}

	keys, err := a.fetch(ctx)
	if err != nil {
		// Keep using known keys if Cloudflare is briefly unreachable.
		if ok {
			return k, nil
		}
		return nil, fmt.Errorf("fetching Access keys: %w", err)
	}
	a.keys, a.fetched = keys, a.now()

	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("unknown signing key %q", kid)
}

func (a *Access) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.certsURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s replied %d", a.certsURL, resp.StatusCode)
	}

	var set struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return nil, err
	}

	keys := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{
			N: new(big.Int).SetBytes(n),
			E: int(new(big.Int).SetBytes(e).Int64()),
		}
	}
	if len(keys) == 0 {
		return nil, errors.New("no RSA keys in the key set")
	}
	return keys, nil
}
