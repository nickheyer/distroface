package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Decodes a json value into out, false on a miss or when off
func (c *Client) Get(ctx context.Context, key string, out any) (bool, error) {
	raw, ok, err := c.GetString(ctx, key)
	if err != nil || !ok {
		return false, err
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		// Corrupt entries are dropped rather than served
		_ = c.Delete(ctx, key)
		return false, nil
	}
	return true, nil
}

// Stores a json value, zero ttl keeps it until evicted
func (c *Client) Set(ctx context.Context, key string, val any, ttl time.Duration) error {
	if c == nil {
		return nil
	}
	raw, err := json.Marshal(val)
	if err != nil {
		return err
	}
	return c.SetString(ctx, key, string(raw), ttl)
}

// Stores only when absent, true when this call won
func (c *Client) SetNX(ctx context.Context, key, val string, ttl time.Duration) (bool, error) {
	if c == nil {
		return false, nil
	}
	ctx, cancel := c.opContext(ctx)
	defer cancel()
	ok, err := c.rdb.SetNX(ctx, key, val, ttl).Result()
	c.observe(err)
	return ok, err
}

func (c *Client) GetString(ctx context.Context, key string) (string, bool, error) {
	if c == nil {
		return "", false, nil
	}
	ctx, cancel := c.opContext(ctx)
	defer cancel()
	raw, err := c.rdb.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	c.observe(err)
	if err != nil {
		return "", false, err
	}
	return raw, true, nil
}

func (c *Client) SetString(ctx context.Context, key, val string, ttl time.Duration) error {
	if c == nil {
		return nil
	}
	ctx, cancel := c.opContext(ctx)
	defer cancel()
	err := c.rdb.Set(ctx, key, val, ttl).Err()
	c.observe(err)
	return err
}

// Reads several keys in one round trip, present marks hits
func (c *Client) GetMany(ctx context.Context, keys ...string) (values []string, present []bool, err error) {
	values, present = make([]string, len(keys)), make([]bool, len(keys))
	if c == nil || len(keys) == 0 {
		return values, present, nil
	}
	ctx, cancel := c.opContext(ctx)
	defer cancel()
	// Clusters refuse cross slot mget, a pipeline of gets fits everywhere
	pipe := c.rdb.Pipeline()
	cmds := make([]*redis.StringCmd, len(keys))
	for i, key := range keys {
		cmds[i] = pipe.Get(ctx, key)
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		c.observe(err)
		return values, present, err
	}
	for i, cmd := range cmds {
		if cmd.Err() == nil {
			values[i], present[i] = cmd.Val(), true
		}
	}
	return values, present, nil
}

func (c *Client) Delete(ctx context.Context, keys ...string) error {
	if c == nil || len(keys) == 0 {
		return nil
	}
	ctx, cancel := c.opContext(ctx)
	defer cancel()
	// Single key unlinks pipelined, clusters refuse multi key ones
	pipe := c.rdb.Pipeline()
	for _, key := range keys {
		pipe.Unlink(ctx, key)
	}
	_, err := pipe.Exec(ctx)
	c.observe(err)
	return err
}

// Drops every key under a prefix
func (c *Client) DeletePrefix(ctx context.Context, prefix string) error {
	return c.DeleteMatching(ctx, globEscape(prefix)+"*")
}

// Drops keys matching glob patterns, each cluster master scans itself
func (c *Client) DeleteMatching(ctx context.Context, patterns ...string) error {
	if c == nil || len(patterns) == 0 {
		return nil
	}
	var err error
	if cluster, ok := c.rdb.(*redis.ClusterClient); ok {
		err = cluster.ForEachMaster(ctx, func(ctx context.Context, node *redis.Client) error {
			return unlinkMatching(ctx, node, patterns)
		})
	} else {
		err = unlinkMatching(ctx, c.rdb, patterns)
	}
	c.observe(err)
	return err
}

func unlinkMatching(ctx context.Context, client redis.Cmdable, patterns []string) error {
	const batch = 500
	for _, pattern := range patterns {
		iter := client.Scan(ctx, 0, pattern, batch).Iterator()
		pipe := client.Pipeline()
		queued := 0
		flush := func() error {
			if queued == 0 {
				return nil
			}
			queued = 0
			_, err := pipe.Exec(ctx)
			return err
		}
		for iter.Next(ctx) {
			pipe.Unlink(ctx, iter.Val())
			queued++
			if queued >= batch {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		if err := iter.Err(); err != nil {
			return fmt.Errorf("scanning %s: %w", pattern, err)
		}
		if err := flush(); err != nil {
			return err
		}
	}
	return nil
}

// Escapes glob metacharacters so a prefix matches literally
func globEscape(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '*', '?', '[', ']', '\\':
			out = append(out, '\\')
		}
		out = append(out, s[i])
	}
	return string(out)
}

// Reads and removes a key in one step, false when it was absent
func (c *Client) Take(ctx context.Context, key string) (string, bool, error) {
	if c == nil {
		return "", false, nil
	}
	ctx, cancel := c.opContext(ctx)
	defer cancel()
	val, err := c.rdb.GetDel(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	c.observe(err)
	if err != nil {
		return "", false, err
	}
	return val, true, nil
}
