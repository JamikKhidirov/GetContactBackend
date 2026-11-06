// Package redis provides caching, rate limiting and token revocation on Redis.
package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/getcontact/backend/internal/config"
	"github.com/getcontact/backend/internal/metrics"
	goredis "github.com/redis/go-redis/v9"
)

// Cache wraps a Redis client with JSON helpers and metrics.
type Cache struct {
	client *goredis.Client
	ttl    time.Duration
}

// New connects to Redis and verifies the connection.
func New(ctx context.Context, cfg config.RedisConfig) (*Cache, error) {
	client := goredis.NewClient(&goredis.Options{
		Addr:            cfg.Addr,
		Password:        cfg.Password,
		DB:              cfg.DB,
		DialTimeout:     5 * time.Second,
		ReadTimeout:     3 * time.Second,
		WriteTimeout:    3 * time.Second,
		PoolSize:        20,
		MinIdleConns:    2,
		ConnMaxIdleTime: 5 * time.Minute,
	})
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return &Cache{client: client, ttl: cfg.CacheTTL}, nil
}

// Client exposes the underlying client for health checks.
func (c *Cache) Client() *goredis.Client { return c.client }

// Close releases the connection pool.
func (c *Cache) Close() error { return c.client.Close() }

// Ping checks Redis connectivity.
func (c *Cache) Ping(ctx context.Context) error { return c.client.Ping(ctx).Err() }

// GetJSON reads and unmarshals a cached value. A miss returns false, nil.
func (c *Cache) GetJSON(ctx context.Context, key string, dst any) (bool, error) {
	raw, err := c.client.Get(ctx, key).Bytes()
	if errors.Is(err, goredis.Nil) {
		metrics.CacheOperations.WithLabelValues("get", "miss").Inc()
		return false, nil
	}
	if err != nil {
		metrics.CacheOperations.WithLabelValues("get", "error").Inc()
		return false, err
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		metrics.CacheOperations.WithLabelValues("get", "error").Inc()
		return false, err
	}
	metrics.CacheOperations.WithLabelValues("get", "hit").Inc()
	return true, nil
}

// SetJSON marshals and stores a value with the configured TTL.
func (c *Cache) SetJSON(ctx context.Context, key string, value any, ttl time.Duration) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if ttl <= 0 {
		ttl = c.ttl
	}
	if err := c.client.Set(ctx, key, raw, ttl).Err(); err != nil {
		metrics.CacheOperations.WithLabelValues("set", "error").Inc()
		return err
	}
	metrics.CacheOperations.WithLabelValues("set", "ok").Inc()
	return nil
}

// Delete removes cache keys (cache invalidation after writes).
func (c *Cache) Delete(ctx context.Context, keys ...string) {
	if len(keys) == 0 {
		return
	}
	if err := c.client.Del(ctx, keys...).Err(); err != nil {
		metrics.CacheOperations.WithLabelValues("delete", "error").Inc()
		return
	}
	metrics.CacheOperations.WithLabelValues("delete", "ok").Inc()
}

// Incr increments a counter with a TTL on first use, in one atomic round-trip.
func (c *Cache) Incr(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	const script = `
		local current = redis.call('INCR', KEYS[1])
		if current == 1 then
			redis.call('PEXPIRE', KEYS[1], ARGV[1])
		end
		return current`
	ms := ttl.Milliseconds()
	if ms <= 0 {
		ms = time.Minute.Milliseconds()
	}
	n, err := c.client.Eval(ctx, script, []string{key}, ms).Int64()
	if err != nil {
		metrics.CacheOperations.WithLabelValues("incr", "error").Inc()
		return 0, err
	}
	metrics.CacheOperations.WithLabelValues("incr", "ok").Inc()
	return n, nil
}

// IncrPersist increments a non-expiring counter (aggregate statistics).
func (c *Cache) IncrPersist(ctx context.Context, key string) (int64, error) {
	n, err := c.client.Incr(ctx, key).Result()
	if err != nil {
		metrics.CacheOperations.WithLabelValues("incr", "error").Inc()
		return 0, err
	}
	metrics.CacheOperations.WithLabelValues("incr", "ok").Inc()
	return n, nil
}

// SetNX stores a value only if the key does not exist.
func (c *Cache) SetNX(ctx context.Context, key string, value any, ttl time.Duration) (bool, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	ok, err := c.client.SetNX(ctx, key, raw, ttl).Result()
	if err != nil {
		metrics.CacheOperations.WithLabelValues("setnx", "error").Inc()
		return false, err
	}
	if ok {
		metrics.CacheOperations.WithLabelValues("setnx", "ok").Inc()
	}
	return ok, nil
}

// DenyAccessToken blacklists a JWT id until it would have expired anyway.
func (c *Cache) DenyAccessToken(ctx context.Context, jti string, ttl time.Duration) error {
	if ttl <= 0 {
		return nil
	}
	return c.client.Set(ctx, denyKey(jti), "1", ttl).Err()
}

// IsAccessTokenDenied reports whether a token was revoked by logout.
func (c *Cache) IsAccessTokenDenied(ctx context.Context, jti string) bool {
	n, err := c.client.Exists(ctx, denyKey(jti)).Result()
	return err == nil && n > 0
}

// DenySession invalidates every access token bound to a revoked refresh session.
func (c *Cache) DenySession(ctx context.Context, sessionID string, ttl time.Duration) error {
	if ttl <= 0 || sessionID == "" {
		return nil
	}
	return c.client.Set(ctx, denySessionKey(sessionID), "1", ttl).Err()
}

// IsSessionDenied reports whether the session was explicitly revoked.
func (c *Cache) IsSessionDenied(ctx context.Context, sessionID string) bool {
	if sessionID == "" {
		return false
	}
	n, err := c.client.Exists(ctx, denySessionKey(sessionID)).Result()
	return err == nil && n > 0
}

func denyKey(jti string) string { return "auth:deny:" + jti }

func denySessionKey(sessionID string) string { return "auth:sid:deny:" + sessionID }
