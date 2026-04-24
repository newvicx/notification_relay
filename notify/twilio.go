package notify

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"notification_relay/config"
)

// uuidV7 returns a UUID v7 string: time-ordered (48-bit Unix ms prefix) with
// random bits for the remainder. No external dependency; uses crypto/rand.
func uuidV7() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("notify: crypto/rand unavailable: " + err.Error())
	}

	// Overwrite the first 6 bytes with the current Unix millisecond timestamp.
	ms := uint64(time.Now().UnixMilli())
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)

	// Set version 7 in the top nibble of byte 6.
	b[6] = (b[6] & 0x0f) | 0x70
	// Set variant bits (10xx) in byte 8.
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		b[0:4],
		b[4:6],
		b[6:8],
		b[8:10],
		b[10:16],
	)
}

// twilioPost performs a form-encoded POST to the Twilio REST API with HTTP Basic
// auth and returns the raw JSON response body.
func twilioPost(client *http.Client, tokenSID, authToken, apiURL string, form url.Values) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, apiURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(tokenSID, authToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Include the Twilio error body in the returned error so callers can log it.
		return nil, fmt.Errorf("twilio %s: status %d: %s", apiURL, resp.StatusCode, body)
	}
	return body, nil
}

// twilioGet performs an authenticated GET to the Twilio REST API and returns
// the raw JSON response body.
func twilioGet(client *http.Client, tokenSID, authToken, apiURL string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(tokenSID, authToken)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("twilio GET %s: status %d: %s", apiURL, resp.StatusCode, body)
	}
	return body, nil
}

// twilioResponse holds the common fields returned by Twilio for both Messages
// and Calls resources.
type twilioResponse struct {
	Sid          string `json:"sid"`
	Status       string `json:"status"`
	ErrorCode    *int   `json:"error_code"`
	ErrorMessage string `json:"error_message"`
}

// TwilioSMS implements SMSProvider using the Twilio Messages REST API.
type TwilioSMS struct {
	accountSID string
	tokenSID   string
	authToken  string
	fromNumber string
	baseURL    string
	client     *http.Client
}

// NewTwilioSMS constructs a TwilioSMS provider from the given config.
func NewTwilioSMS(cfg config.TwilioConfig) *TwilioSMS {
	return &TwilioSMS{
		accountSID: cfg.AccountSID,
		tokenSID:   cfg.TokenSID,
		authToken:  cfg.AuthToken,
		fromNumber: cfg.FromNumber,
		baseURL:    "https://api.twilio.com",
		client:     &http.Client{Timeout: 30 * time.Second},
	}
}

// Send sends an SMS to `to` with the given `message` text.
// It returns the Twilio message SID, the initial status from Twilio, and any error.
func (t *TwilioSMS) Send(to, message string) (sid, status string, err error) {
	apiURL := fmt.Sprintf(
		"%s/2010-04-01/Accounts/%s/Messages.json",
		t.baseURL, t.accountSID,
	)
	form := url.Values{
		"To":   {to},
		"From": {t.fromNumber},
		"Body": {message},
	}
	body, err := twilioPost(t.client, t.tokenSID, t.authToken, apiURL, form)
	if err != nil {
		return "", "", err
	}
	var r twilioResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return "", "", fmt.Errorf("twilio sms: parse response: %w", err)
	}
	return r.Sid, r.Status, nil
}

// TwilioVoice implements VoiceProvider using the Twilio Calls REST API.
// The message is delivered via TwiML <Say> with Twilio's text-to-speech engine.
type TwilioVoice struct {
	accountSID string
	tokenSID   string
	authToken  string
	fromNumber string
	baseURL    string
	client     *http.Client
}

// NewTwilioVoice constructs a TwilioVoice provider from the given config.
func NewTwilioVoice(cfg config.TwilioConfig, timeout time.Duration) *TwilioVoice {
	return &TwilioVoice{
		accountSID: cfg.AccountSID,
		tokenSID:   cfg.TokenSID,
		authToken:  cfg.AuthToken,
		fromNumber: cfg.FromNumber,
		baseURL:    "https://api.twilio.com",
		client:     &http.Client{Timeout: timeout},
	}
}

// Call initiates a voice call to `to` that reads `message` aloud via TTS.
// It returns the Twilio call SID, the initial status from Twilio, and any error.
func (t *TwilioVoice) Call(to, message string) (sid, status string, err error) {
	apiURL := fmt.Sprintf(
		"%s/2010-04-01/Accounts/%s/Calls.json",
		t.baseURL, t.accountSID,
	)
	twiml := fmt.Sprintf(
		`<Response>
		<!-- Wait for 1 second -->
		<Pause length="1" />
		
		<!-- Deliver the message -->
		<Say>%s</Say>
		
		<!-- Wait for 1 second -->
		<Pause length="1" />
		
		<!-- Deliver the message again -->
		<Say>%s</Say>
	</Response>`,
		message, message)
	form := url.Values{
		"To":    {to},
		"From":  {t.fromNumber},
		"Twiml": {twiml},
	}
	body, err := twilioPost(t.client, t.tokenSID, t.authToken, apiURL, form)
	if err != nil {
		return "", "", err
	}
	var r twilioResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return "", "", fmt.Errorf("twilio voice: parse response: %w", err)
	}
	return r.Sid, r.Status, nil
}
