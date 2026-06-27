package smtpapi

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"testing"
)

// TestCRAMServer_RFC2195Vector verifies the digest computation against the
// worked example from RFC 2195 section 3: challenge
// "<1896.697170952@postoffice.reston.mci.net>", secret "tanstaaftanstaaf"
// yields digest "b913a602c7eda7a495b4e6e7334d3890".
func TestCRAMServer_RFC2195Vector(t *testing.T) {
	const challenge = "<1896.697170952@postoffice.reston.mci.net>"
	const secret = "tanstaaftanstaaf"
	const wantDigest = "b913a602c7eda7a495b4e6e7334d3890"

	mac := hmac.New(md5.New, []byte(secret))
	mac.Write([]byte(challenge))
	got := hex.EncodeToString(mac.Sum(nil))
	if got != wantDigest {
		t.Fatalf("digest = %q, want %q", got, wantDigest)
	}

	var finishedUser string
	var finishedRoles []string
	srv := &cramServer{
		domain:    "relay.local",
		challenge: challenge,
		lookup: func(ctx context.Context, username string) (string, []string, error) {
			if username != "tim" {
				t.Fatalf("lookup called with username=%q", username)
			}
			return secret, []string{"publisher"}, nil
		},
		finish: func(username string, roles []string) error {
			finishedUser, finishedRoles = username, roles
			return nil
		},
		fail: func(username string) { t.Fatalf("unexpected fail callback for username=%q", username) },
	}

	_, done, err := srv.Next([]byte("tim " + wantDigest))
	if err != nil || !done {
		t.Fatalf("Next() = done=%v err=%v, want done=true err=nil", done, err)
	}
	if finishedUser != "tim" || len(finishedRoles) != 1 || finishedRoles[0] != "publisher" {
		t.Fatalf("finish called with username=%q roles=%v", finishedUser, finishedRoles)
	}
}

func TestCRAMServer_FirstCallReturnsChallenge(t *testing.T) {
	srv := newCRAMServer("relay.local", nil, nil, nil)
	challenge, done, err := srv.Next(nil)
	if err != nil || done {
		t.Fatalf("Next(nil) = done=%v err=%v, want done=false err=nil", done, err)
	}
	if len(challenge) == 0 || challenge[0] != '<' {
		t.Fatalf("challenge = %q, want RFC2195-style angle-bracketed string", challenge)
	}
}

func TestCRAMServer_WrongDigestRejected(t *testing.T) {
	var failedUser string
	srv := &cramServer{
		domain:    "relay.local",
		challenge: "<challenge@relay.local>",
		lookup: func(ctx context.Context, username string) (string, []string, error) {
			return "correct-secret", []string{"publisher"}, nil
		},
		finish: func(username string, roles []string) error {
			t.Fatal("finish should not be called on digest mismatch")
			return nil
		},
		fail: func(username string) { failedUser = username },
	}

	_, done, err := srv.Next([]byte("tim deadbeefdeadbeefdeadbeefdeadbeef"))
	if err == nil || !done {
		t.Fatalf("Next() = done=%v err=%v, want done=true err!=nil", done, err)
	}
	if failedUser != "tim" {
		t.Fatalf("fail callback got username=%q, want %q", failedUser, "tim")
	}
}

func TestCRAMServer_UnknownUserRejected(t *testing.T) {
	var failedUser string
	srv := &cramServer{
		domain:    "relay.local",
		challenge: "<challenge@relay.local>",
		lookup: func(ctx context.Context, username string) (string, []string, error) {
			return "", nil, errCRAMCredentialNotFound
		},
		finish: func(username string, roles []string) error {
			t.Fatal("finish should not be called for unknown user")
			return nil
		},
		fail: func(username string) { failedUser = username },
	}

	_, done, err := srv.Next([]byte("ghost deadbeefdeadbeefdeadbeefdeadbeef"))
	if err == nil || !done {
		t.Fatalf("Next() = done=%v err=%v, want done=true err!=nil", done, err)
	}
	if failedUser != "ghost" {
		t.Fatalf("fail callback got username=%q, want %q", failedUser, "ghost")
	}
}

func TestCRAMServer_MalformedResponseRejected(t *testing.T) {
	failed := false
	srv := &cramServer{
		domain:    "relay.local",
		challenge: "<challenge@relay.local>",
		fail:      func(username string) { failed = true },
	}

	_, _, err := srv.Next([]byte("no-space-here"))
	if err == nil {
		t.Fatal("expected error for malformed response")
	}
	if !failed {
		t.Fatal("expected fail callback to be invoked")
	}
}

func TestCRAMServer_LookupError(t *testing.T) {
	srv := &cramServer{
		domain:    "relay.local",
		challenge: "<challenge@relay.local>",
		lookup: func(ctx context.Context, username string) (string, []string, error) {
			return "", nil, errors.New("db unavailable")
		},
		fail: func(username string) { t.Fatal("fail should not be called for transient errors") },
	}

	_, done, err := srv.Next([]byte("tim deadbeefdeadbeefdeadbeefdeadbeef"))
	if err == nil || !done {
		t.Fatalf("Next() = done=%v err=%v, want done=true err!=nil", done, err)
	}
}
