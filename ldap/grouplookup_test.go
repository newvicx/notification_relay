package ldap

import (
	"context"
	"errors"
	"testing"
	"time"
)

type countingLookup struct {
	calls int
	err   error
}

func (c *countingLookup) LookupGroups(_ context.Context, u string) (*AuthResult, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return &AuthResult{UserDN: "CN=" + u, Groups: []string{"g"}}, nil
}

func TestCachedGroupLookup(t *testing.T) {
	inner := &countingLookup{}
	c := NewCachedGroupLookup(inner, 2, time.Hour)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := c.LookupGroups(ctx, "alice"); err != nil {
			t.Fatal(err)
		}
	}
	if inner.calls != 1 {
		t.Fatalf("calls = %d, want 1 (cached)", inner.calls)
	}
	// LRU eviction at capacity 2.
	c.LookupGroups(ctx, "bob")
	c.LookupGroups(ctx, "carol") // evicts alice
	c.LookupGroups(ctx, "alice")
	if inner.calls != 4 {
		t.Fatalf("calls = %d, want 4 after eviction", inner.calls)
	}
}

func TestCachedGroupLookup_DoesNotCacheErrors(t *testing.T) {
	inner := &countingLookup{err: errors.New("down")}
	c := NewCachedGroupLookup(inner, 2, time.Hour)
	c.LookupGroups(context.Background(), "alice")
	c.LookupGroups(context.Background(), "alice")
	if inner.calls != 2 {
		t.Fatalf("calls = %d, want 2", inner.calls)
	}
}

func TestCachedGroupLookup_Expiry(t *testing.T) {
	inner := &countingLookup{}
	c := NewCachedGroupLookup(inner, 2, 10*time.Millisecond)
	c.LookupGroups(context.Background(), "alice")
	time.Sleep(20 * time.Millisecond)
	c.LookupGroups(context.Background(), "alice")
	if inner.calls != 2 {
		t.Fatalf("calls = %d, want 2 after TTL", inner.calls)
	}
}

func TestLookupFilter_EscapesAndMatchesIdentityForms(t *testing.T) {
	got := lookupFilter("a*)(uid=*")
	want := `(&(objectClass=user)(|(sAMAccountName=a\2a\29\28uid=\2a)(userPrincipalName=a\2a\29\28uid=\2a)(mail=a\2a\29\28uid=\2a)))`
	if got != want {
		t.Fatalf("filter = %s\nwant     %s", got, want)
	}
}
