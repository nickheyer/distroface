package cache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Sliding window on a sorted set, scores are event times in ms
var windowScript = redis.NewScript(`
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now - window)
local count = redis.call('ZCARD', KEYS[1])
local allowed = 1
if ARGV[4] == '1' then
  if limit > 0 and count >= limit then
    allowed = 0
  else
    redis.call('ZADD', KEYS[1], now, ARGV[5])
    count = count + 1
  end
end
if count > 0 then
  redis.call('PEXPIRE', KEYS[1], window)
end
local oldest = redis.call('ZRANGE', KEYS[1], 0, 0, 'WITHSCORES')
local first = now
if oldest[2] then first = tonumber(oldest[2]) end
return {allowed, count, first}`)

// Counts events in the trailing window, add records one when under limit.
// A zero limit never refuses, oldest is when the window frees a slot
func (c *Client) Window(ctx context.Context, key string, limit int, window time.Duration, add bool) (allowed bool, count int, oldest time.Time, err error) {
	now := time.Now()
	if c == nil {
		return true, 0, now, nil
	}
	ctx, cancel := c.opContext(ctx)
	defer cancel()
	var b [6]byte
	_, _ = rand.Read(b[:])
	member := strconv.FormatInt(now.UnixMilli(), 10) + "-" + hex.EncodeToString(b[:])
	flag := "0"
	if add {
		flag = "1"
	}
	res, err := windowScript.Run(ctx, c.rdb, []string{key}, now.UnixMilli(), window.Milliseconds(), limit, flag, member).Int64Slice()
	c.observe(err)
	if err != nil {
		return true, 0, now, err
	}
	if len(res) != 3 {
		return true, 0, now, nil
	}
	return res[0] == 1, int(res[1]), time.UnixMilli(res[2]), nil
}
