package cache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/nickheyer/distroface/pkg/config"
	"github.com/nickheyer/distroface/pkg/logger"
)

// Namespace in front of every key this process writes
const Prefix = "df:"

// Instance liveness, three missed beats drop an instance
const (
	heartbeatInterval = 10 * time.Second
	heartbeatStale    = 3 * heartbeatInterval
	probeInterval     = 10 * time.Second
	bootPingTimeout   = 5 * time.Second
	opTimeout         = 2 * time.Second
)

var instancesKey = Prefix + "instances"

// Shared redis behind every cross instance cache, nil means off
type Client struct {
	rdb     redis.UniversalClient
	log     *logger.Logger
	id      string
	summary string
	cancel  context.CancelFunc

	mu      sync.Mutex
	lastErr error
	down    bool
}

// Connects, probes once, and starts the heartbeat and prober
func New(ctx context.Context, cfg config.RedisConfig, log *logger.Logger) (*Client, error) {
	if !cfg.Enabled() {
		return nil, nil
	}
	if log == nil {
		log = logger.New()
	}
	rdb, err := newUniversal(cfg)
	if err != nil {
		return nil, err
	}
	bg, cancel := context.WithCancel(ctx)
	c := &Client{rdb: rdb, log: log, id: instanceID(), summary: cfg.Redacted(), cancel: cancel}

	pingCtx, pingCancel := context.WithTimeout(ctx, bootPingTimeout)
	err = rdb.Ping(pingCtx).Err()
	pingCancel()
	c.observe(err)
	if err != nil {
		log.Warn("Redis %s unreachable, caches run in process memory until it answers: %v", c.summary, err)
	} else {
		log.Info("Redis %s reachable, shared cache on as %s", c.summary, c.id)
		// Peers may count instances right away
		c.beat(bg)
	}
	go c.heartbeat(bg)
	return c, nil
}

// Reports whether a shared cache is wired
func (c *Client) Enabled() bool {
	return c != nil
}

// Name of this process in locks and status reports
func (c *Client) ID() string {
	if c == nil {
		return ""
	}
	return c.id
}

// Underlying pool for libraries that take one directly
func (c *Client) Redis() redis.UniversalClient {
	if c == nil {
		return nil
	}
	return c.rdb
}

// Connection summary safe for logs and the ui
func (c *Client) Summary() string {
	if c == nil {
		return "memory"
	}
	return c.summary
}

func (c *Client) Ping(ctx context.Context) error {
	if c == nil {
		return nil
	}
	err := c.rdb.Ping(ctx).Err()
	c.observe(err)
	return err
}

// Last probe failure, nil while redis answers
func (c *Client) Err() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}

// Failing subsystem map entry for the health service
func (c *Client) Health(context.Context) map[string]string {
	out := map[string]string{}
	if err := c.Err(); err != nil {
		out["redis"] = err.Error()
	}
	return out
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	_ = c.rdb.ZRem(ctx, instancesKey, c.id).Err()
	return c.rdb.Close()
}

// Ids of processes that beat within the staleness window
func (c *Client) Instances(ctx context.Context) ([]string, error) {
	if c == nil {
		return nil, nil
	}
	oldest := strconv.FormatInt(time.Now().Add(-heartbeatStale).UnixMilli(), 10)
	ids, err := c.rdb.ZRangeByScore(ctx, instancesKey, &redis.ZRangeBy{Min: oldest, Max: "+inf"}).Result()
	c.observe(err)
	return ids, err
}

// Logs transitions instead of every failure
func (c *Client) observe(err error) {
	if c == nil || errors.Is(err, redis.Nil) || errors.Is(err, context.Canceled) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.lastErr = err
		if !c.down {
			c.down = true
			c.log.Warn("Redis %s unreachable, falling back to process memory: %v", c.summary, err)
		}
		return
	}
	c.lastErr = nil
	if c.down {
		c.down = false
		c.log.Info("Redis %s reachable again", c.summary)
	}
}

// Registers this process and prunes ones that stopped beating
func (c *Client) beat(ctx context.Context) {
	opCtx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	now := time.Now()
	pipe := c.rdb.Pipeline()
	pipe.ZAdd(opCtx, instancesKey, redis.Z{Score: float64(now.UnixMilli()), Member: c.id})
	pipe.ZRemRangeByScore(opCtx, instancesKey, "-inf", strconv.FormatInt(now.Add(-heartbeatStale).UnixMilli(), 10))
	_, err := pipe.Exec(opCtx)
	c.observe(err)
}

// Beats on a cadence, the first beat ran during New
func (c *Client) heartbeat(ctx context.Context) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.beat(ctx)
		}
	}
}

// Hostname plus a random tail, unique per process
func instanceID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "distroface"
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	return host + "-" + hex.EncodeToString(b[:])
}

// Bounded context for fire and forget cache calls
func (c *Client) opContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, opTimeout)
}
