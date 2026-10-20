package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Positive checks live this long, a hash change misses sooner
const (
	passwordCacheTTL = 5 * time.Minute
	passwordCacheMax = 10000
)

// Full expiry sweeps at capacity run at most this often
const cacheSweepEvery = 30 * time.Second

// Matched passwords kept as keyed hashes, wrong ones never cached
type passwordCache struct {
	secret    []byte
	compare   func(hash, password []byte) error
	mu        sync.Mutex
	entries   map[[32]byte]passwordEntry
	lastSweep time.Time
}

type passwordEntry struct {
	hash    string
	expires time.Time
}

func newPasswordCache() *passwordCache {
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	return &passwordCache{
		secret:  secret,
		compare: bcrypt.CompareHashAndPassword,
		entries: map[[32]byte]passwordEntry{},
	}
}

func (c *passwordCache) key(userID, password string) [32]byte {
	mac := hmac.New(sha256.New, c.secret)
	mac.Write([]byte(userID))
	mac.Write([]byte{0})
	mac.Write([]byte(password))
	var key [32]byte
	copy(key[:], mac.Sum(nil))
	return key
}

// Checks a password against the stored hash, a hit skips bcrypt.
// The stored hash is part of the hit, so a password change misses
func (c *passwordCache) Verify(userID, hash, password string) bool {
	if hash == "" || password == "" {
		return false
	}
	if c == nil {
		return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
	}
	key := c.key(userID, password)
	now := time.Now()
	c.mu.Lock()
	entry, ok := c.entries[key]
	c.mu.Unlock()
	if ok && now.Before(entry.expires) && subtle.ConstantTimeCompare([]byte(entry.hash), []byte(hash)) == 1 {
		return true
	}
	if c.compare([]byte(hash), []byte(password)) != nil {
		if ok {
			c.mu.Lock()
			delete(c.entries, key)
			c.mu.Unlock()
		}
		return false
	}
	c.mu.Lock()
	if len(c.entries) >= passwordCacheMax && now.Sub(c.lastSweep) >= cacheSweepEvery {
		c.lastSweep = now
		for k, e := range c.entries {
			if !now.Before(e.expires) {
				delete(c.entries, k)
			}
		}
	}
	if len(c.entries) < passwordCacheMax {
		c.entries[key] = passwordEntry{hash: hash, expires: now.Add(passwordCacheTTL)}
	}
	c.mu.Unlock()
	return true
}
