package ldap

import (
	"container/list"
	"context"
	"fmt"
	"sync"
	"time"

	goldap "github.com/go-ldap/ldap/v3"
)

// GroupLookup resolves a user's LDAP group memberships without verifying a
// password. The identifier may be a sAMAccountName, userPrincipalName or mail
// value; AuthResult.Username reports the canonical sAMAccountName. It is for callers that have already authenticated the user by
// other means (e.g. a validated OAuth JWT) and only need authorization data.
// ErrUserNotFound is returned when no LDAP entry matches the username.
type GroupLookup interface {
	LookupGroups(ctx context.Context, username string) (*AuthResult, error)
}

type ldapGroupLookup struct {
	primaryURL, backupURL, bindDN, bindPassword, userBaseDN string
	tlsSkipVerify                                           bool
}

// NewGroupLookup returns a GroupLookup backed by the real LDAP server(s).
func NewGroupLookup(primaryURL, backupURL, bindDN, bindPassword, userBaseDN string, tlsSkipVerify bool) GroupLookup {
	return &ldapGroupLookup{primaryURL, backupURL, bindDN, bindPassword, userBaseDN, tlsSkipVerify}
}

func (l *ldapGroupLookup) LookupGroups(_ context.Context, username string) (*AuthResult, error) {
	conn, err := dialAndBind(l.primaryURL, l.bindDN, l.bindPassword, l.tlsSkipVerify)
	if err != nil {
		primaryErr := err
		if l.backupURL == "" {
			return nil, fmt.Errorf("ldap connect: %w", primaryErr)
		}
		conn, err = dialAndBind(l.backupURL, l.bindDN, l.bindPassword, l.tlsSkipVerify)
		if err != nil {
			return nil, fmt.Errorf("ldap connect: primary: %w; backup: %w", primaryErr, err)
		}
	}
	defer conn.Close()

	req := goldap.NewSearchRequest(
		l.userBaseDN,
		goldap.ScopeWholeSubtree,
		goldap.NeverDerefAliases,
		2, 0, false, // 2 so an ambiguous identifier is detectable
		lookupFilter(username),
		[]string{"dn", "sAMAccountName", "memberOf"},
		nil,
	)
	result, err := conn.Search(req)
	if err != nil && !goldap.IsErrorWithCode(err, goldap.LDAPResultSizeLimitExceeded) {
		return nil, fmt.Errorf("user search: %w", err)
	}
	if result == nil || len(result.Entries) == 0 {
		return nil, ErrUserNotFound
	}
	if len(result.Entries) > 1 || err != nil {
		// More than one account claims this identifier; refuse to guess.
		return nil, ErrUserNotFound
	}
	entry := result.Entries[0]
	return &AuthResult{
		Username: entry.GetAttributeValue("sAMAccountName"),
		UserDN:   entry.DN,
		Groups:   parseCNs(entry.GetAttributeValues("memberOf")),
	}, nil
}

// lookupFilter matches an identifier against the attributes an identity
// provider commonly sends: username, UPN, or email.
func lookupFilter(id string) string {
	e := goldap.EscapeFilter(id)
	return fmt.Sprintf("(&(objectClass=user)(|(sAMAccountName=%s)(userPrincipalName=%s)(mail=%s)))", e, e, e)
}

type groupCacheEntry struct {
	username  string
	result    *AuthResult
	expiresAt time.Time
}

type cachedGroupLookup struct {
	inner   GroupLookup
	maxSize int
	ttl     time.Duration
	mu      sync.Mutex
	lru     *list.List
	index   map[string]*list.Element
}

// NewCachedGroupLookup wraps inner with a small LRU+TTL cache so API clients
// sending a bearer token on every request do not each cost an LDAP round
// trip. Only successes are cached. maxSize or ttl <= 0 disables caching.
func NewCachedGroupLookup(inner GroupLookup, maxSize int, ttl time.Duration) GroupLookup {
	if maxSize <= 0 || ttl <= 0 {
		return inner
	}
	return &cachedGroupLookup{inner: inner, maxSize: maxSize, ttl: ttl, lru: list.New(), index: map[string]*list.Element{}}
}

func (c *cachedGroupLookup) LookupGroups(ctx context.Context, username string) (*AuthResult, error) {
	c.mu.Lock()
	if el, ok := c.index[username]; ok {
		e := el.Value.(*groupCacheEntry)
		if time.Now().Before(e.expiresAt) {
			c.lru.MoveToFront(el)
			c.mu.Unlock()
			return e.result, nil
		}
		c.lru.Remove(el)
		delete(c.index, username)
	}
	c.mu.Unlock()

	res, err := c.inner.LookupGroups(ctx, username)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.index[username]; ok {
		c.lru.Remove(el)
		delete(c.index, username)
	}
	for c.lru.Len() >= c.maxSize {
		back := c.lru.Back()
		delete(c.index, back.Value.(*groupCacheEntry).username)
		c.lru.Remove(back)
	}
	c.index[username] = c.lru.PushFront(&groupCacheEntry{username, res, time.Now().Add(c.ttl)})
	return res, nil
}
