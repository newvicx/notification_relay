package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"notification_relay/db"
	ldap "notification_relay/ldap"
	"notification_relay/oauth"
)

// authenticate is middleware that verifies HTTP Basic Auth credentials against
// LDAP, resolves the user's roles, and stores the authenticated User in the
// request context for downstream handlers and middleware.
//
// Failed login attempts are written to the audit log; successful ones are
// not, to avoid flooding the audit log with routine activity. Requests
// without an Authorization header are rejected with 401 but not audited —
// no username is present to associate with an audit entry.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if raw, isBearer := bearerToken(r); isBearer {
			s.authenticateBearer(w, r, next, raw)
			return
		}

		username, password, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", s.challenge())
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}

		ip := clientIP(r)

		result, err := s.auth.AuthenticateUser(r.Context(), username, password)
		if err != nil {
			s.auditLog(r.Context(), username, ip, "login_failed", "auth", "", "")
			if errors.Is(err, ldap.ErrInvalidCredentials) {
				w.Header().Set("WWW-Authenticate", s.challenge())
				http.Error(w, "invalid credentials", http.StatusUnauthorized)
				return
			}
			s.logger.Error("ldap authentication error", "error", err, "username", username, "ip", ip)
			http.Error(w, "authentication error", http.StatusInternalServerError)
			return
		}

		roles := resolveRoles(result.Groups, s.roleConfig)
		user := &User{Username: username, DN: result.UserDN, Roles: roles}

		ctx := context.WithValue(r.Context(), ctxKey{}, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requirePermissions is middleware that enforces RBAC on an already-authenticated
// request. It must be chained after authenticate. If the User stored in context
// does not hold all of the required permissions, the request is rejected with 403.
func (s *Server) requirePermissions(perms ...Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := UserFromContext(r.Context())
			if !ok {
				// Misconfigured route — authenticate middleware was not applied.
				s.logger.Error("requirePermissions called without authenticate middleware")
				http.Error(w, "authentication required", http.StatusUnauthorized)
				return
			}
			for _, perm := range perms {
				if !hasPermission(user.Roles, perm) {
					http.Error(w, "forbidden", http.StatusForbidden)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// auditLogAction writes a data-mutation audit entry for an already-authenticated
// handler. Username and IP are extracted from the request automatically.
// oldJSON and newJSON are JSON-serialised snapshots of the record before and after
// the mutation; either may be empty (stored as NULL).
func (s *Server) auditLogAction(r *http.Request, action, impactedTable, oldJSON, newJSON string) {
	user, _ := UserFromContext(r.Context())
	ip := clientIP(r)
	err := s.q.InsertAuditLog(r.Context(), db.InsertAuditLogParams{
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		Username:      user.Username,
		IpAddress:     sql.NullString{String: ip, Valid: ip != ""},
		Action:        action,
		ImpactedTable: impactedTable,
		OldValues:     sql.NullString{String: oldJSON, Valid: oldJSON != ""},
		NewValues:     sql.NullString{String: newJSON, Valid: newJSON != ""},
	})
	if err != nil {
		s.logger.Error("failed to write audit log",
			"action", action,
			"table", impactedTable,
			"error", err,
		)
	}
}

// marshalAuditJSON serialises v to a compact JSON string for audit log snapshots.
// Returns "" on error — the audit entry is still written with a NULL column.
func marshalAuditJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// auditLog writes a single audit entry to the database. Errors are logged but
// do not affect the HTTP response — audit failures must not block requests.
func (s *Server) auditLog(ctx context.Context, username, ip, action, impactedTable, oldJSON, newJSON string) {
	err := s.q.InsertAuditLog(ctx, db.InsertAuditLogParams{
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		Username:      username,
		IpAddress:     sql.NullString{String: ip, Valid: ip != ""},
		Action:        action,
		ImpactedTable: impactedTable,
		OldValues:     sql.NullString{String: oldJSON, Valid: oldJSON != ""},
		NewValues:     sql.NullString{String: newJSON, Valid: newJSON != ""},
	})
	if err != nil {
		s.logger.Error("failed to write audit log",
			"action", action,
			"username", username,
			"error", err,
		)
	}
}

// clientIP extracts the client's IP address from the X-Client-IP header set
// by the reverse proxy. Falls back to an empty string if the header is absent.
func clientIP(r *http.Request) string {
	return r.Header.Get("X-Client-IP")
}

// challenge returns the WWW-Authenticate value, advertising Bearer when OAuth
// is enabled.
func (s *Server) challenge() string {
	if s.oauth != nil {
		return `Basic realm="notification-relay", Bearer realm="notification-relay"`
	}
	return `Basic realm="notification-relay"`
}

// bearerToken extracts a token from "Authorization: Bearer <token>".
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(h[len(prefix):]), true
}

// authenticateBearer validates a JWT from the OAuth provider (authentication)
// and resolves roles from LDAP group membership (authorization).
func (s *Server) authenticateBearer(w http.ResponseWriter, r *http.Request, next http.Handler, raw string) {
	if s.oauth == nil {
		w.Header().Set("WWW-Authenticate", s.challenge())
		http.Error(w, "bearer authentication is not enabled", http.StatusUnauthorized)
		return
	}
	ip := clientIP(r)
	id, err := s.oauth.VerifyBearer(r.Context(), raw)
	if err != nil {
		if errors.Is(err, oauth.ErrInvalidToken) {
			s.auditLog(r.Context(), "", ip, "bearer_login_failed", "auth", "", "")
			w.Header().Set("WWW-Authenticate", `Bearer realm="notification-relay", error="invalid_token"`)
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		s.logger.Error("oauth token verification error", "error", err, "ip", ip)
		http.Error(w, "authentication error", http.StatusInternalServerError)
		return
	}
	user, status := s.userFromIdentity(r, id)
	switch status {
	case identityOK:
	case identityNotFound, identityNoRole:
		// Valid token, but no LDAP account/roles: authenticated, not authorized.
		s.auditLog(r.Context(), id.Username, ip, "bearer_login_failed_no_role", "auth", "", "")
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	default:
		http.Error(w, "authentication error", http.StatusInternalServerError)
		return
	}
	next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, user)))
}
