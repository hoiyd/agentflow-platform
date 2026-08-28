// Package delegation owns bounded in-process child Run admission and
// cancellation. Durable parent-child state lives in the store; this controller
// deliberately has no queue and is not a persistence authority.
package delegation

import (
	"context"
	"sync"

	"agentflow-platform/apps/api/internal/failure"
)

var (
	ErrCapacity = failure.New(failure.Definition{Message: "child run capacity is exhausted", Info: failure.Info{
		Code: "child_run_capacity_exhausted", Source: "delegation", Category: failure.CategoryCapacity, Retryable: true,
	}})
	ErrParentCapacity = failure.New(failure.Definition{Message: "parent child run capacity is exhausted", Info: failure.Info{
		Code: "parent_child_run_capacity_exhausted", Source: "delegation", Category: failure.CategoryCapacity, Retryable: true,
	}})
	ErrDepth = failure.New(failure.Definition{Message: "child run delegation depth exceeded", Info: failure.Info{
		Code: "child_run_depth_exceeded", Source: "delegation", Category: failure.CategoryValidation, Retryable: false,
	}})
)

type Options struct {
	MaxConcurrent int
	MaxPerParent  int
	MaxDepth      int
}

type Controller struct {
	mu           sync.Mutex
	max          int
	maxPerParent int
	maxDepth     int
	active       int
	perParent    map[string]int
	children     map[string]map[string]context.CancelCauseFunc
}

type Reservation struct {
	controller *Controller
	parentID   string
	childID    string
	once       sync.Once
}

func NewController(options Options) *Controller {
	if options.MaxConcurrent <= 0 {
		options.MaxConcurrent = 2
	}
	if options.MaxPerParent <= 0 {
		options.MaxPerParent = 1
	}
	if options.MaxDepth <= 0 {
		options.MaxDepth = 1
	}
	return &Controller{
		max: options.MaxConcurrent, maxPerParent: options.MaxPerParent, maxDepth: options.MaxDepth,
		perParent: map[string]int{}, children: map[string]map[string]context.CancelCauseFunc{},
	}
}

// Reserve never waits. This prevents child work from forming a hidden second
// queue behind the top-level Run controller.
func (c *Controller) Reserve(parentID string, depth int) (*Reservation, error) {
	if c == nil {
		return &Reservation{}, nil
	}
	if depth > c.maxDepth {
		return nil, ErrDepth
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.perParent[parentID] >= c.maxPerParent {
		return nil, ErrParentCapacity
	}
	if c.active >= c.max {
		return nil, ErrCapacity
	}
	c.active++
	c.perParent[parentID]++
	return &Reservation{controller: c, parentID: parentID}, nil
}

func (r *Reservation) Bind(childID string, cancel context.CancelCauseFunc) {
	if r == nil || r.controller == nil || childID == "" || cancel == nil {
		return
	}
	r.controller.mu.Lock()
	defer r.controller.mu.Unlock()
	children := r.controller.children[r.parentID]
	if children == nil {
		children = map[string]context.CancelCauseFunc{}
		r.controller.children[r.parentID] = children
	}
	children[childID] = cancel
	r.childID = childID
}

func (r *Reservation) Release() {
	if r == nil || r.controller == nil {
		return
	}
	r.once.Do(func() {
		c := r.controller
		c.mu.Lock()
		defer c.mu.Unlock()
		c.active--
		c.perParent[r.parentID]--
		if c.perParent[r.parentID] == 0 {
			delete(c.perParent, r.parentID)
		}
		if children := c.children[r.parentID]; children != nil {
			delete(children, r.childID)
			if len(children) == 0 {
				delete(c.children, r.parentID)
			}
		}
	})
}

func (c *Controller) CancelParent(parentID string, cause error) {
	if c == nil {
		return
	}
	c.mu.Lock()
	cancels := make([]context.CancelCauseFunc, 0, len(c.children[parentID]))
	for _, cancel := range c.children[parentID] {
		cancels = append(cancels, cancel)
	}
	c.mu.Unlock()
	for _, cancel := range cancels {
		cancel(cause)
	}
}
