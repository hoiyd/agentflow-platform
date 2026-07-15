package concurrency

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"agentflow-platform/apps/api/internal/modelrequest"
)

type ModelRequestLimits struct {
	MaxConcurrent     int
	RequestsPerPeriod int
	TokensPerPeriod   int
	RatePeriod        time.Duration
}

// ModelRequestLimiter applies concurrency and per-key rate limits to each
// physical model HTTP request, including retry attempts.
type ModelRequestLimiter struct {
	global chan struct{}
	period time.Duration
	rpm    int
	tpm    int

	mu   sync.Mutex
	keys map[string]*apiKeyLimiter
}

var _ modelrequest.Limiter = (*ModelRequestLimiter)(nil)

func NewModelRequestLimiter(limits ModelRequestLimits) *ModelRequestLimiter {
	if limits.MaxConcurrent <= 0 {
		limits.MaxConcurrent = 1
	}
	if limits.RatePeriod <= 0 {
		limits.RatePeriod = time.Minute
	}
	return &ModelRequestLimiter{
		global: make(chan struct{}, limits.MaxConcurrent),
		period: limits.RatePeriod,
		rpm:    limits.RequestsPerPeriod,
		tpm:    limits.TokensPerPeriod,
		keys:   make(map[string]*apiKeyLimiter),
	}
}

func (l *ModelRequestLimiter) AcquireRequest(ctx context.Context, apiKey string, estimatedTokens int) (func(), error) {
	if l == nil {
		return func() {}, nil
	}
	if estimatedTokens < 1 {
		estimatedTokens = 1
	}
	if strings.TrimSpace(apiKey) != "" && (l.rpm > 0 || l.tpm > 0) {
		if err := l.apiKeyLimiter(apiKey).take(ctx, estimatedTokens); err != nil {
			return nil, err
		}
	}

	select {
	case l.global <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			<-l.global
		})
	}, nil
}

func (l *ModelRequestLimiter) apiKeyLimiter(apiKey string) *apiKeyLimiter {
	digest := sha256.Sum256([]byte(apiKey))
	keyID := hex.EncodeToString(digest[:])
	l.mu.Lock()
	defer l.mu.Unlock()
	if limiter := l.keys[keyID]; limiter != nil {
		return limiter
	}
	now := time.Now()
	limiter := &apiKeyLimiter{
		requests: newTokenBucket(l.rpm, l.period, now),
		tokens:   newTokenBucket(l.tpm, l.period, now),
	}
	l.keys[keyID] = limiter
	return limiter
}

type apiKeyLimiter struct {
	mu       sync.Mutex
	requests tokenBucket
	tokens   tokenBucket
}

func (l *apiKeyLimiter) take(ctx context.Context, tokenCost int) error {
	for {
		now := time.Now()
		l.mu.Lock()
		requestWait, _ := l.requests.waitDuration(now, 1)
		tokenWait, tokenOK := l.tokens.waitDuration(now, float64(tokenCost))
		if !tokenOK {
			l.mu.Unlock()
			return &modelrequest.TokenLimitError{EstimatedTokens: tokenCost, Capacity: int(l.tokens.capacity)}
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
