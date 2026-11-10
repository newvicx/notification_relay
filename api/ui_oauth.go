package api

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"sync"
	"time"

	"golang.org/x/oauth2"

	ldap "notification_relay/ldap"
	"notification_relay/oauth"
)

const (
	oauthStateCookieName = "nr_oauth_state"
	oauthStateTTL        = 10 * time.Minute
)

// SetOAuth enables OAuth/OIDC authentication. provider validates tokens and
// runs the login flow; groups resolves the authenticated username to LDAP
// groups so authorization stays with Active Directory. Call before Start.
func (s *Server) SetOAuth(provider oauth.Provider, groups ldap.GroupLookup) {
	s.oauth = provider
	s.groupLookup = groups
}

// oauthPending is the server-side half of one in-flight login attempt.
type oauthPending struct {
	nonce     string
	verifier  string
	expiresAt time.Time
}

// oauthStateStore holds in-flight logins keyed by the OAuth "state" value.
// Entries are single-use and short-lived; like uiSessionStore it is in-memory.
type oauthStateStore struct {
	mu      sync.Mutex
	pending map[string]*oauthPending
}

func newOAuthStateStore() *oauthStateStore {
	return &oauthStateStore{pending: make(map[string]*oauthPending)}
}

func (st *oauthStateStore) put(state string, p *oauthPending) {
	st.mu.Lock()
	defer st.mu.Unlock()
	now := time.Now()
	for k, v := range st.pending { // prune abandoned logins
		if now.After(v.expiresAt) {
			delete(st.pending, k)
		}
	}
	st.pending[state] = p
}

// take removes and returns the entry (single use), if present and unexpired.
func (st *oauthStateStore) take(state string) (*oauthPending, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	p, ok := st.pending[state]
	delete(st.pending, state)
	if !ok || time.Now().After(p.expiresAt) {
		return nil, false
	}
	return p, true
}

func (s *Server) oauthLoginFailed(w http.ResponseWriter, msg string) {
	s.renderUIPage(w, "login.html", uiPageData{
		Title: "Sign in", Flash: msg, FlashClass: "error",
		Data: s.loginData(),
	})
}

// loginData tells login.html whether to show the SSO button.
func (s *Server) loginData() map[string]any {
	d := map[string]any{"OAuth": s.oauth != nil}
	if s.oauth != nil {
		d["ProviderName"] = s.oauth.ProviderName()
	}
	return d
}

// handleUIOAuthLogin starts the authorization-code + PKCE flow by redirecting
// the browser to the identity provider.
func (s *Server) handleUIOAuthLogin(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		http.NotFound(w, r)
		return
	}
	state, err1 := newSessionToken()
	nonce, err2 := newSessionToken()
	if err := errors.Join(err1, err2); err != nil {
		s.logger.Error("ui oauth: random generation failed", "error", err)
		s.oauthLoginFailed(w, "Could not start sign-in. Please try again.")
		return
	}
	verifier := oauth2.GenerateVerifier()

	authURL, err := s.oauth.AuthCodeURL(r.Context(), state, nonce, verifier)
	if err != nil {
		s.logger.Error("ui oauth: build authorization url failed", "error", err)
		s.oauthLoginFailed(w, "Single sign-on is currently unavailable. Please try again later.")
		return
	}
	s.oauthStates.put(state, &oauthPending{nonce: nonce, verifier: verifier, expiresAt: time.Now().Add(oauthStateTTL)})
	// The cookie binds the flow to this browser, so a callback URL planted by
	// an attacker (login CSRF) does not match.
	http.SetCookie(w, &http.Cookie{
		Name: oauthStateCookieName, Value: state, Path: "/",
		HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode,
		MaxAge: int(oauthStateTTL.Seconds()),
	})
	http.Redirect(w, r, authURL, http.StatusFound)
}

