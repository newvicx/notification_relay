package testutil

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// FakeIdP is a minimal OpenID Connect provider for tests: discovery, JWKS,
// and a token endpoint that enforces PKCE. Tokens are really RS256-signed.
type FakeIdP struct {
	Server   *httptest.Server
	ClientID string
	key      *rsa.PrivateKey
	mu       sync.Mutex
	codes    map[string]fakeCode
}

type fakeCode struct {
	nonce, challenge, user string
}

func NewFakeIdP(t *testing.T, clientID string) *FakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &FakeIdP{ClientID: clientID, key: key, codes: map[string]fakeCode{}}
	mux := http.NewServeMux()
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Server.Close)

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                f.Server.URL,
			"authorization_endpoint":                f.Server.URL + "/authorize",
			"token_endpoint":                        f.Server.URL + "/token",
			"jwks_uri":                              f.Server.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
			{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"},
		}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		c, ok := f.codes[r.Form.Get("code")]
		delete(f.codes, r.Form.Get("code"))
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != c.challenge {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "opaque",
			"token_type":   "Bearer",
			"expires_in":   300,
			"id_token":     f.Sign(map[string]any{"aud": f.ClientID, "sub": c.user, "nonce": c.nonce}),
		})
	})
	return f
}

func (f *FakeIdP) Issuer() string { return f.Server.URL }

// IssueCode registers an authorization code, as if the user had logged in at
// the provider with the given PKCE challenge and nonce.
func (f *FakeIdP) IssueCode(user, nonce, challenge string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	code := base64.RawURLEncoding.EncodeToString(b)
	f.mu.Lock()
	f.codes[code] = fakeCode{nonce: nonce, challenge: challenge, user: user}
	f.mu.Unlock()
	return code
}

// Sign returns an RS256 JWT. iss, iat and exp default sensibly and can be
// overridden through claims.
func (f *FakeIdP) Sign(claims map[string]any) string {
	return f.SignWithKey(f.key, claims)
}

func (f *FakeIdP) SignWithKey(key *rsa.PrivateKey, claims map[string]any) string {
	c := map[string]any{
		"iss": f.Server.URL,
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	}
	for k, v := range claims {
		c[k] = v
	}
	sig, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
	if err != nil {
		panic(err)
	}
	raw, err := jwt.Signed(sig).Claims(c).Serialize()
	if err != nil {
		panic(err)
	}
	return raw
}
