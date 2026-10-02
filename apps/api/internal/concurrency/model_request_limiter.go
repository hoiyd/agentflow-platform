package concurrency

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"agentflow-platform/apps/api/internal/inference/requestcontrol"
)

type ModelRequestLimits struct {
	MaxConcurrent     int
	RequestsPerPeriod int
	TokensPerPeriod   int
	RatePeriod        time.Duration
	// Zero disables owner admission for standalone/offline clients. Production
	// composition always enables it and resolves a trusted owner on every attempt.
	OwnerMaxConcurrent int
	OwnerQueueSize     int
	OwnerWaitTimeout   time.Duration
	OwnerResolver      func(context.Context) (string, error)
}

// ModelRequestLimiter applies concurrency and per-key rate limits to each
// physical model HTTP request, including retry attempts.
type ModelRequestLimiter struct {
	global chan struct{}
	period time.Duration
	rpm    int
	tpm    int

	mu          sync.Mutex
	keys        map[string]*apiKeyLimiter
	owners      map[string]*ownerLimiter
	ownerLimits ModelRequestLimits
	closed      context.Context
	closeCancel context.CancelFunc
}

var _ requestcontrol.Limiter = (*ModelRequestLimiter)(nil)

func NewModelRequestLimiter(limits ModelRequestLimits) *ModelRequestLimiter {
	if limits.MaxConcurrent <= 0 {
		limits.MaxConcurrent = 1
	}
	if limits.RatePeriod <= 0 {
		limits.RatePeriod = time.Minute
	}
	closed, closeCancel := context.WithCancel(context.Background())
	return &ModelRequestLimiter{
		global:      make(chan struct{}, limits.MaxConcurrent),
		period:      limits.RatePeriod,
		rpm:         limits.RequestsPerPeriod,
		tpm:         limits.TokensPerPeriod,
		keys:        make(map[string]*apiKeyLimiter),
		owners:      make(map[string]*ownerLimiter),
		ownerLimits: limits,
		closed:      closed, closeCancel: closeCancel,
	}
}

func (l *ModelRequestLimiter) AcquireRequest(ctx context.Context, apiKey string, estimatedTokens int) (func(), error) {
	if l == nil {
		return func() {}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Close wakes pending owner/rate/global waiters. An acquired transport keeps
	// its permits until response cleanup; shutdown never reuses an active permit.
	waitCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(l.closed, cancel)
	defer func() { stop(); cancel() }()
	ctx = waitCtx
	select {
	case <-l.closed.Done():
		return nil, &requestcontrol.OwnerAdmissionError{Code: "model_limiter_closed"}
	default:
	}
	ownerRelease, err := l.acquireOwner(ctx)
	if err != nil {
		return nil, l.waitError(err)
	}
	acquired := false
	defer func() {
		if !acquired {
			ownerRelease()
		}
	}()
	timing := requestcontrol.AttemptTimingFromContext(ctx)
	if timing != nil {
		timing.Limited = true
	}
	if estimatedTokens < 1 {
		estimatedTokens = 1
	}
	if strings.TrimSpace(apiKey) != "" && (l.rpm > 0 || l.tpm > 0) {
		started := time.Now()
		err := l.apiKeyLimiter(apiKey).take(ctx, estimatedTokens)
		if timing != nil {
			timing.RateWait = time.Since(started)
		}
		if err != nil {
			return nil, l.waitError(err)
		}
	}

	started := time.Now()
	select {
	case l.global <- struct{}{}:
	case <-ctx.Done():
		if timing != nil {
			timing.PermitWait = time.Since(started)
		}
		return nil, l.waitError(ctx.Err())
	}
	if timing != nil {
		timing.PermitWait = time.Since(started)
	}
	if err := l.waitError(ctx.Err()); err != nil {
		<-l.global
		return nil, err
	}
	// Recheck durable access after waiting, before any provider transmission.
	if resolve := l.ownerLimits.OwnerResolver; resolve != nil && l.ownerLimits.OwnerMaxConcurrent > 0 {
		if _, err := resolve(ctx); err != nil {
			<-l.global
			return nil, err
		}
	}

	var once sync.Once
	acquired = true
	return func() {
		once.Do(func() {
			<-l.global
			ownerRelease()
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
			return &requestcontrol.TokenBucketCapacityError{EstimatedTokens: tokenCost, Capacity: int(l.tokens.capacity)}
		}
		if requestWait <= 0 && tokenWait <= 0 {
			if err := ctx.Err(); err != nil {
				l.mu.Unlock()
				return err
			}
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
