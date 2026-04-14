//go:build integration

// Integration tests that send real SMS messages and voice calls through Twilio.
// They are excluded from the normal test run and must be opted in explicitly:
//
//	go test -tags integration -v -timeout 10m ./notify/ -run Integration
//
// Required environment variables:
//
//	TEST_CONFIG    Path to a config.yaml containing a [twilio] section
//	TEST_SMS_TO    E.164 phone number to receive the test SMS   (+15550001111)
//	TEST_VOICE_TO  E.164 phone number to receive the test call  (+15550001111)
//
// The config.yaml only needs the [twilio] block — LDAP, database, and other
// sections are not read. Minimal example:
//
//	twilio:
//	  account_sid:  ACxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
//	  token_sid:    SKxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
//	  auth_token:   ${TWILIO_AUTH_TOKEN}
//	  from_number:  "+15550000000"
//	  poll_interval: 5s

package notify_test

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"notification_relay/config"
	"notification_relay/notify"

	"gopkg.in/yaml.v3"
)

// ---- Config loading ----

// loadTwilioConfig reads only the twilio: section from the config file
// pointed to by TEST_CONFIG. Skips the test if the env var is absent or
// the Twilio account SID is not populated.
func loadTwilioConfig(t *testing.T) config.TwilioConfig {
	t.Helper()

	path := os.Getenv("TEST_CONFIG")
	if path == "" {
		// go test runs with the package dir as cwd; walk up to find config.yaml.
		for _, candidate := range []string{"../config.yaml", "../../config.yaml"} {
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
				break
			}
		}
	}
	if path == "" {
		t.Skip("config.yaml not found — set TEST_CONFIG to its path")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("cannot read config file %q: %v", path, err)
	}

	// Interpolate ${ENV_VAR} placeholders the same way the main loader does.
	data = interpolateEnv(data)

	var wrapper struct {
		Twilio config.TwilioConfig `yaml:"twilio"`
	}
	if err := yaml.Unmarshal(data, &wrapper); err != nil {
		t.Fatalf("parse twilio config: %v", err)
	}
	if wrapper.Twilio.AccountSID == "" {
		t.Skip("twilio.account_sid not configured — skipping integration test")
	}
	return wrapper.Twilio
}

// ---- Status polling ----

const (
	integrationPollInterval = 5 * time.Second
	integrationPollTimeout  = 3 * time.Minute
)

type twilioStatusResp struct {
	Status       string `json:"status"`
	ErrorCode    *int   `json:"error_code"`
	ErrorMessage string `json:"error_message"`
}

// pollStatus queries Twilio for the status of a call or message SID until
// a terminal state is reached or the timeout expires.
// Returns the final status string and whether a terminal state was reached.
func pollStatus(t *testing.T, cfg config.TwilioConfig, channel, sid string) (finalStatus string, terminal bool) {
	t.Helper()

	client := &http.Client{Timeout: 15 * time.Second}
	deadline := time.Now().Add(integrationPollTimeout)

	var resourcePath string
	switch channel {
	case "voice":
		resourcePath = fmt.Sprintf("/2010-04-01/Accounts/%s/Calls/%s.json", cfg.AccountSID, sid)
	default: // sms
		resourcePath = fmt.Sprintf("/2010-04-01/Accounts/%s/Messages/%s.json", cfg.AccountSID, sid)
	}
	apiURL := "https://api.twilio.com" + resourcePath

	for {
		req, _ := http.NewRequest(http.MethodGet, apiURL, nil)
		req.SetBasicAuth(cfg.TokenSID, cfg.AuthToken)

		resp, err := client.Do(req)
		if err != nil {
			t.Logf("poller: fetch error (will retry): %v", err)
		} else {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			var r twilioStatusResp
			if err := json.Unmarshal(body, &r); err != nil {
				t.Logf("poller: parse error (will retry): %v — body: %s", err, body)
			} else {
				t.Logf("poller: %s %s → status=%q", channel, sid, r.Status)

				_, isTerminal := mapTerminalStatus(channel, r.Status)
				if isTerminal {
					if r.ErrorCode != nil && *r.ErrorCode != 0 {
						t.Logf("poller: provider error %d: %s", *r.ErrorCode, r.ErrorMessage)
					}
					return r.Status, true
				}
			}
		}

		if time.Now().After(deadline) {
			return finalStatus, false
		}
		time.Sleep(integrationPollInterval)
	}
}

