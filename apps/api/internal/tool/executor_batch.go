package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
)

// ExecuteBatch preserves input order. A serial or unresolved tool makes the whole
// batch serial; explicitly read-only and distinct keyed groups may run in parallel.
func (e *Executor) ExecuteBatch(ctx context.Context, requests []ExecutionRequest) []ExecutionResult {
	if len(requests) == 0 {
		return nil
	}
	groups, parallel := e.concurrentGroups(requests)
	if !parallel || len(groups) == 1 {
		return e.finishBatch(ctx, requests, e.executeSequential(ctx, requests))
	}

	results := make([]ExecutionResult, len(requests))
	jobs := make(chan []indexedExecutionRequest)
	workerCount := min(e.maxConcurrency, len(groups))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for group := range jobs {
				for _, item := range group {
					results[item.index] = e.execute(ctx, item.request, false)
				}
			}
		}()
	}
	for _, group := range groups {
		jobs <- group
	}
	close(jobs)
	workers.Wait()
	return e.finishBatch(ctx, requests, results)
}

type indexedExecutionRequest struct {
	index   int
	request ExecutionRequest
}

func (e *Executor) executeSequential(ctx context.Context, requests []ExecutionRequest) []ExecutionResult {
	results := make([]ExecutionResult, len(requests))
	for index, request := range requests {
		results[index] = e.execute(ctx, request, false)
	}
	return results
}

func (e *Executor) concurrentGroups(requests []ExecutionRequest) ([][]indexedExecutionRequest, bool) {
	if e.catalog == nil {
		return nil, false
	}
	groups := make([][]indexedExecutionRequest, 0, len(requests))
	keyedGroups := make(map[string]int)
	for index, request := range requests {
		binding, ok := e.catalog.Resolve(request.Tool)
		if !ok {
			return nil, false
		}
		item := indexedExecutionRequest{index: index, request: request}
		switch binding.Descriptor.Concurrency.Mode {
		case ConcurrencyReadOnly:
			groups = append(groups, []indexedExecutionRequest{item})
		case ConcurrencyKeyed:
			key, ok := concurrencyKey(binding.Descriptor.Concurrency, request.Arguments)
			if !ok {
				return nil, false
			}
			groupIndex, exists := keyedGroups[key]
			if !exists {
				groupIndex = len(groups)
				keyedGroups[key] = groupIndex
				groups = append(groups, nil)
			}
			groups[groupIndex] = append(groups[groupIndex], item)
		default:
			return nil, false
		}
	}
	return groups, true
}

func concurrencyKey(policy ConcurrencyPolicy, arguments json.RawMessage) (string, bool) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(normalizeArguments(arguments), &object); err != nil {
		return "", false
	}
	raw := bytes.TrimSpace(object[policy.KeyArgument])
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", false
	}
	var scalar any
	if err := json.Unmarshal(raw, &scalar); err != nil {
		return "", false
	}
	switch scalar.(type) {
	case string, float64, bool:
		return strings.TrimSpace(policy.KeyArgument) + ":" + string(raw), true
	default:
		return "", false
	}
}
