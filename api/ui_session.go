package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sync"
	"time"
)

const (
	uiSessionCookieName = "nr_session"
	uiSessionTTL        = 8 * time.Hour
	uiSessionSweepEvery = 10 * time.Minute
)

// uiSession is a server-side record backing a browser session cookie. Sessions
// are held in memory only — a process restart logs everyone out, which is an
// acceptable tradeoff for a single-process app that already centralizes all
// state in one SQLite writer.
type uiSession struct {
	user      *User
	expiresAt time.Time
}

// uiSessionStore is an in-memory session store guarded by a mutex.
type uiSessionStore struct {
	mu       sync.Mutex
	sessions map[string]*uiSession
}

func newUISessionStore() *uiSessionStore {
	return &uiSessionStore{sessions: make(map[string]*uiSession)}
}

func newSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (st *uiSessionStore) create(user *User) (string, error) {
	token, err := newSessionToken()
	if err != nil {
		return "", err
	}
	st.mu.Lock()
	st.sessions[token] = &uiSession{user: user, expiresAt: time.Now().Add(uiSessionTTL)}
	st.mu.Unlock()
	return token, nil
}

// get returns the session's user, touching its expiry (sliding window) if
// found and not expired.
func (st *uiSessionStore) get(token string) (*User, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	sess, ok := st.sessions[token]
	if !ok {
		return nil, false
	}
	if time.Now().After(sess.expiresAt) {
		delete(st.sessions, token)
		return nil, false
	}
	sess.expiresAt = time.Now().Add(uiSessionTTL)
	return sess.user, true
}

func (st *uiSessionStore) delete(token string) {
	st.mu.Lock()
	delete(st.sessions, token)
	st.mu.Unlock()
}

// sweepExpired periodically removes expired sessions until ctx is cancelled.
func (st *uiSessionStore) sweepExpired(ctx context.Context) {
	ticker := time.NewTicker(uiSessionSweepEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			st.mu.Lock()
			for token, sess := range st.sessions {
				if now.After(sess.expiresAt) {
					delete(st.sessions, token)
				}
			}
			st.mu.Unlock()
		}
	}
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     uiSessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(uiSessionTTL.Seconds()),
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     uiSessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// authenticateSession is UI middleware analogous to authenticate, but backed
// by a session cookie instead of HTTP Basic Auth. On success it stores the
// same *User type under the same ctxKey{} used by authenticate, so
// requirePermissions, UserFromContext, and auditLogAction work unchanged for
// UI routes.
func (s *Server) authenticateSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(uiSessionCookieName)
		if err != nil {
			http.Redirect(w, r, "/ui/login", http.StatusSeeOther)
			return
		}
		user, ok := s.uiSessions.get(cookie.Value)
		if !ok {
			http.Redirect(w, r, "/ui/login", http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
