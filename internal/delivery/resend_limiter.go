package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type resendRequestWaiter struct {
	ctx   context.Context
	ready chan struct{}
}

type prioritizedResendLimiter struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
	platform []*resendRequestWaiter
	campaign []*resendRequestWaiter
	wake     chan struct{}
	running  bool
}

var sharedResendLimiters = struct {
	sync.Mutex
	limiters map[string]*prioritizedResendLimiter
}{limiters: make(map[string]*prioritizedResendLimiter)}

var sharedResendRedis = struct {
	sync.RWMutex
	client redis.UniversalClient
}{}

var distributedResendWaitScript = redis.NewScript(`
redis.call("ZADD", KEYS[1], "NX", ARGV[1], ARGV[2])
redis.call("PEXPIRE", KEYS[1], 86400000)
local next_at = tonumber(redis.call("GET", KEYS[2]) or "0")
local now = tonumber(ARGV[3])
if now < next_at then
  return next_at - now
end
local first = redis.call("ZRANGE", KEYS[1], 0, 0)
if first[1] == ARGV[2] then
  redis.call("ZREM", KEYS[1], ARGV[2])
  redis.call("SET", KEYS[2], now + tonumber(ARGV[4]), "PX", 86400000)
  return 0
end
return 10
`)

// ConfigureSharedResendRedis installs the Redis client used by all services
// to coordinate the account-wide Resend request queue.
func ConfigureSharedResendRedis(ctx context.Context, address string) (redis.UniversalClient, error) {
	options, err := redis.ParseURL(address)
	if err != nil {
		return nil, fmt.Errorf("parse Resend limiter Redis URL: %w", err)
	}
	client := redis.NewClient(options)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("connect Resend limiter Redis: %w", err)
	}
	sharedResendRedis.Lock()
	sharedResendRedis.client = client
	sharedResendRedis.Unlock()
	return client, nil
}

// WaitResendRequest shares one per-key request budget across services while
// allowing platform mail to take the next available slot before campaigns.
func WaitResendRequest(ctx context.Context, apiKey string, requestsPerSecond int, platform bool) error {
	if requestsPerSecond < 1 {
		requestsPerSecond = 8
	}
	digest := sha256.Sum256([]byte(apiKey))
	key := hex.EncodeToString(digest[:])
	sharedResendRedis.RLock()
	redisClient := sharedResendRedis.client
	sharedResendRedis.RUnlock()
	if redisClient != nil {
		return waitDistributedResendRequest(ctx, redisClient, key, requestsPerSecond, platform)
	}
	return waitLocalResendRequest(ctx, key, requestsPerSecond, platform)
}

func waitDistributedResendRequest(ctx context.Context, client redis.UniversalClient, key string, requestsPerSecond int, platform bool) error {
	requestID := uuid.NewString()
	queueKey := "sendwise:resend:" + key + ":queue"
	nextKey := "sendwise:resend:" + key + ":next"
	priority := 1
	if platform {
		priority = 0
	}
	score := float64(priority)*1e15 + float64(time.Now().UnixMilli())
	interval := int64(time.Second / time.Duration(requestsPerSecond) / time.Millisecond)
	if interval < 1 {
		interval = 1
	}
	for {
		wait, err := distributedResendWaitScript.Run(ctx, client,
			[]string{queueKey, nextKey},
			score, requestID, time.Now().UnixMilli(), interval,
		).Int64()
		if err != nil {
			_ = client.ZRem(context.Background(), queueKey, requestID).Err()
			return fmt.Errorf("wait for shared Resend request limit: %w", err)
		}
		if wait == 0 {
			return nil
		}
		delay := time.Duration(wait) * time.Millisecond
		if delay > 25*time.Millisecond {
			delay = 25 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			_ = client.ZRem(context.Background(), queueKey, requestID).Err()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func waitLocalResendRequest(ctx context.Context, key string, requestsPerSecond int, platform bool) error {
	sharedResendLimiters.Lock()
	limiter := sharedResendLimiters.limiters[key]
	if limiter == nil {
		limiter = &prioritizedResendLimiter{wake: make(chan struct{}, 1)}
		sharedResendLimiters.limiters[key] = limiter
	}
	sharedResendLimiters.Unlock()

	waiter := &resendRequestWaiter{ctx: ctx, ready: make(chan struct{})}
	limiter.mu.Lock()
	limiter.interval = time.Second / time.Duration(requestsPerSecond)
	if platform {
		limiter.platform = append(limiter.platform, waiter)
	} else {
		limiter.campaign = append(limiter.campaign, waiter)
	}
	if !limiter.running {
		limiter.running = true
		go limiter.run()
	}
	limiter.mu.Unlock()
	limiter.signal()

	select {
	case <-waiter.ready:
		return nil
	case <-ctx.Done():
		limiter.signal()
		return ctx.Err()
	}
}

func (l *prioritizedResendLimiter) run() {
	for {
		l.mu.Lock()
		l.discardCanceled()
		if len(l.platform) == 0 && len(l.campaign) == 0 {
			l.running = false
			l.mu.Unlock()
			return
		}
		delay := time.Until(l.next)
		l.mu.Unlock()

		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-l.wake:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			}
		}

		l.mu.Lock()
		l.discardCanceled()
		if len(l.platform) == 0 && len(l.campaign) == 0 {
			l.mu.Unlock()
			continue
		}
		now := time.Now()
		if now.Before(l.next) {
			l.mu.Unlock()
			continue
		}
		var waiter *resendRequestWaiter
		if len(l.platform) > 0 {
			waiter, l.platform = l.platform[0], l.platform[1:]
		} else {
			waiter, l.campaign = l.campaign[0], l.campaign[1:]
		}
		l.next = now.Add(l.interval)
		l.mu.Unlock()
		close(waiter.ready)
	}
}

func (l *prioritizedResendLimiter) discardCanceled() {
	l.platform = discardCanceledWaiters(l.platform)
	l.campaign = discardCanceledWaiters(l.campaign)
}

func discardCanceledWaiters(waiters []*resendRequestWaiter) []*resendRequestWaiter {
	active := waiters[:0]
	for _, waiter := range waiters {
		if waiter.ctx.Err() == nil {
			active = append(active, waiter)
		}
	}
	return active
}

func (l *prioritizedResendLimiter) signal() {
	select {
	case l.wake <- struct{}{}:
	default:
	}
}
