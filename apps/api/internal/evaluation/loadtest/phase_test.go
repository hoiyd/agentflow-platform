package loadtest

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/memory"
	"agentflow-platform/apps/api/internal/tool"
)

type phaseSpec struct {
	name               string
	count              int
	arrival            time.Duration
	sharedConversation bool
}

type phaseRecorder struct {
	mu                  sync.Mutex
	report              phaseReport
	queueWaits          []time.Duration
	serviceTimes        []time.Duration
	active              int
	activeConversations map[string]int
}

func newPhaseRecorder(spec phaseSpec) *phaseRecorder {
	return &phaseRecorder{report: phaseReport{
		Name: spec.name, ArrivalIntervalMS: spec.arrival.Milliseconds(), FailureCodes: map[string]int{},
	}, activeConversations: map[string]int{}}
}

func (r *phaseRecorder) offer() {
	r.mu.Lock()
	r.report.Offered++
	r.mu.Unlock()
}

func (r *phaseRecorder) accept() {
	r.mu.Lock()
	r.report.Accepted++
	r.mu.Unlock()
}

func (r *phaseRecorder) reject(err error) {
	r.mu.Lock()
	r.report.Rejected++
	r.report.FailureCodes[failure.Describe(err).Code]++
	r.mu.Unlock()
}

func (r *phaseRecorder) start(conversationID string, queueWait time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queueWaits = append(r.queueWaits, queueWait)
	r.active++
	r.report.PeakActiveRuns = max(r.report.PeakActiveRuns, r.active)
	r.activeConversations[conversationID]++
	r.report.PeakSameConversation = max(r.report.PeakSameConversation, r.activeConversations[conversationID])
}

func (r *phaseRecorder) finish(conversationID string, serviceTime time.Duration, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.serviceTimes = append(r.serviceTimes, serviceTime)
	r.active--
	r.activeConversations[conversationID]--
	if err == nil {
		r.report.Completed++
		return
	}
	r.report.Failed++
	r.report.FailureCodes[failure.Describe(err).Code]++
}

func (r *phaseRecorder) startFailed(queueWait time.Duration, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queueWaits = append(r.queueWaits, queueWait)
	r.report.Failed++
	r.report.FailureCodes[failure.Describe(err).Code]++
}

func (r *phaseRecorder) snapshot(duration time.Duration) phaseReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.report.DurationMS = duration.Milliseconds()
	if r.report.Offered > 0 {
		r.report.RejectionRate = float64(r.report.Rejected) / float64(r.report.Offered)
	}
	r.report.QueueWaitP50MS = percentileMS(r.queueWaits, 50)
	r.report.QueueWaitP95MS = percentileMS(r.queueWaits, 95)
	r.report.ServiceP50MS = percentileMS(r.serviceTimes, 50)
	r.report.ServiceP95MS = percentileMS(r.serviceTimes, 95)
	return r.report
}

func (h *harness) runPhase(spec phaseSpec) phaseReport {
	recorder := newPhaseRecorder(spec)
	started := time.Now()
	var workers sync.WaitGroup
	for index := 0; index < spec.count; index++ {
		recorder.offer()
		reservation, err := h.controller.Reserve()
		if err != nil {
			recorder.reject(err)
		} else {
			recorder.accept()
			conversationID := fmt.Sprintf("%s-%d", spec.name, index)
			if spec.sharedConversation {
				conversationID = spec.name + "-shared"
			}
			workers.Add(1)
			go func(task int, writerID string) {
				defer workers.Done()
				h.runReservation(spec.name, task, writerID, reservation, recorder)
			}(index, conversationID)
		}
		if spec.arrival > 0 {
			time.Sleep(spec.arrival)
		}
	}
	workers.Wait()
	return recorder.snapshot(time.Since(started))
}

func (h *harness) runReservation(phase string, task int, writerID string, reservation *concurrency.Reservation, recorder *phaseRecorder) {
	ctx, cancel := context.WithTimeout(context.Background(), h.config.RequestTimeout)
	defer cancel()
	waitStarted := time.Now()
	release, err := reservation.Start(ctx, writerID)
	queueWait := time.Since(waitStarted)
	if err != nil {
		recorder.startFailed(queueWait, err)
		return
	}
	defer release()
	recorder.start(writerID, queueWait)

	serviceStarted := time.Now()
	run, message, err := h.newRun(512, phase, task)
	if err == nil {
		ctx = budget.WithController(ctx, budget.NewTracker(h.store, nil, run))
		_, err = h.client.CompleteTextDetailed(ctx, "Return ok.", fmt.Sprintf("phase=%s task=%d", phase, task))
		if err == nil {
			result := h.toolExecutor.Execute(ctx, tool.ExecutionRequest{
				CallID: fmt.Sprintf("tool-%s-%d", phase, task), RunID: run.ID,
				Tool: "load_probe", Arguments: json.RawMessage(`{}`),
			})
			if result.Error != nil {
				err = result.Error
			}
		}
	}
	serviceTime := time.Since(serviceStarted)
	recorder.finish(writerID, serviceTime, err)
	if err != nil {
		return
	}
	if syncErr := h.memoryProvider.SyncTurn(memory.TurnSyncRequest{RunID: run.ID, Message: message}); syncErr != nil {
		h.memoryRejected.Add(1)
	} else {
		h.memoryAccepted.Add(1)
	}
}

func percentileMS(values []time.Duration, percentile int) int64 {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]time.Duration(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	index := (len(ordered)*percentile+99)/100 - 1
	return ordered[max(0, min(index, len(ordered)-1))].Milliseconds()
}

func updatePeak(target *atomic.Int64, value int64) {
	for current := target.Load(); value > current && !target.CompareAndSwap(current, value); current = target.Load() {
	}
}
