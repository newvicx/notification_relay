package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"time"

	"notification_relay/db"
	ldap "notification_relay/ldap"
	"notification_relay/notify"
)

// subscribeWelcomeMsg is sent to new subscribers via SMS.
const subscribeWelcomeMsg = "You've been registered for SMS alerts. " +
	"Msg frequency varies. Msg & data rates may apply."

var subscribeFormTmpl = template.Must(template.New("subscribe").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Register for SMS Notifications</title>
<style>
  body { font-family: sans-serif; max-width: 480px; margin: 60px auto; padding: 0 16px; color: #222; }
  h1 { font-size: 1.4rem; margin-bottom: 0.25rem; }
  p.desc { color: #555; font-size: 0.95rem; margin-top: 0; }
  label { display: block; margin-top: 1rem; font-size: 0.9rem; font-weight: 600; }
  input { width: 100%; box-sizing: border-box; padding: 8px; margin-top: 4px; border: 1px solid #ccc; border-radius: 4px; font-size: 1rem; }
  button { margin-top: 1.25rem; width: 100%; padding: 10px; background: #1a6fc4; color: #fff; border: none; border-radius: 4px; font-size: 1rem; cursor: pointer; }
  button:hover { background: #155aa0; }
  .footer { margin-top: 2rem; font-size: 0.8rem; color: #888; }
  .msg { padding: 12px; border-radius: 4px; margin-bottom: 1rem; font-size: 0.95rem; }
  .error { background: #fde8e8; color: #b00; border: 1px solid #f5c6c6; }
  .info  { background: #e8f4fd; color: #155; border: 1px solid #b3d7f0; }
  .ok    { background: #e8fde8; color: #050; border: 1px solid #b3f0b3; }
  .container {
    display: flex;
    justify-content: center; /* Centers horizontally */
    align-items: center;     /* Centers vertically */
    height: 50px;           /* Required for vertical centering */
  }
</style>
</head>
<body>
<h1>Register for SMS Notifications</h1>
<p class="desc">Enter your company credentials to opt in to SMS alerts for process alarms sent to your company mobile phone.</p>
{{if .Flash}}<div class="msg {{.FlashClass}}">{{.Flash}}</div>{{end}}
{{if .Done}}{{else}}
<form method="POST" action="/subscribe">
  <label for="username">Username</label>
  <input type="text" id="username" name="username" autocomplete="username" required>
  <label for="password">Password</label>
  <input type="password" id="password" name="password" autocomplete="current-password" required>
  <button type="submit">Register</button>
</form>
{{end}}
<div class="footer">
  <p class="desc">By entering your credentials you consent to allow AbbVie to send you alarms and alerts to your company issued mobile phone number.</p>
  <div class="container">
	<img src="assets/logo.png" alt="AbbVie Logo" width="120" height="90">
  </div>
  Message frequency varies &bull; Msg &amp; data rates may apply &bull; Reply STOP to opt out &bull;
  <a href="/unsubscribe">Unsubscribe</a>
</div>
</body>
</html>
`))

var unsubscribeFormTmpl = template.Must(template.New("unsubscribe").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Unsubscribe from SMS Notifications</title>
<style>
  body { font-family: sans-serif; max-width: 480px; margin: 60px auto; padding: 0 16px; color: #222; }
  h1 { font-size: 1.4rem; margin-bottom: 0.25rem; }
  p.desc { color: #555; font-size: 0.95rem; margin-top: 0; }
  label { display: block; margin-top: 1rem; font-size: 0.9rem; font-weight: 600; }
  input { width: 100%; box-sizing: border-box; padding: 8px; margin-top: 4px; border: 1px solid #ccc; border-radius: 4px; font-size: 1rem; }
  button { margin-top: 1.25rem; width: 100%; padding: 10px; background: #c43a1a; color: #fff; border: none; border-radius: 4px; font-size: 1rem; cursor: pointer; }
  button:hover { background: #a03015; }
  .footer { margin-top: 2rem; font-size: 0.8rem; color: #888; }
  .msg { padding: 12px; border-radius: 4px; margin-bottom: 1rem; font-size: 0.95rem; }
  .error { background: #fde8e8; color: #b00; border: 1px solid #f5c6c6; }
  .info  { background: #e8f4fd; color: #155; border: 1px solid #b3d7f0; }
  .ok    { background: #e8fde8; color: #050; border: 1px solid #b3f0b3; }
</style>
</head>
<body>
<h1>Unsubscribe from SMS Notifications</h1>
<p class="desc">Enter your company credentials to remove yourself from SMS alerts.</p>
{{if .Flash}}<div class="msg {{.FlashClass}}">{{.Flash}}</div>{{end}}
{{if .Done}}{{else}}
<form method="POST" action="/unsubscribe">
  <label for="username">Username</label>
  <input type="text" id="username" name="username" autocomplete="username" required>
  <label for="password">Password</label>
  <input type="password" id="password" name="password" autocomplete="current-password" required>
  <button type="submit">Unsubscribe</button>
</form>
{{end}}
<div class="footer">
  <a href="/subscribe">Subscribe</a>
</div>
</body>
</html>
`))

type formPageData struct {
	Flash      string
	FlashClass string
	Done       bool
}

func (s *Server) handleSubscribeForm(w http.ResponseWriter, r *http.Request) {
	renderPage(w, subscribeFormTmpl, formPageData{})
}

func (s *Server) handleSubscribeSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		renderPage(w, subscribeFormTmpl, formPageData{Flash: "Invalid form submission.", FlashClass: "error"})
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")
	ip := clientIP(r)

	if username == "" || password == "" {
		renderPage(w, subscribeFormTmpl, formPageData{Flash: "Username and password are required.", FlashClass: "error"})
		return
	}

	if _, err := s.auth.AuthenticateUser(r.Context(), username, password); err != nil {
		s.auditLog(r.Context(), username, ip, "sms_subscribe_failed_invalid_credentials", "auth", "", "")
		if errors.Is(err, ldap.ErrInvalidCredentials) {
			renderPage(w, subscribeFormTmpl, formPageData{Flash: "Invalid credentials.", FlashClass: "error"})
			return
		}
		s.logger.Error("subscribe: ldap auth error", "username", username, "error", err)
		renderPage(w, subscribeFormTmpl, formPageData{Flash: "Authentication error. Please try again later.", FlashClass: "error"})
		return
	}

	member, err := s.userLookup.LookupUser(r.Context(), username)
	if err != nil {
		s.logger.Error("subscribe: ldap user lookup failed", "username", username, "error", err)
		renderPage(w, subscribeFormTmpl, formPageData{Flash: "Could not retrieve account details. Please try again later.", FlashClass: "error"})
		return
	}

	if member.Mobile == "" {
		s.auditLog(r.Context(), username, ip, "sms_subscribe_no_phone", "auth", "", "")
		renderPage(w, subscribeFormTmpl, formPageData{
			Flash:      "No company mobile phone number is on record for your account. Contact IT to have your mobile number added before registering.",
			FlashClass: "error",
		})
		return
	}

	if !rePhone.MatchString(member.Mobile) {
		s.logger.Warn("subscribe: mobile not E.164", "username", username, "mobile", member.Mobile)
		renderPage(w, subscribeFormTmpl, formPageData{
			Flash:      "Your company mobile number is not in the expected format. Contact IT to correct your mobile number.",
			FlashClass: "error",
		})
		return
	}

	// Already subscribed — show info, no duplicate insert.
	if _, err := s.q.GetSMSSubscription(r.Context(), username); err == nil {
		renderPage(w, subscribeFormTmpl, formPageData{
			Flash:      "You are already registered for SMS notifications.",
			FlashClass: "info",
			Done:       true,
		})
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.q.InsertSMSSubscription(r.Context(), db.InsertSMSSubscriptionParams{
		Username:     username,
		Phone:        member.Mobile,
		SubscribedAt: now,
	}); err != nil {
		s.logger.Error("subscribe: insert failed", "username", username, "error", err)
		renderPage(w, subscribeFormTmpl, formPageData{Flash: "Registration failed. Please try again later.", FlashClass: "error"})
		return
	}

	s.auditLog(r.Context(), username, ip, "sms_subscribe", "sms_subscriptions", "", marshalAuditJSON(map[string]string{
		"username": username,
		"phone":    member.Mobile,
	}))

	sendWelcomeSMS(r.Context(), s, username, member.Mobile)

	renderPage(w, subscribeFormTmpl, formPageData{
		Flash:      "You have been successfully registered for SMS notifications.",
		FlashClass: "ok",
		Done:       true,
	})
}

func (s *Server) handleUnsubscribeForm(w http.ResponseWriter, r *http.Request) {
	renderPage(w, unsubscribeFormTmpl, formPageData{})
}

func (s *Server) handleUnsubscribeSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		renderPage(w, unsubscribeFormTmpl, formPageData{Flash: "Invalid form submission.", FlashClass: "error"})
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")
	ip := clientIP(r)

	if username == "" || password == "" {
		renderPage(w, unsubscribeFormTmpl, formPageData{Flash: "Username and password are required.", FlashClass: "error"})
		return
	}

	if _, err := s.auth.AuthenticateUser(r.Context(), username, password); err != nil {
		s.auditLog(r.Context(), username, ip, "sms_unsubscribe_failed_invalid_credentials", "auth", "", "")
		if errors.Is(err, ldap.ErrInvalidCredentials) {
			renderPage(w, unsubscribeFormTmpl, formPageData{Flash: "Invalid credentials.", FlashClass: "error"})
			return
		}
		s.logger.Error("unsubscribe: ldap auth error", "username", username, "error", err)
		renderPage(w, unsubscribeFormTmpl, formPageData{Flash: "Authentication error. Please try again later.", FlashClass: "error"})
		return
	}

	existing, err := s.q.GetSMSSubscription(r.Context(), username)
	if err != nil {
		renderPage(w, unsubscribeFormTmpl, formPageData{
			Flash:      "You are not currently registered for SMS notifications.",
			FlashClass: "info",
			Done:       true,
		})
		return
	}

	if err := s.q.DeleteSMSSubscription(r.Context(), username); err != nil {
		s.logger.Error("unsubscribe: delete failed", "username", username, "error", err)
		renderPage(w, unsubscribeFormTmpl, formPageData{Flash: "Unsubscribe failed. Please try again later.", FlashClass: "error"})
		return
	}

	s.auditLog(r.Context(), username, ip, "sms_unsubscribe", "sms_subscriptions", marshalAuditJSON(map[string]string{
		"username": username,
		"phone":    existing.Phone,
	}), "")

	renderPage(w, unsubscribeFormTmpl, formPageData{
		Flash:      "You have been successfully unsubscribed from SMS notifications.",
		FlashClass: "ok",
		Done:       true,
	})
}

// sendWelcomeSMS enqueues a welcome SMS notification for a newly subscribed user.
// The phone must already be in sms_subscriptions so the dispatcher gate passes.
// Errors are logged but do not block the caller.
func sendWelcomeSMS(ctx context.Context, s *Server, username, phone string) {
	now := time.Now().UTC().Format(time.RFC3339)
	welcomeNotifID := newUUIDV7()
	welcomeEventID := newUUIDV7()
	eventName := "SMS Welcome: " + username

	_, _ = s.q.InsertEvent(ctx, db.InsertEventParams{
		EventID:   welcomeEventID,
		EventName: nullString(eventName),
		StartTime: now,
		CreatedAt: now,
		CreatedBy: nullString("system"),
	})

	destsJSON, _ := json.Marshal([]map[string]string{{"channel": "sms", "target": phone}})

	if _, err := s.q.InsertNotification(ctx, db.InsertNotificationParams{
		NotificationID: welcomeNotifID,
		EventID:        welcomeEventID,
		Destinations:   sql.NullString{String: string(destsJSON), Valid: true},
		Message:        subscribeWelcomeMsg,
		Status:         "pending",
		CreatedAt:      now,
		CreatedBy:      nullString("system"),
	}); err != nil {
		s.logger.Warn("subscribe: failed to create welcome notification", "username", username, "error", err)
		return
	}
	select {
	case s.queue <- notify.Job{NotificationID: welcomeNotifID}:
	default:
		s.logger.Warn("subscribe: job queue full, welcome SMS dropped", "username", username)
	}
}

func renderPage(w http.ResponseWriter, tmpl *template.Template, data formPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}
