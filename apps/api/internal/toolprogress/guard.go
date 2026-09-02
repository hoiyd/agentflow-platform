package toolprogress

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
)

const CurrentVersion = "tool-progress-guard-v1"

type Action string

const (
	ActionAllow     Action = "allow"
	ActionWarn      Action = "warn"
	ActionBlockCall Action = "block_call"
	ActionHaltTurn  Action = "halt_turn"
)

type Rule string

const (
	RuleRepeatedFailure Rule = "repeated_typed_failure"
	RuleRepeatedResult  Rule = "repeated_read_only_result"
	RuleAlternatingLoop Rule = "alternating_loop"
)

// Config is frozen in a Runtime Snapshot so Resume applies the same no-progress
// thresholds that governed the original Run.
type Config struct {
	Version    string `json:"version"`
	Enabled    bool   `json:"enabled"`
	WarnAfter  int    `json:"warn_after"`
	BlockAfter int    `json:"block_after"`
	HaltAfter  int    `json:"halt_after"`
	HistoryMax int    `json:"history_max"`
}

func DefaultConfig() Config {
	return Config{
		Version: CurrentVersion, Enabled: true,
		WarnAfter: 2, BlockAfter: 4, HaltAfter: 5, HistoryMax: 8,
	}
}

func DisabledConfig() Config {
	config := DefaultConfig()
	config.Enabled = false
	return config
}

func NormalizeConfig(config Config) Config {
	if strings.TrimSpace(config.Version) == "" {
		config.Version = CurrentVersion
	}
	if config.WarnAfter <= 0 {
		config.WarnAfter = 2
	}
	if config.BlockAfter <= config.WarnAfter {
		config.BlockAfter = config.WarnAfter + 2
	}
	if config.HaltAfter <= config.BlockAfter {
		config.HaltAfter = config.BlockAfter + 1
	}
	if config.HistoryMax < 4 {
		config.HistoryMax = 8
	}
	return config
}

func ValidateConfig(config Config) bool {
	return config.Version == CurrentVersion && config.WarnAfter > 0 &&
		config.BlockAfter > config.WarnAfter && config.HaltAfter > config.BlockAfter &&
		config.HistoryMax >= 4
}

type Call struct {
	Source             string
	Tool               string
	DefinitionRevision string
	ArgumentsHash      string
}

type Outcome struct {
	ErrorCode       string
	ErrorCategory   string
	EncodedResult   []byte
	ReadOnly        bool
	SuccessfulWrite bool
}

// Decision contains only stable hashes and classifications. It is safe to
// persist in Replay without exposing Tool arguments or result content.
type Decision struct {
	Version            string `json:"version"`
	Rule               Rule   `json:"rule,omitempty"`
	Action             Action `json:"action"`
	Count              int    `json:"count"`
	Reason             string `json:"reason,omitempty"`
	SignatureHash      string `json:"signature_hash,omitempty"`
	OutcomeFingerprint string `json:"outcome_fingerprint,omitempty"`
	Trackable          bool   `json:"trackable"`
	Executed           bool   `json:"executed"`
}

type Record struct {
	Decision Decision
}

type observation struct {
	signature   string
	fingerprint string
	rule        Rule
}

func (o observation) key() string { return o.signature + ":" + o.fingerprint }

// Guard owns progress history for one Run. It is safe for parallel read-only
// Tool batches; all modes share it through the common Tool Executor context.
type Guard struct {
	mu              sync.Mutex
	config          Config
	history         []observation
	blockedAttempts map[string]int
}

func New(config Config) *Guard {
	return &Guard{config: NormalizeConfig(config), blockedAttempts: map[string]int{}}
}

func (g *Guard) Config() Config {
	if g == nil {
		return DisabledConfig()
	}
	return g.config
}

