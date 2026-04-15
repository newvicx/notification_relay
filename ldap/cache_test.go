package ldap

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// stubAuthenticator is a configurable test double for the Authenticator interface.
type stubAuthenticator struct {
	calls  int
	result *AuthResult
	err    error
}

func (s *stubAuthenticator) AuthenticateUser(_ context.Context, _, _ string) (*AuthResult, error) {
	s.calls++
	return s.result, s.err
}

var okResult = &AuthResult{UserDN: "CN=alice,DC=example,DC=com", Groups: []string{"grp-oncall"}}

func TestCache_Hit(t *testing.T) {
	inner := &stubAuthenticator{result: okResult}
	cached := NewCachedAuthenticator(inner, 10, time.Minute)

	r1, err := cached.AuthenticateUser(context.Background(), "alice", "pass")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	r2, err := cached.AuthenticateUser(context.Background(), "alice", "pass")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if inner.calls != 1 {
		t.Errorf("inner called %d times, want 1 (second should be cached)", inner.calls)
	}
	if r1 != r2 {
		t.Error("expected same pointer from cache")
	}
}

func TestCache_Miss_DifferentPassword(t *testing.T) {
	inner := &stubAuthenticator{result: okResult}
	cached := NewCachedAuthenticator(inner, 10, time.Minute)

	cached.AuthenticateUser(context.Background(), "alice", "pass1")
	cached.AuthenticateUser(context.Background(), "alice", "pass2")

	if inner.calls != 2 {
		t.Errorf("inner called %d times, want 2 (different passwords → different keys)", inner.calls)
	}
}

func TestCache_TTLExpiry(t *testing.T) {
	inner := &stubAuthenticator{result: okResult}
	cached := NewCachedAuthenticator(inner, 10, time.Millisecond)

	cached.AuthenticateUser(context.Background(), "alice", "pass")
	time.Sleep(5 * time.Millisecond) // let TTL expire
	cached.AuthenticateUser(context.Background(), "alice", "pass")

	if inner.calls != 2 {
		t.Errorf("inner called %d times after TTL expiry, want 2", inner.calls)
	}
}

func TestCache_LRUEviction(t *testing.T) {
	inner := &stubAuthenticator{result: okResult}
	cached := NewCachedAuthenticator(inner, 2, time.Minute) // capacity = 2

	// Fill to capacity: list after → [bob(MRU), alice(LRU)]
	cached.AuthenticateUser(context.Background(), "alice", "pass")
	cached.AuthenticateUser(context.Background(), "bob", "pass")

	// Access alice to promote it to MRU: list → [alice(MRU), bob(LRU)]
	cached.AuthenticateUser(context.Background(), "alice", "pass")

	// Add carol: capacity full, evict LRU = bob → list = [carol, alice]
	callsBefore := inner.calls
	cached.AuthenticateUser(context.Background(), "carol", "pass")
	if inner.calls != callsBefore+1 {
		t.Fatalf("carol: expected inner call, got none (calls=%d)", inner.calls)
	}

	// bob was evicted: looking it up must call inner again.
	callsBefore = inner.calls
	cached.AuthenticateUser(context.Background(), "bob", "pass")
	if inner.calls != callsBefore+1 {
		t.Errorf("bob should be evicted (was LRU): inner.calls went from %d to %d, want +1",
			callsBefore, inner.calls)
	}
}

func TestCache_FailureNotCached(t *testing.T) {
	authErr := errors.New("invalid credentials")
	inner := &stubAuthenticator{err: authErr}
	cached := NewCachedAuthenticator(inner, 10, time.Minute)

	cached.AuthenticateUser(context.Background(), "alice", "wrong")
	cached.AuthenticateUser(context.Background(), "alice", "wrong")

	if inner.calls != 2 {
		t.Errorf("failures must not be cached: inner called %d times, want 2", inner.calls)
	}
}

func TestCache_Disabled(t *testing.T) {
	inner := &stubAuthenticator{result: okResult}
	// maxSize=0 disables caching; NewCachedAuthenticator returns inner as-is.
	result := NewCachedAuthenticator(inner, 0, time.Minute)
	if result != inner {
		t.Error("want inner returned directly when maxSize=0")
	}
}

func TestAuthCacheKey_DifferentInputs(t *testing.T) {
	k1 := authCacheKey("alice", "pass")
	k2 := authCacheKey("alice", "pass")
	k3 := authCacheKey("alice", "different")
	k4 := authCacheKey("bob", "pass")

	if k1 != k2 {
		t.Error("same inputs should produce same key")
	}
	if k1 == k3 {
		t.Error("different passwords should produce different keys")
	}
	if k1 == k4 {
		t.Error("different usernames should produce different keys")
	}
	// Key must not contain plaintext credentials.
	if strings.Contains(k1, "alice") || strings.Contains(k1, "pass") {
		t.Errorf("cache key must not contain plaintext credentials: %s", k1)
	}
}
