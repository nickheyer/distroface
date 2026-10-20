package cache

import (
	"context"
	"strings"
)

var channelPrefix = Prefix + "ch:"

// Tells the other instances, the sender skips its own messages
func (c *Client) Publish(ctx context.Context, channel, payload string) error {
	if c == nil {
		return nil
	}
	ctx, cancel := c.opContext(ctx)
	defer cancel()
	err := c.rdb.Publish(ctx, channelPrefix+channel, c.id+"|"+payload).Err()
	c.observe(err)
	return err
}

// Runs fn for every message from another instance until ctx ends
func (c *Client) Subscribe(ctx context.Context, channel string, fn func(payload string)) {
	if c == nil {
		return
	}
	sub := c.rdb.Subscribe(ctx, channelPrefix+channel)
	go func() {
		defer sub.Close()
		// The channel reconnects on its own after a dropped link
		msgs := sub.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-msgs:
				if !ok {
					return
				}
				origin, payload, _ := strings.Cut(msg.Payload, "|")
				if origin == c.id {
					continue
				}
				fn(payload)
			}
		}
	}()
}
