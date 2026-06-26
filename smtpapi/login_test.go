package smtpapi

import (
	"errors"
	"testing"
)

func TestLoginServer_NoInitialResponse(t *testing.T) {
	var gotUser, gotPass string
	srv := newLoginServer(func(username, password string) error {
		gotUser, gotPass = username, password
		return nil
	})

	challenge, done, err := srv.Next(nil)
	if err != nil || done || string(challenge) != "Username:" {
		t.Fatalf("step 1: got challenge=%q done=%v err=%v", challenge, done, err)
	}

	challenge, done, err = srv.Next([]byte("alice"))
	if err != nil || done || string(challenge) != "Password:" {
		t.Fatalf("step 2: got challenge=%q done=%v err=%v", challenge, done, err)
	}

	challenge, done, err = srv.Next([]byte("hunter2"))
	if err != nil || !done || challenge != nil {
		t.Fatalf("step 3: got challenge=%q done=%v err=%v", challenge, done, err)
	}

	if gotUser != "alice" || gotPass != "hunter2" {
		t.Fatalf("authenticate called with username=%q password=%q", gotUser, gotPass)
	}
}

func TestLoginServer_InitialResponse(t *testing.T) {
	var gotUser, gotPass string
	srv := newLoginServer(func(username, password string) error {
		gotUser, gotPass = username, password
		return nil
	})

	// Client supplied the username as the initial response, so the first
	// call to Next has a non-nil response instead of nil.
	challenge, done, err := srv.Next([]byte("bob"))
	if err != nil || done || string(challenge) != "Password:" {
		t.Fatalf("step 1: got challenge=%q done=%v err=%v", challenge, done, err)
	}

	_, done, err = srv.Next([]byte("swordfish"))
	if err != nil || !done {
		t.Fatalf("step 2: done=%v err=%v", done, err)
	}

	if gotUser != "bob" || gotPass != "swordfish" {
		t.Fatalf("authenticate called with username=%q password=%q", gotUser, gotPass)
	}
}

func TestLoginServer_AuthenticateError(t *testing.T) {
	wantErr := errors.New("invalid credentials")
	srv := newLoginServer(func(username, password string) error {
		return wantErr
	})

	srv.Next(nil)
	srv.Next([]byte("alice"))
	_, done, err := srv.Next([]byte("wrong"))
	if !done || err != wantErr {
		t.Fatalf("got done=%v err=%v, want done=true err=%v", done, err, wantErr)
	}
}

func TestLoginServer_ResponseAfterDone(t *testing.T) {
	srv := newLoginServer(func(username, password string) error {
		return nil
	})

	srv.Next(nil)
	srv.Next([]byte("alice"))
	srv.Next([]byte("hunter2"))

	if _, _, err := srv.Next([]byte("extra")); err == nil {
		t.Fatal("expected error for response after authentication completed")
	}
}
