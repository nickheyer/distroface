package cache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var lockPrefix = Prefix + "lock:"

// Compare and expire so only the holder can extend
var refreshScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return 0`)

// Compare and delete so only the holder can release
var unlockScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0`)

// One held lease, nil when the cache is off and nothing contends
type Lock struct {
	c     *Client
	key   string
	token string
	ttl   time.Duration
}

// Grabs a named lease, false when another holder has it
func (c *Client) TryLock(ctx context.Context, name string, ttl time.Duration) (*Lock, bool, error) {
	if c == nil {
		return nil, true, nil
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	l := &Lock{c: c, key: lockPrefix + name, token: c.id + ":" + hex.EncodeToString(b[:]), ttl: ttl}
	ok, err := c.SetNX(ctx, l.key, l.token, ttl)
	if err != nil || !ok {
		return nil, false, err
	}
	return l, true, nil
}

// Instance holding a named lease, empty when free
func (c *Client) LockHolder(ctx context.Context, name string) (string, error) {
	token, ok, err := c.GetString(ctx, lockPrefix+name)
	if err != nil || !ok {
		return "", err
	}
	holder, _, _ := strings.Cut(token, ":")
	return holder, nil
}

// Extends the lease, fails once another holder took over
func (l *Lock) Refresh(ctx context.Context) error {
	if l == nil {
		return nil
	}
	ctx, cancel := l.c.opContext(ctx)
	defer cancel()
	n, err := refreshScript.Run(ctx, l.c.rdb, []string{l.key}, l.token, l.ttl.Milliseconds()).Int()
	l.c.observe(err)
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("lock lost")
	}
	return nil
}

func (l *Lock) Unlock(ctx context.Context) error {
	if l == nil {
		return nil
	}
	ctx, cancel := l.c.opContext(ctx)
	defer cancel()
	err := unlockScript.Run(ctx, l.c.rdb, []string{l.key}, l.token).Err()
	l.c.observe(err)
	return err
}

// Refreshes in the background until stop runs or ctx ends
func (l *Lock) KeepAlive(ctx context.Context) (stop func()) {
	if l == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(max(l.ttl/3, 100*time.Millisecond))
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := l.Refresh(ctx); err != nil && !errors.Is(err, context.Canceled) {
					l.c.log.Warn("Lock %s lease refresh failed: %v", strings.TrimPrefix(l.key, lockPrefix), err)
				}
			}
		}
	}()
	return cancel
}

var duePrefix = Prefix + "due:"

// Claims one scheduled slot per interval across instances. The stamp has
// no ttl so the clock survives restarts, the claim keeps peers off one tick
func (c *Client) ClaimDue(ctx context.Context, name string, interval time.Duration) (bool, error) {
	if c == nil {
		return false, errors.New("shared cache off")
	}
	raw, ok, err := c.GetString(ctx, duePrefix+name)
	if err != nil {
		return false, err
	}
	var last time.Time
	if ok {
		last, _ = time.Parse(time.RFC3339, raw)
	}
	if time.Since(last) < interval {
		return false, nil
	}
	won, err := c.SetNX(ctx, duePrefix+name+":claim", c.id, time.Minute)
	if err != nil {
		return false, err
	}
	if !won {
		return false, nil
	}
	return true, c.SetString(ctx, duePrefix+name, time.Now().UTC().Format(time.RFC3339), 0)
}
