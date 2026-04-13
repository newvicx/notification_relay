package notify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func mockTwilioServer(t *testing.T, statusCode int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ---- TwilioSMS.Send ----

func TestTwilioSMS_Send_Success(t *testing.T) {
	srv := mockTwilioServer(t, 201, `{"sid":"SM123","status":"queued"}`)

	sms := &TwilioSMS{
		accountSID: "ACtest",
		tokenSID:   "SKtest",
		authToken:  "token",
		fromNumber: "+15550000000",
		baseURL:    srv.URL,
		client:     &http.Client{Timeout: 5 * time.Second},
	}

	sid, status, err := sms.Send("+15551111111", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sid != "SM123" {
		t.Errorf("want sid=SM123, got %q", sid)
	}
	if status != "queued" {
		t.Errorf("want status=queued, got %q", status)
	}
}

func TestTwilioSMS_Send_HTTPError(t *testing.T) {
	srv := mockTwilioServer(t, 429, `{"code":20429,"message":"too many requests"}`)

	sms := &TwilioSMS{
		accountSID: "ACtest",
		tokenSID:   "SKtest",
		authToken:  "token",
		fromNumber: "+15550000000",
		baseURL:    srv.URL,
		client:     &http.Client{Timeout: 5 * time.Second},
	}

	_, _, err := sms.Send("+15551111111", "hello")
	if err == nil {
		t.Fatal("want error for 429, got nil")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error should mention status code 429, got: %v", err)
	}
}

func TestTwilioSMS_Send_BadJSON(t *testing.T) {
	srv := mockTwilioServer(t, 201, `not json`)

	sms := &TwilioSMS{
		accountSID: "ACtest",
		tokenSID:   "SKtest",
		authToken:  "token",
		fromNumber: "+15550000000",
		baseURL:    srv.URL,
		client:     &http.Client{Timeout: 5 * time.Second},
	}

	_, _, err := sms.Send("+15551111111", "hello")
	if err == nil {
		t.Fatal("want parse error, got nil")
	}
}

// ---- TwilioVoice.Call ----

func TestTwilioVoice_Call_Success(t *testing.T) {
	var capturedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		capturedBody = r.FormValue("Twiml")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]any{"sid": "CA123", "status": "queued"})
	}))
	t.Cleanup(srv.Close)

	voice := &TwilioVoice{
		accountSID: "ACtest",
		authToken:  "token",
		fromNumber: "+15550000000",
		baseURL:    srv.URL,
		client:     &http.Client{Timeout: 5 * time.Second},
	}

	sid, status, err := voice.Call("+15551111111", "fire alarm")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sid != "CA123" {
		t.Errorf("want sid=CA123, got %q", sid)
	}
	if status != "queued" {
		t.Errorf("want status=queued, got %q", status)
	}
	if !strings.Contains(capturedBody, "fire alarm") {
		t.Errorf("TwiML should contain message text, got: %s", capturedBody)
	}
}

func TestTwilioVoice_Call_HTTPError(t *testing.T) {
	srv := mockTwilioServer(t, 400, `{"code":21211,"message":"invalid to number"}`)

	voice := &TwilioVoice{
		accountSID: "ACtest",
		authToken:  "token",
		fromNumber: "+15550000000",
		baseURL:    srv.URL,
		client:     &http.Client{Timeout: 5 * time.Second},
	}

	_, _, err := voice.Call("not-a-number", "alert")
	if err == nil {
		t.Fatal("want error for 400, got nil")
	}
}

// ---- mapTwilioStatus (pure) ----

func TestMapTwilioStatus(t *testing.T) {
	tests := []struct {
		channel      string
		twilioStatus string
		wantStatus   string
		wantTerminal bool
	}{
		// Voice terminal
		{"voice", "completed", "completed", true},
		{"voice", "failed", "failed", true},
		{"voice", "busy", "busy", true},
		{"voice", "no-answer", "no-answer", true},
		{"voice", "canceled", "canceled", true},
		// Voice non-terminal
		{"voice", "queued", "queued", false},
		{"voice", "ringing", "ringing", false},
		{"voice", "in-progress", "in-progress", false},
		// SMS terminal
		{"sms", "delivered", "delivered", true},
		{"sms", "undelivered", "undelivered", true},
		{"sms", "failed", "failed", true},
		// SMS non-terminal
		{"sms", "queued", "queued", false},
		{"sms", "sent", "sent", false},
		{"sms", "sending", "sending", false},
		{"sms", "accepted", "accepted", false},
	}

	for _, tc := range tests {
		status, terminal := mapTwilioStatus(tc.channel, tc.twilioStatus)
		if status != tc.wantStatus {
			t.Errorf("mapTwilioStatus(%q,%q) status=%q, want %q", tc.channel, tc.twilioStatus, status, tc.wantStatus)
		}
		if terminal != tc.wantTerminal {
			t.Errorf("mapTwilioStatus(%q,%q) terminal=%v, want %v", tc.channel, tc.twilioStatus, terminal, tc.wantTerminal)
		}
	}
}
