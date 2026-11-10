package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"notification_relay/api"
	"notification_relay/config"
	ldap "notification_relay/ldap"
	"notification_relay/oauth"
	"notification_relay/testutil"
)

type stubGroupLookup struct {
	users map[string][]string
	err   error
}

func (s *stubGroupLookup) LookupGroups(_ context.Context, u string) (*ldap.AuthResult, error) {
	if s.err != nil {
		return nil, s.err
	}
	g, ok := s.users[u]
	if !ok {
		return nil, ldap.ErrUserNotFound
	}
	// Users may be looked up by email; the canonical name is the part before "@".
	return &ldap.AuthResult{Username: strings.SplitN(u, "@", 2)[0], UserDN: "CN=" + u + ",DC=x", Groups: g}, nil
}

func newOAuthServer(t *testing.T) (*api.Server, *testutil.FakeIdP) {
	t.Helper()
	idp := testutil.NewFakeIdP(t, "relay")
	// Basic auth always fails: proves the bearer path never touches the password authenticator.
	srv, _ := newTestServer(t, &stubAuth{err: ldap.ErrInvalidCredentials})
	srv.SetOAuth(oauth.New(config.OAuthConfig{
		Issuer: idp.Issuer(), ClientID: "relay", ClientSecret: "s", Audience: "relay",
		RedirectURL: "https://relay.example.com/ui/oauth/callback", Scopes: []string{"openid"},
		IdentityClaim: "sub", SigningAlgs: []string{"RS256"}, ProviderName: "Corporate SSO",
	}), &stubGroupLookup{users: map[string][]string{
		"pub":               {"grp-pub"},
		"reader":            {"grp-reader"},
		"norole":            {"grp-other"},
		"disabled":          nil,
		"carol@example.com": {"grp-pub"},
	}})
	return srv, idp
}

func bearerGet(srv *api.Server, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func TestBearer_ValidTokenRolesFromLDAP(t *testing.T) {
	srv, idp := newOAuthServer(t)
	tok := func(sub string) string { return idp.Sign(map[string]any{"aud": "relay", "sub": sub}) }

	if w := bearerGet(srv, "/api/v1/events", tok("reader")); w.Code != http.StatusOK {
		t.Fatalf("reader read: want 200, got %d: %s", w.Code, w.Body)
	}
	// Reader role (from LDAP) must not be able to hit an admin endpoint.
	if w := bearerGet(srv, "/api/v1/audit", tok("reader")); w.Code != http.StatusForbidden {
		t.Fatalf("reader admin: want 403, got %d", w.Code)
	}
	// Valid token, LDAP account without a mapped role / unknown account → 403, not 401.
	for _, u := range []string{"norole", "disabled", "ghost"} {
		if w := bearerGet(srv, "/api/v1/events", tok(u)); w.Code != http.StatusForbidden {
			t.Errorf("%s: want 403, got %d", u, w.Code)
		}
	}
}

func TestBearer_InvalidTokens(t *testing.T) {
	srv, idp := newOAuthServer(t)
	for name, tok := range map[string]string{
		"garbage":        "abc",
		"wrong audience": idp.Sign(map[string]any{"aud": "other", "sub": "pub"}),
		"expired":        idp.Sign(map[string]any{"aud": "relay", "sub": "pub", "exp": 1}),
	} {
		w := bearerGet(srv, "/api/v1/events", tok)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: want 401, got %d", name, w.Code)
		}
		if !strings.Contains(w.Header().Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("%s: missing Bearer challenge", name)
		}
	}
}

func TestBearer_DisabledWhenOAuthOff(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())
	if w := bearerGet(srv, "/api/v1/events", "anything"); w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestBearer_BasicAuthStillWorks(t *testing.T) {
	srv, _ := newTestServer(t, publisherAuth())
	if w := do(srv, "GET", "/api/v1/events", nil); w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

func TestBearer_LDAPOutageIs500(t *testing.T) {
	idp := testutil.NewFakeIdP(t, "relay")
	srv, _ := newTestServer(t, publisherAuth())
	srv.SetOAuth(oauth.New(config.OAuthConfig{Issuer: idp.Issuer(), ClientID: "relay", Audience: "relay", IdentityClaim: "sub"}),
		&stubGroupLookup{err: context.DeadlineExceeded})
	if w := bearerGet(srv, "/api/v1/events", idp.Sign(map[string]any{"aud": "relay", "sub": "pub"})); w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", w.Code)
	}
}

// startLogin hits /ui/oauth/login and returns the state cookie plus the
// authorization URL parameters the browser would carry to the provider.
func startLogin(t *testing.T, srv *api.Server) (*http.Cookie, url.Values) {
	t.Helper()
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/ui/oauth/login", nil))
	if w.Code != http.StatusFound {
		t.Fatalf("login: want 302, got %d: %s", w.Code, w.Body)
	}
	loc, _ := url.Parse(w.Header().Get("Location"))
	var c *http.Cookie
	for _, ck := range w.Result().Cookies() {
		if ck.Name == "nr_oauth_state" {
			c = ck
		}
	}
	if c == nil || !c.HttpOnly {
		t.Fatal("missing HttpOnly state cookie")
	}
	return c, loc.Query()
}

