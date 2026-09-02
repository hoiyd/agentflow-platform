package tooltest

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tools"
)

type EffectPhase string

const (
	EffectIntentPersisted   EffectPhase = "intent_persisted"
	EffectApplied           EffectPhase = "effect_applied"
	EffectSettlementPending EffectPhase = "settlement_pending"
)

type phaseGate struct {
	reached     chan struct{}
	release     chan struct{}
	reachedOnce sync.Once
	releaseOnce sync.Once
}

// EffectGateFixture is a deterministic test double for the durable
// intent -> external effect -> settlement protocol. Tests can inspect durable
// state at each boundary without sleeps or a real external dependency.
type EffectGateFixture struct {
	mu           sync.Mutex
	record       domain.ToolEffectRecord
	gates        map[EffectPhase]*phaseGate
	handlerCalls atomic.Int32
	beginErr     error
	completeErr  error
}

func NewEffectGateFixture() *EffectGateFixture {
	fixture := &EffectGateFixture{gates: make(map[EffectPhase]*phaseGate)}
	for _, phase := range []EffectPhase{EffectIntentPersisted, EffectApplied, EffectSettlementPending} {
		fixture.gates[phase] = &phaseGate{reached: make(chan struct{}), release: make(chan struct{})}
	}
	return fixture
}

func (f *EffectGateFixture) Binding(name string) tools.Binding {
	return tools.Binding{
		Descriptor: tools.Descriptor{
			Name: name, Parameters: tools.ObjectSchema(map[string]any{
				"value": map[string]any{"type": "string", "minLength": 1},
			}, []string{"value"}),
			SideEffect: tools.SideEffectPolicy{Mode: tools.SideEffectExternal},
			Security:   testExternalWriteCapability(),
		},
		Handler: func(ctx context.Context, arguments json.RawMessage) (any, error) {
			f.handlerCalls.Add(1)
			if err := f.reach(ctx, EffectApplied); err != nil {
				return nil, err
			}
			return map[string]any{"committed": true}, nil
		},
	}
}

func (f *EffectGateFixture) BeginToolEffect(record domain.ToolEffectRecord) (domain.ToolEffectRecord, bool, error) {
	if f.beginErr != nil {
		return domain.ToolEffectRecord{}, false, f.beginErr
	}
	f.mu.Lock()
	if f.record.IdempotencyKey != "" {
		existing := f.record
		f.mu.Unlock()
		return existing, false, nil
	}
	record.Status = domain.ToolEffectExecuting
	f.record = record
	f.mu.Unlock()
	f.waitForRelease(EffectIntentPersisted)
	return record, true, nil
}

func (f *EffectGateFixture) CompleteToolEffect(key string, result []byte) (domain.ToolEffectRecord, error) {
	f.waitForRelease(EffectSettlementPending)
	if f.completeErr != nil {
		return domain.ToolEffectRecord{}, f.completeErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.record.IdempotencyKey != key {
		return domain.ToolEffectRecord{}, errors.New("unexpected effect key")
	}
	f.record.Status = domain.ToolEffectCommitted
	f.record.Result = append([]byte(nil), result...)
	return f.record, nil
}

func (f *EffectGateFixture) MarkToolEffectNeedsReconciliation(key, message string) (domain.ToolEffectRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.record.IdempotencyKey != key {
		return domain.ToolEffectRecord{}, errors.New("unexpected effect key")
	}
	f.record.Status = domain.ToolEffectNeedsReconciliation
	f.record.Error = message
	return f.record, nil
}

func (f *EffectGateFixture) Wait(ctx context.Context, phase EffectPhase) error {
	gate, ok := f.gates[phase]
	if !ok {
		return errors.New("unknown effect phase")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-gate.reached:
		return nil
	}
}

func (f *EffectGateFixture) Release(phase EffectPhase) {
	if gate, ok := f.gates[phase]; ok {
		gate.releaseOnce.Do(func() { close(gate.release) })
	}
}

func (f *EffectGateFixture) Record() domain.ToolEffectRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	cloned := f.record
	cloned.Result = append([]byte(nil), cloned.Result...)
	return cloned
}

func (f *EffectGateFixture) HandlerCalls() int {
	return int(f.handlerCalls.Load())
}

func (f *EffectGateFixture) FailBegin(err error)    { f.beginErr = err }
func (f *EffectGateFixture) FailComplete(err error) { f.completeErr = err }

func (f *EffectGateFixture) reach(ctx context.Context, phase EffectPhase) error {
	gate, ok := f.gates[phase]
	if !ok {
		return errors.New("unknown effect phase")
	}
	gate.reachedOnce.Do(func() { close(gate.reached) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-gate.release:
		return nil
	}
}

func (f *EffectGateFixture) waitForRelease(phase EffectPhase) {
	gate := f.gates[phase]
	gate.reachedOnce.Do(func() { close(gate.reached) })
	<-gate.release
}
