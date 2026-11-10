// Package oauth authenticates users with an OpenID Connect provider
// (PingFederate). It verifies JWTs and runs the authorization-code flow; it
// does not decide what a user may do — that stays with LDAP group membership.
package oauth

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"notification_relay/config"
)

// ErrInvalidToken is returned when a token fails signature, issuer, audience,
// expiry, nonce or claim validation.
var ErrInvalidToken = errors.New("invalid token")

// Identity is the verified identity extracted from a token.
type Identity struct {
	Subject  string
	Username string // value of the configured username claim
}

// Provider is the surface the API layer depends on.
type Provider interface {
	// VerifyBearer validates a raw JWT access token presented to the API.
	VerifyBearer(ctx context.Context, raw string) (*Identity, error)
	// AuthCodeURL builds the provider authorization URL for the UI login.
	AuthCodeURL(ctx context.Context, state, nonce, pkceVerifier string) (string, error)
	// Exchange trades an authorization code for tokens and verifies the ID
	// token, including the nonce.
	Exchange(ctx context.Context, code, nonce, pkceVerifier string) (*Identity, error)
	// ProviderName is the label shown on the sign-in button.
	ProviderName() string
}

// Client is the OIDC-backed Provider. Discovery is lazy and retried, so the
// relay still starts (and LDAP logins still work) while the provider is down.
type Client struct {
	cfg  config.OAuthConfig
	http *http.Client

	mu       sync.Mutex
	provider *oidc.Provider
}

func New(cfg config.OAuthConfig) *Client {
	hc := &http.Client{}
	if cfg.TLSSkipVerify {
		hc.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec // opt-in, testing only
	}
	return &Client{cfg: cfg, http: hc}
}

func (c *Client) ProviderName() string { return c.cfg.ProviderName }

func (c *Client) discover(ctx context.Context) (*oidc.Provider, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.provider != nil {
		return c.provider, nil
	}
	ctx = oidc.ClientContext(ctx, c.http)
	p, err := oidc.NewProvider(ctx, c.cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	c.provider = p
	return p, nil
}

func (c *Client) verifier(p *oidc.Provider, clientID string) *oidc.IDTokenVerifier {
	return p.Verifier(&oidc.Config{ClientID: clientID, SupportedSigningAlgs: c.cfg.SigningAlgs})
}

func (c *Client) oauth2Config(p *oidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     c.cfg.ClientID,
		ClientSecret: c.cfg.ClientSecret,
		Endpoint:     p.Endpoint(),
		RedirectURL:  c.cfg.RedirectURL,
		Scopes:       append([]string{oidc.ScopeOpenID}, withoutOpenID(c.cfg.Scopes)...),
	}
}

func withoutOpenID(in []string) []string {
	var out []string
	for _, s := range in {
		if s != oidc.ScopeOpenID {
			out = append(out, s)
		}
	}
	return out
}

func (c *Client) identity(tok *oidc.IDToken) (*Identity, error) {
	var claims map[string]any
	if err := tok.Claims(&claims); err != nil {
		return nil, fmt.Errorf("%w: parse claims: %v", ErrInvalidToken, err)
	}
	name, _ := claims[c.cfg.UsernameClaim].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: missing %q claim", ErrInvalidToken, c.cfg.UsernameClaim)
	}
	return &Identity{Subject: tok.Subject, Username: name}, nil
}

func (c *Client) VerifyBearer(ctx context.Context, raw string) (*Identity, error) {
	p, err := c.discover(ctx)
	if err != nil {
		return nil, err
	}
	ctx = oidc.ClientContext(ctx, c.http)
	tok, err := c.verifier(p, c.cfg.Audience).Verify(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	return c.identity(tok)
}

func (c *Client) AuthCodeURL(ctx context.Context, state, nonce, pkceVerifier string) (string, error) {
	p, err := c.discover(ctx)
	if err != nil {
		return "", err
	}
	return c.oauth2Config(p).AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.S256ChallengeOption(pkceVerifier),
	), nil
}

func (c *Client) Exchange(ctx context.Context, code, nonce, pkceVerifier string) (*Identity, error) {
	p, err := c.discover(ctx)
	if err != nil {
		return nil, err
	}
	ctx = oidc.ClientContext(ctx, c.http)
	tok, err := c.oauth2Config(p).Exchange(ctx, code, oauth2.VerifierOption(pkceVerifier))
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}
	rawID, _ := tok.Extra("id_token").(string)
	if rawID == "" {
		return nil, fmt.Errorf("%w: no id_token in token response", ErrInvalidToken)
	}
	idTok, err := c.verifier(p, c.cfg.ClientID).Verify(ctx, rawID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if idTok.Nonce != nonce {
		return nil, fmt.Errorf("%w: nonce mismatch", ErrInvalidToken)
	}
	return c.identity(idTok)
}
