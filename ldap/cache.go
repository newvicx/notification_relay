package ldap

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

type cacheEntry struct {
	key       string
	result    *AuthResult
	expiresAt time.Time
}

type cachedAuthenticator struct {
	inner   Authenticator
	maxSize int
	ttl     time.Duration
	mu      sync.Mutex
	lru     *list.List               // front = most recently used
	index   map[string]*list.Element // key → list element
}

// NewCachedAuthenticator wraps inner with an in-process LRU cache. maxSize
// limits the number of cached entries; ttl controls how long an entry is valid.
// Only successful authentications are cached — failures are never stored so a
// corrected password is always tried against LDAP immediately.
// Set maxSize <= 0 to disable caching (inner is returned as-is).
func NewCachedAuthenticator(inner Authenticator, maxSize int, ttl time.Duration) Authenticator {
	if maxSize <= 0 {
		return inner
	}
	return &cachedAuthenticator{
		inner:   inner,
		maxSize: maxSize,
		ttl:     ttl,
		lru:     list.New(),
		index:   make(map[string]*list.Element, maxSize),
	}
}

func (c *cachedAuthenticator) AuthenticateUser(ctx context.Context, username, password string) (*AuthResult, error) {
	key := authCacheKey(username, password)
	if result, ok := c.cacheGet(key); ok {
		return result, nil
	}
	result, err := c.inner.AuthenticateUser(ctx, username, password)
	if err != nil {
		return nil, err // never cache failures
	}
	c.cachePut(key, result)
	return result, nil
}

func (c *cachedAuthenticator) cacheGet(key string) (*AuthResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.index[key]
	if !ok {
		return nil, false
	}
	entry := el.Value.(*cacheEntry)
	if time.Now().After(entry.expiresAt) {
		c.lru.Remove(el)
		delete(c.index, key)
		return nil, false
	}
	c.lru.MoveToFront(el)
	return entry.result, true
}

func (c *cachedAuthenticator) cachePut(key string, result *AuthResult) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Update existing entry if present.
	if el, ok := c.index[key]; ok {
		entry := el.Value.(*cacheEntry)
		entry.result = result
		entry.expiresAt = time.Now().Add(c.ttl)
		c.lru.MoveToFront(el)
		return
	}

	// Evict the least-recently-used entry if at capacity.
	if c.lru.Len() >= c.maxSize {
		back := c.lru.Back()
		if back != nil {
			evicted := back.Value.(*cacheEntry)
			delete(c.index, evicted.key)
			c.lru.Remove(back)
		}
	}

	entry := &cacheEntry{key: key, result: result, expiresAt: time.Now().Add(c.ttl)}
	el := c.lru.PushFront(entry)
	c.index[key] = el
}

// authCacheKey returns a SHA-256 hash of the credentials as a hex string.
// Credentials are hashed so they are never stored as plaintext map keys.
func authCacheKey(username, password string) string {
	h := sha256.New()
	h.Write([]byte(username))
	h.Write([]byte(":"))
	h.Write([]byte(password))
	return hex.EncodeToString(h.Sum(nil))
}