// Before blocks only a previously observed, stable outcome. Argument or typed
// error changes must execute once before they can establish a new pattern.
func (g *Guard) Before(call Call) Decision {
	if g == nil || !g.config.Enabled {
		return Decision{Version: CurrentVersion, Action: ActionAllow}
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	predicted, rule, count, ok := g.predict(call)
	if !ok || count < g.config.BlockAfter {
		return Decision{Version: g.config.Version, Action: ActionAllow}
	}
	key := predicted.key()
	count += g.blockedAttempts[key]
	action := ActionBlockCall
	if count >= g.config.HaltAfter {
		action = ActionHaltTurn
	}
	g.blockedAttempts[key]++
	return decision(g.config.Version, predicted, rule, action, count, false)
}

// Observe records only typed failures and explicitly read-only identical
// results. Successful writes reset progress history and are never hashed.
func (g *Guard) Observe(call Call, outcome Outcome) Decision {
	if g == nil || !g.config.Enabled {
		return Decision{Version: CurrentVersion, Action: ActionAllow, Executed: true}
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if outcome.SuccessfulWrite {
		g.resetLocked()
		return Decision{Version: g.config.Version, Action: ActionAllow, Executed: true}
	}
	item, ok := observed(call, outcome)
	if !ok {
		g.resetLocked()
		return Decision{Version: g.config.Version, Action: ActionAllow, Executed: true}
	}
	g.history = append(g.history, item)
	if overflow := len(g.history) - g.config.HistoryMax; overflow > 0 {
		g.history = append([]observation(nil), g.history[overflow:]...)
	}
	delete(g.blockedAttempts, item.key())
	rule, count := g.currentPattern()
	action := ActionAllow
	if count >= g.config.WarnAfter {
		action = ActionWarn
	}
	return decision(g.config.Version, item, rule, action, count, true)
}

// Restore rebuilds the bounded state from terminal Tool events after a process
// restart. Malformed or untrackable historical records safely reset the chain.
func (g *Guard) Restore(records []Record) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.resetLocked()
	for _, record := range records {
		d := record.Decision
		if d.Version != g.config.Version || !d.Trackable || d.SignatureHash == "" || d.OutcomeFingerprint == "" {
			if d.Executed {
				g.resetLocked()
			}
			continue
		}
		item := observation{signature: d.SignatureHash, fingerprint: d.OutcomeFingerprint, rule: d.Rule}
		if d.Executed {
			g.history = append(g.history, item)
			if overflow := len(g.history) - g.config.HistoryMax; overflow > 0 {
				g.history = append([]observation(nil), g.history[overflow:]...)
			}
			delete(g.blockedAttempts, item.key())
			continue
		}
		if d.Action == ActionBlockCall || d.Action == ActionHaltTurn {
			g.blockedAttempts[item.key()]++
		}
	}
}

func (g *Guard) Reset() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.resetLocked()
}

func (g *Guard) predict(call Call) (observation, Rule, int, bool) {
	if len(g.history) == 0 {
		return observation{}, "", 0, false
	}
	signature := Signature(call)
	last := g.history[len(g.history)-1]
	if last.signature == signature {
		count := repeatedTail(g.history, last.key()) + 1
		return last, last.rule, count, true
	}
	if len(g.history) >= 2 {
		twoBack := g.history[len(g.history)-2]
		if twoBack.signature == signature && twoBack.key() != last.key() {
			count := alternatingTail(g.history) + 1
			return twoBack, RuleAlternatingLoop, count, true
		}
	}
	return observation{}, "", 0, false
}

func (g *Guard) currentPattern() (Rule, int) {
	last := g.history[len(g.history)-1]
	direct := repeatedTail(g.history, last.key())
	alternating := alternatingTail(g.history)
	if alternating > direct {
		return RuleAlternatingLoop, alternating
	}
	return last.rule, direct
}

func repeatedTail(history []observation, key string) int {
	count := 0
	for index := len(history) - 1; index >= 0 && history[index].key() == key; index-- {
		count++
	}
	return count
}

func alternatingTail(history []observation) int {
	if len(history) < 3 {
		return 0
	}
	count := 0
	for index := len(history) - 1; index >= 2; index-- {
		if history[index].key() != history[index-2].key() || history[index].key() == history[index-1].key() {
			break
		}
		count++
	}
	return count
}

func observed(call Call, outcome Outcome) (observation, bool) {
	item := observation{signature: Signature(call)}
	if outcome.ErrorCode != "" && outcome.ErrorCategory != "" {
		item.rule = RuleRepeatedFailure
		item.fingerprint = hash("error", outcome.ErrorCode, outcome.ErrorCategory)
		return item, true
	}
	if outcome.ReadOnly && len(outcome.EncodedResult) > 0 {
		item.rule = RuleRepeatedResult
		item.fingerprint = hash("result", string(outcome.EncodedResult))
		return item, true
	}
	return observation{}, false
}

func decision(version string, item observation, rule Rule, action Action, count int, executed bool) Decision {
	reason := ""
	switch rule {
	case RuleRepeatedFailure:
		reason = "same Tool call produced the same typed failure without progress"
	case RuleRepeatedResult:
		reason = "same read-only Tool call produced an unchanged result"
	case RuleAlternatingLoop:
		reason = "Tool calls are alternating between the same outcomes without progress"
	}
	return Decision{
		Version: version, Rule: rule, Action: action, Count: count, Reason: reason,
		SignatureHash: item.signature, OutcomeFingerprint: item.fingerprint,
		Trackable: true, Executed: executed,
	}
}

func Signature(call Call) string {
	return hash(strings.TrimSpace(call.Source), strings.TrimSpace(call.Tool),
		strings.TrimSpace(call.DefinitionRevision), strings.TrimSpace(call.ArgumentsHash))
}

func hash(parts ...string) string {
	digest := sha256.New()
	for _, part := range parts {
		_, _ = digest.Write([]byte(part))
		_, _ = digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func (g *Guard) resetLocked() {
	g.history = nil
	g.blockedAttempts = map[string]int{}
}

type guardContextKey struct{}

func WithGuard(ctx context.Context, guard *Guard) context.Context {
	if guard == nil {
		return ctx
	}
	return context.WithValue(ctx, guardContextKey{}, guard)
}

func FromContext(ctx context.Context) *Guard {
	if ctx == nil {
		return nil
	}
	guard, _ := ctx.Value(guardContextKey{}).(*Guard)
	return guard
}
