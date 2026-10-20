package auth

import (
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestPasswordCacheVerifiesOncePerPassword(t *testing.T) {
	c := newPasswordCache()
	var compares atomic.Int32
	c.compare = func(hash, password []byte) error {
		compares.Add(1)
		return bcrypt.CompareHashAndPassword(hash, password)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("s3cret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if !c.Verify("u1", string(hash), "s3cret") {
			t.Fatal("right password refused")
		}
	}
	if compares.Load() != 1 {
		t.Errorf("%d compares for three right checks", compares.Load())
	}

	// Wrong passwords always cost a compare and are never remembered
	for range 2 {
		if c.Verify("u1", string(hash), "nope") {
			t.Fatal("wrong password accepted")
		}
	}
	if compares.Load() != 3 {
		t.Errorf("%d compares after two wrong checks", compares.Load())
	}

	// Another account with the same password is checked on its own
	if !c.Verify("u2", string(hash), "s3cret") || compares.Load() != 4 {
		t.Errorf("second account hit the first account's entry, compares=%d", compares.Load())
	}

	// A new hash for the same password misses and is checked again
	rehash, err := bcrypt.GenerateFromPassword([]byte("s3cret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Verify("u1", string(rehash), "s3cret") || compares.Load() != 5 {
		t.Errorf("changed hash served from cache, compares=%d", compares.Load())
	}

	// Expired entries are checked again
	c.mu.Lock()
	for k, e := range c.entries {
		e.expires = time.Now().Add(-time.Second)
		c.entries[k] = e
	}
	c.mu.Unlock()
	if !c.Verify("u1", string(rehash), "s3cret") || compares.Load() != 6 {
		t.Errorf("expired entry served, compares=%d", compares.Load())
	}

	// Empty inputs never match and never reach bcrypt
	if c.Verify("u1", "", "s3cret") || c.Verify("u1", string(hash), "") || compares.Load() != 6 {
		t.Errorf("empty input accepted or compared, compares=%d", compares.Load())
	}

	// Without a cache the check still works
	var none *passwordCache
	if !none.Verify("u1", string(hash), "s3cret") || none.Verify("u1", string(hash), "nope") {
		t.Error("nil cache misjudged a password")
	}
}

func TestPasswordCacheCapsEntries(t *testing.T) {
	c := newPasswordCache()
	c.compare = func([]byte, []byte) error { return nil }
	future := time.Now().Add(time.Hour)
	for i := range passwordCacheMax {
		var key [32]byte
		key[0], key[1], key[2] = byte(i), byte(i>>8), byte(i>>16)
		c.entries[key] = passwordEntry{hash: "h", expires: future}
	}
	if !c.Verify("new", "h", "p") {
		t.Fatal("verify failed")
	}
	if len(c.entries) != passwordCacheMax {
		t.Errorf("full cache grew to %d", len(c.entries))
	}
	// Expired entries make room once the sweep interval has passed
	c.mu.Lock()
	for k, e := range c.entries {
		e.expires = time.Now().Add(-time.Second)
		c.entries[k] = e
	}
	c.lastSweep = time.Now().Add(-cacheSweepEvery)
	c.mu.Unlock()
	if !c.Verify("new", "h", "p") {
		t.Fatal("verify failed")
	}
	if len(c.entries) != 1 {
		t.Errorf("sweep left %d entries", len(c.entries))
	}
}