// handleUIOAuthCallback completes the flow: validates state, exchanges the
// code, verifies the ID token, then resolves roles from LDAP and starts the
// same cookie session used by password login.
func (s *Server) handleUIOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		http.NotFound(w, r)
		return
	}
	ip := clientIP(r)
	q := r.URL.Query()

	state := q.Get("state")
	cookie, cerr := r.Cookie(oauthStateCookieName)
	s.clearOAuthStateCookie(w, r)
	if cerr != nil || state == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(state)) != 1 {
		s.auditLog(r.Context(), "", ip, "ui_oauth_login_failed", "auth", "", "")
		s.oauthLoginFailed(w, "Sign-in session expired or invalid. Please try again.")
		return
	}
	pending, ok := s.oauthStates.take(state)
	if !ok {
		s.auditLog(r.Context(), "", ip, "ui_oauth_login_failed", "auth", "", "")
		s.oauthLoginFailed(w, "Sign-in session expired or invalid. Please try again.")
		return
	}
	if e := q.Get("error"); e != "" {
		s.logger.Warn("ui oauth: provider returned error", "error", e, "description", q.Get("error_description"))
		s.auditLog(r.Context(), "", ip, "ui_oauth_login_failed", "auth", "", "")
		s.oauthLoginFailed(w, "Sign-in was not completed.")
		return
	}
	code := q.Get("code")
	if code == "" {
		s.oauthLoginFailed(w, "Sign-in was not completed.")
		return
	}

	id, err := s.oauth.Exchange(r.Context(), code, pending.nonce, pending.verifier)
	if err != nil {
		s.logger.Warn("ui oauth: code exchange failed", "error", err)
		s.auditLog(r.Context(), "", ip, "ui_oauth_login_failed", "auth", "", "")
		s.oauthLoginFailed(w, "Sign-in failed. Please try again.")
		return
	}

	user, status := s.userFromIdentity(r, id)
	switch status {
	case identityOK:
	case identityNoRole:
		s.auditLog(r.Context(), id.Username, ip, "ui_login_failed_no_role", "auth", "", "")
		s.oauthLoginFailed(w, "Your account is not authorized to use this application.")
		return
	case identityNotFound:
		s.auditLog(r.Context(), id.Username, ip, "ui_oauth_login_failed", "auth", "", "")
		s.oauthLoginFailed(w, "Your account is not authorized to use this application.")
		return
	default:
		s.oauthLoginFailed(w, "Authentication error. Please try again later.")
		return
	}
	if !hasPermission(user.Roles, PermRead) {
		s.auditLog(r.Context(), id.Username, ip, "ui_login_failed_no_role", "auth", "", "")
		s.oauthLoginFailed(w, "Your account is not authorized to use this application.")
		return
	}

	token, err := s.uiSessions.create(user)
	if err != nil {
		s.logger.Error("ui oauth: session create failed", "username", id.Username, "error", err)
		s.oauthLoginFailed(w, "Could not start a session. Please try again.")
		return
	}
	s.auditLog(r.Context(), id.Username, ip, "ui_oauth_login", "auth", "", "")
	s.setSessionCookie(w, r, token)
	http.Redirect(w, r, s.url("/ui/events"), http.StatusSeeOther)
}

func (s *Server) clearOAuthStateCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: oauthStateCookieName, Value: "", Path: "/",
		HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

type identityStatus int

const (
	identityOK identityStatus = iota
	identityNotFound
	identityNoRole
	identityError
)

// userFromIdentity maps a token-verified identity to an authorized *User by
// looking the username up in LDAP. Authentication came from the token;
// authorization (roles) comes only from LDAP group membership.
func (s *Server) userFromIdentity(r *http.Request, id *oauth.Identity) (*User, identityStatus) {
	if s.groupLookup == nil {
		s.logger.Error("oauth enabled but no LDAP group lookup configured")
		return nil, identityError
	}
	res, err := s.groupLookup.LookupGroups(r.Context(), id.Username)
	if err != nil {
		if errors.Is(err, ldap.ErrUserNotFound) {
			return nil, identityNotFound
		}
		s.logger.Error("oauth: ldap group lookup failed", "username", id.Username, "error", err)
		return nil, identityError
	}
	roles := resolveRoles(res.Groups, s.roleConfig)
	if len(roles) == 0 {
		return nil, identityNoRole
	}
	return &User{Username: id.Username, DN: res.UserDN, Roles: roles}, identityOK
}
