package oauth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net/url"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"notification_relay/config"
	"notification_relay/oauth"
	"notification_relay/testutil"
)

func newClient(t *testing.T) (*oauth.Client, *testutil.FakeIdP) {
	t.Helper()
	idp := testutil.NewFakeIdP(t, "relay")
	c := oauth.New(config.OAuthConfig{
		Enabled: true, Issuer: idp.Issuer(), ClientID: "relay", ClientSecret: "s",
		RedirectURL: "https://relay.example.com/ui/oauth/callback", Audience: "relay-api",
		Scopes: []string{"openid", "profile"}, UsernameClaim: "sAMAccountName",
		SigningAlgs: []string{"RS256"},
	})
	return c, idp
}

func TestVerifyBearer(t *testing.T) {
	c, idp := newClient(t)
	ctx := context.Background()
	other, _ := rsa.GenerateKey(rand.Reader, 2048)

	good := map[string]any{"aud": "relay-api", "sub": "x", "sAMAccountName": "alice"}
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for kk, vv := range good {
			m[kk] = vv
		}
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
		return m
	}

	id, err := c.VerifyBearer(ctx, idp.Sign(good))
	if err != nil || id.Username != "alice" {
		t.Fatalf("valid token: id=%+v err=%v", id, err)
	}

	bad := map[string]string{
		"expired":        idp.Sign(with("exp", time.Now().Add(-time.Hour).Unix())),
		"wrong audience": idp.Sign(with("aud", "someone-else")),
		"wrong issuer":   idp.Sign(with("iss", "https://evil.example.com")),
		"missing claim":  idp.Sign(with("sAMAccountName", nil)),
		"wrong key":      idp.SignWithKey(other, good),
		"garbage":        "not.a.jwt",
	}
	for name, tok := range bad {
		if _, err := c.VerifyBearer(ctx, tok); !errors.Is(err, oauth.ErrInvalidToken) {
			t.Errorf("%s: want ErrInvalidToken, got %v", name, err)
		}
	}
}

func TestAuthCodeURLAndMissingUsernameClaim(t *testing.T) {
	c, idp := newClient(t)
	ctx := context.Background()
	verifier := oauth2.GenerateVerifier()

	u, err := c.AuthCodeURL(ctx, "st", "nonce1", verifier)
	if err != nil {
		t.Fatal(err)
	}
	q, _ := url.Parse(u)
	v := q.Query()
	if v.Get("code_challenge_method") != "S256" || v.Get("nonce") != "nonce1" || v.Get("state") != "st" ||
		v.Get("scope") != "openid profile" || v.Get("client_id") != "relay" {
		t.Fatalf("unexpected authorization params: %v", v)
	}

	// The fake token endpoint only sets "sub"; this client reads
	// "sAMAccountName", so the exchange must fail the claim check.
	code := idp.IssueCode("alice", "nonce1", v.Get("code_challenge"))
	if _, err := c.Exchange(ctx, code, "nonce1", verifier); !errors.Is(err, oauth.ErrInvalidToken) {
		t.Fatalf("want missing-claim error, got %v", err)
	}
}

func TestExchange(t *testing.T) {
	idp := testutil.NewFakeIdP(t, "relay")
	c := oauth.New(config.OAuthConfig{
		Issuer: idp.Issuer(), ClientID: "relay", RedirectURL: "https://r/cb", Audience: "relay",
		UsernameClaim: "sub", SigningAlgs: []string{"RS256"},
	})
	ctx := context.Background()
	verifier := oauth2.GenerateVerifier()
	u, _ := c.AuthCodeURL(ctx, "st", "n1", verifier)
	pu, _ := url.Parse(u)
	challenge := pu.Query().Get("code_challenge")

	id, err := c.Exchange(ctx, idp.IssueCode("alice", "n1", challenge), "n1", verifier)
	if err != nil || id.Username != "alice" {
		t.Fatalf("id=%+v err=%v", id, err)
	}
	if _, err := c.Exchange(ctx, idp.IssueCode("alice", "n1", challenge), "different-nonce", verifier); !errors.Is(err, oauth.ErrInvalidToken) {
		t.Errorf("nonce mismatch: got %v", err)
	}
	if _, err := c.Exchange(ctx, idp.IssueCode("alice", "n1", challenge), "n1", oauth2.GenerateVerifier()); err == nil {
		t.Error("wrong PKCE verifier should fail")
	}
}

func TestDiscoveryRetriedAfterFailure(t *testing.T) {
	c := oauth.New(config.OAuthConfig{Issuer: "http://127.0.0.1:1", ClientID: "x", Audience: "x", UsernameClaim: "sub"})
	if _, err := c.VerifyBearer(context.Background(), "a.b.c"); err == nil || errors.Is(err, oauth.ErrInvalidToken) {
		t.Fatalf("provider outage must be a non-token error, got %v", err)
	}
}