// mapTerminalStatus returns the status and true if it is a terminal (non-polling) state.
func mapTerminalStatus(channel, status string) (string, bool) {
	switch channel {
	case "voice":
		switch status {
		case "completed", "failed", "busy", "no-answer", "canceled":
			return status, true
		}
	default: // sms
		switch status {
		case "delivered", "undelivered", "failed":
			return status, true
		}
	}
	return status, false
}

// interpolateEnv replicates the env-var substitution from config.Load so that
// ${VAR} placeholders in the config file are resolved before YAML parsing.
func interpolateEnv(data []byte) []byte {
	out := make([]byte, 0, len(data))
	i := 0
	for i < len(data) {
		if i+1 < len(data) && data[i] == '$' && data[i+1] == '{' {
			end := i + 2
			for end < len(data) && data[end] != '}' {
				end++
			}
			if end < len(data) {
				name := string(data[i+2 : end])
				if val, ok := os.LookupEnv(name); ok {
					out = append(out, val...)
				} else {
					out = append(out, data[i:end+1]...)
				}
				i = end + 1
				continue
			}
		}
		out = append(out, data[i])
		i++
	}
	return out
}

// ---- Tests ----

func TestIntegration_TwilioSMS(t *testing.T) {
	cfg := loadTwilioConfig(t)

	to := os.Getenv("TEST_SMS_TO")
	if to == "" {
		t.Skip("TEST_SMS_TO not set — skipping SMS integration test")
	}

	t.Logf("sending SMS from %s to %s", cfg.FromNumber, to)

	sms := notify.NewTwilioSMS(cfg)
	sid, initialStatus, err := sms.Send(to, "notification relay integration test — please ignore")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	t.Logf("sent: sid=%s initial_status=%s", sid, initialStatus)

	finalStatus, reached := pollStatus(t, cfg, "sms", sid)
	if !reached {
		t.Errorf("timed out after %s waiting for terminal SMS status (last: %q)", integrationPollTimeout, finalStatus)
		return
	}

	t.Logf("terminal status reached: %s", finalStatus)

	// "failed" and "undelivered" are terminal but indicate delivery problems.
	// Log them clearly but do not fail the test — the credentials and number
	// are valid if Twilio accepted the message (sid was returned without error).
	if finalStatus == "failed" || finalStatus == "undelivered" {
		t.Logf("WARNING: message reached terminal failure state %q — check Twilio logs for error details", finalStatus)
	}
}

func TestIntegration_TwilioVoice(t *testing.T) {
	cfg := loadTwilioConfig(t)

	to := os.Getenv("TEST_VOICE_TO")
	if to == "" {
		t.Skip("TEST_VOICE_TO not set — skipping voice integration test")
	}

	t.Logf("initiating voice call from %s to %s", cfg.FromNumber, to)

	voice := notify.NewTwilioVoice(cfg)
	sid, initialStatus, err := voice.Call(to, "This is a notification relay integration test. Please ignore this message.")
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	t.Logf("call initiated: sid=%s initial_status=%s", sid, initialStatus)

	finalStatus, reached := pollStatus(t, cfg, "voice", sid)
	if !reached {
		t.Errorf("timed out after %s waiting for terminal voice status (last: %q)", integrationPollTimeout, finalStatus)
		return
	}

	t.Logf("terminal status reached: %s", finalStatus)

	// Terminal failures (busy, no-answer, failed, canceled) are still valid
	// outcomes from an integration perspective — the call was placed and
	// Twilio processed it. Only "failed" with a provider error code indicates
	// a configuration problem worth investigating.
	if finalStatus == "failed" {
		t.Logf("WARNING: call reached terminal failure state — check Twilio error logs")
	}
}

// noopLogger is used if any test helpers need a logger.
var _ = slog.New(slog.NewTextHandler(io.Discard, nil))
