package api

import (
	"errors"
	"net/http"

	ldap "notification_relay/ldap"
)

func (s *Server) handleUILoginForm(w http.ResponseWriter, r *http.Request) {
	// Already signed in — skip the form.
	if cookie, err := r.Cookie(uiSessionCookieName); err == nil {
		if _, ok := s.uiSessions.get(cookie.Value); ok {
			http.Redirect(w, r, "/ui/events", http.StatusSeeOther)
			return
		}
	}
	s.renderUIPage(w, "login.html", uiPageData{Title: "Sign in"})
}

func (s *Server) handleUILoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderUIPage(w, "login.html", uiPageData{Title: "Sign in", Flash: "Invalid form submission.", FlashClass: "error"})
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")
	ip := clientIP(r)

	if username == "" || password == "" {
		s.renderUIPage(w, "login.html", uiPageData{Title: "Sign in", Flash: "Username and password are required.", FlashClass: "error"})
		return
	}

	result, err := s.auth.AuthenticateUser(r.Context(), username, password)
	if err != nil {
		s.auditLog(r.Context(), username, ip, "ui_login_failed", "auth", "", "")
		if errors.Is(err, ldap.ErrInvalidCredentials) {
			s.renderUIPage(w, "login.html", uiPageData{Title: "Sign in", Flash: "Invalid credentials.", FlashClass: "error"})
			return
		}
		s.logger.Error("ui login: ldap auth error", "username", username, "error", err)
		s.renderUIPage(w, "login.html", uiPageData{Title: "Sign in", Flash: "Authentication error. Please try again later.", FlashClass: "error"})
		return
	}

	roles := resolveRoles(result.Groups, s.roleConfig)
	if !hasPermission(roles, PermRead) {
		s.auditLog(r.Context(), username, ip, "ui_login_failed_no_role", "auth", "", "")
		s.renderUIPage(w, "login.html", uiPageData{Title: "Sign in", Flash: "Your account is not authorized to use this application.", FlashClass: "error"})
		return
	}
	user := &User{Username: username, DN: result.UserDN, Roles: roles}

	token, err := s.uiSessions.create(user)
	if err != nil {
		s.logger.Error("ui login: session create failed", "username", username, "error", err)
		s.renderUIPage(w, "login.html", uiPageData{Title: "Sign in", Flash: "Could not start a session. Please try again.", FlashClass: "error"})
		return
	}

	s.auditLog(r.Context(), username, ip, "ui_login", "auth", "", "")
	s.setSessionCookie(w, r, token)
	http.Redirect(w, r, "/ui/events", http.StatusSeeOther)
}

func (s *Server) handleUILogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(uiSessionCookieName); err == nil {
		s.uiSessions.delete(cookie.Value)
	}
	s.clearSessionCookie(w, r)
	http.Redirect(w, r, "/ui/login", http.StatusSeeOther)
}
