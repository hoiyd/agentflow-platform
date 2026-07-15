package concurrency

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var ErrTokenLimitExceeded = errors.New("estimated request tokens exceed the configured token bucket capacity")

type ModelOptions struct {
	MaxConcurrent     int
	RequestsPerPeriod int
	TokensPerPeriod   int
	RatePeriod        time.Duration
}

type ModelGovernor struct {
	global chan struct{}
	period time.Duration
	rpm    int
	tpm    int

	mu   sync.Mutex
	keys map[string]*keyLimiter
}

func NewModelGovernor(options ModelOptions) *ModelGovernor {
	if options.MaxConcurrent <= 0 {
		options.MaxConcurrent = 1
	}
	if options.RatePeriod <= 0 {
		options.RatePeriod = time.Minute
	}
	return &ModelGovernor{
		global: make(chan struct{}, options.MaxConcurrent),
		period: options.RatePeriod,
		rpm:    options.RequestsPerPeriod,
		tpm:    options.TokensPerPeriod,
		keys:   make(map[string]*keyLimiter),
	}
}

func (g *ModelGovernor) Acquire(ctx context.Context, apiKey string, estimatedTokens int) (func(), error) {
	if g == nil {
		return func() {}, nil
	}
	if estimatedTokens < 1 {
		estimatedTokens = 1
	}
	if strings.TrimSpace(apiKey) != "" && (g.rpm > 0 || g.tpm > 0) {
		if err := g.keyLimiter(apiKey).take(ctx, estimatedTokens); err != nil {
			return nil, err
		}
	}

	select {
	case g.global <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			<-g.global
		})
	}, nil
}

func (g *ModelGovernor) keyLimiter(apiKey string) *keyLimiter {
	digest := sha256.Sum256([]byte(apiKey))
	keyID := hex.EncodeToString(digest[:])
	g.mu.Lock()
	defer g.mu.Unlock()
	if limiter := g.keys[keyID]; limiter != nil {
		return limiter
	}
	now := time.Now()
	limiter := &keyLimiter{
		requests: newTokenBucket(g.rpm, g.period, now),
		tokens:   newTokenBucket(g.tpm, g.period, now),
	}
	g.keys[keyID] = limiter
	return limiter
}

type keyLimiter struct {
	mu       sync.Mutex
	requests tokenBucket
	tokens   tokenBucket
}

func (l *keyLimiter) take(ctx context.Context, tokenCost int) error {
	for {
		now := time.Now()
		l.mu.Lock()
		requestWait, _ := l.requests.waitDuration(now, 1)
		tokenWait, tokenOK := l.tokens.waitDuration(now, float64(tokenCost))
		if !tokenOK {
			l.mu.Unlock()
			return fmt.Errorf("%w: estimated=%d", ErrTokenLimitExceeded, tokenCost)
		}
		if requestWait <= 0 && tokenWait <= 0 {
			l.requests.consume(1)
			l.tokens.consume(float64(tokenCost))
			l.mu.Unlock()
			return nil
		}
		wait := maxDuration(requestWait, tokenWait)
		l.mu.Unlock()

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

type tokenBucket struct {
	capacity float64
	tokens   float64
	refill   float64
	last     time.Time
}

func newTokenBucket(capacity int, period time.Duration, now time.Time) tokenBucket {
	if capacity <= 0 {
		return tokenBucket{}
	}
	value := float64(capacity)
	return tokenBucket{capacity: value, tokens: value, refill: value / period.Seconds(), last: now}
}

func (b *tokenBucket) waitDuration(now time.Time, cost float64) (time.Duration, bool) {
	if b.capacity == 0 {
		return 0, true
	}
	if cost > b.capacity {
		return 0, false
	}
	b.refillAt(now)
	if b.tokens >= cost {
		return 0, true
	}
	seconds := (cost - b.tokens) / b.refill
	return time.Duration(seconds * float64(time.Second)), true
}

func (b *tokenBucket) consume(cost float64) {
	if b.capacity > 0 {
		b.tokens -= cost
	}
}

func (b *tokenBucket) refillAt(now time.Time) {
	if b.capacity == 0 || !now.After(b.last) {
		return
	}
	b.tokens = min(b.capacity, b.tokens+now.Sub(b.last).Seconds()*b.refill)
	b.last = now
}

func maxDuration(left, right time.Duration) time.Duration {
	if left > right {
		return left
	}
	return right
}