func callback(srv *api.Server, c *http.Cookie, state, code string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/ui/oauth/callback?state="+url.QueryEscape(state)+"&code="+url.QueryEscape(code), nil)
	if c != nil {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func TestUIOAuth_FullFlow(t *testing.T) {
	srv, idp := newOAuthServer(t)
	c, q := startLogin(t, srv)
	if !strings.HasPrefix(q.Get("redirect_uri"), "https://relay.example.com/") || q.Get("code_challenge") == "" {
		t.Fatalf("bad auth params: %v", q)
	}
	code := idp.IssueCode("reader", q.Get("nonce"), q.Get("code_challenge"))
	w := callback(srv, c, q.Get("state"), code)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/ui/events" {
		t.Fatalf("callback: want 303 -> /ui/events, got %d %s", w.Code, w.Header().Get("Location"))
	}
	var sess *http.Cookie
	for _, ck := range w.Result().Cookies() {
		if ck.Name == "nr_session" && ck.Value != "" {
			sess = ck
		}
	}
	if sess == nil {
		t.Fatal("no session cookie issued")
	}
	// The session works against a protected UI page.
	req := httptest.NewRequest("GET", "/ui/events", nil)
	req.AddCookie(sess)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("events page: want 200, got %d", rec.Code)
	}

	// State is single-use: replaying the callback fails.
	if w := callback(srv, c, q.Get("state"), idp.IssueCode("reader", q.Get("nonce"), q.Get("code_challenge"))); w.Code == http.StatusSeeOther {
		t.Fatal("replayed state must not log in")
	}
}

func TestUIOAuth_Rejections(t *testing.T) {
	srv, idp := newOAuthServer(t)

	t.Run("no state cookie (login CSRF)", func(t *testing.T) {
		_, q := startLogin(t, srv)
		code := idp.IssueCode("reader", q.Get("nonce"), q.Get("code_challenge"))
		if w := callback(srv, nil, q.Get("state"), code); w.Code == http.StatusSeeOther {
			t.Fatal("must not log in without the state cookie")
		}
	})
	t.Run("state cookie mismatch", func(t *testing.T) {
		c, q := startLogin(t, srv)
		code := idp.IssueCode("reader", q.Get("nonce"), q.Get("code_challenge"))
		bad := *c
		bad.Value = "forged"
		if w := callback(srv, &bad, q.Get("state"), code); w.Code == http.StatusSeeOther {
			t.Fatal("must not log in with mismatched cookie")
		}
	})
	t.Run("authenticated but no LDAP role", func(t *testing.T) {
		c, q := startLogin(t, srv)
		code := idp.IssueCode("norole", q.Get("nonce"), q.Get("code_challenge"))
		w := callback(srv, c, q.Get("state"), code)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "not authorized") {
			t.Fatalf("want 200 error page, got %d: %s", w.Code, w.Body)
		}
		for _, ck := range w.Result().Cookies() {
			if ck.Name == "nr_session" && ck.Value != "" {
				t.Fatal("no session may be issued")
			}
		}
	})
	t.Run("provider error", func(t *testing.T) {
		c, q := startLogin(t, srv)
		req := httptest.NewRequest("GET", "/ui/oauth/callback?error=access_denied&state="+q.Get("state"), nil)
		req.AddCookie(c)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code == http.StatusSeeOther {
			t.Fatal("must not log in on provider error")
		}
	})
}

func TestUIOAuth_DisabledAndLoginButton(t *testing.T) {
	off, _ := newTestServer(t, publisherAuth())
	for _, p := range []string{"/ui/oauth/login", "/ui/oauth/callback"} {
		w := httptest.NewRecorder()
		off.Handler().ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s disabled: want 404, got %d", p, w.Code)
		}
	}
	w := httptest.NewRecorder()
	off.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/ui/login", nil))
	if strings.Contains(w.Body.String(), "oauth/login") {
		t.Error("SSO button must be hidden when OAuth is off")
	}

	on, _ := newOAuthServer(t)
	w = httptest.NewRecorder()
	on.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/ui/login", nil))
	if !strings.Contains(w.Body.String(), "/ui/oauth/login") || !strings.Contains(w.Body.String(), "Corporate SSO") {
		t.Errorf("SSO button missing: %s", w.Body)
	}
}

func TestBearer_EmailIdentityUsesCanonicalUsername(t *testing.T) {
	srv, idp := newOAuthServer(t)
	tok := idp.Sign(map[string]any{"aud": "relay", "sub": "carol@example.com"})
	// carol maps to publisher via LDAP, so the publish-only listing works...
	if w := bearerGet(srv, "/api/v1/events", tok); w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
}
