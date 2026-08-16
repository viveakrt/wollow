// Package jobs runs detached background work and reports its progress.
//
// Long-running work (mailbox sync, classifying thousands of messages or
// transactions) cannot run inside a request: proxies time out and a client
// disconnect would cancel the request context mid-pass. Jobs run detached and
// are polled for status.
//
// Both products use this: Mail for sync and message classification, Money for
// transaction classification. It lives in platform so the two cannot drift
// into subtly different definitions of "is it still running".
package jobs

import (
	"context"
	"sync"
	"time"
)

type State struct {
	Running    bool   `json:"running"`
	StartedAt  string `json:"startedAt,omitempty"`
	FinishedAt string `json:"finishedAt,omitempty"`
	Error      string `json:"error,omitempty"`
	// Detail carries job-specific results (sync counts, classified count).
	Detail any `json:"detail,omitempty"`
	// Progress is updated while the job runs, so a long action can show how
	// far along it is instead of an indefinite spinner. Nil for jobs that
	// don't report it.
	Progress *Progress `json:"progress,omitempty"`
}

// Progress is a countable unit of work in flight.
type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
	// Label describes the unit, e.g. "messages", so the client doesn't have to
	// guess what it is counting.
	Label string `json:"label,omitempty"`
}

type Runner struct {
	mu    sync.Mutex
	state map[string]*State
}

func NewRunner() *Runner {
	return &Runner{state: make(map[string]*State)}
}

// Start launches fn in the background under the given key unless a job with
// that key is already running. It reports whether it started a new job.
func (j *Runner) Start(key string, fn func(ctx context.Context) (any, error)) bool {
	j.mu.Lock()
	if existing, ok := j.state[key]; ok && existing.Running {
		j.mu.Unlock()
		return false
	}
	j.state[key] = &State{
		Running:   true,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	j.mu.Unlock()

	go func() {
		// Detached context: this must outlive the request that started it.
		detail, err := fn(context.Background())

		j.mu.Lock()
		defer j.mu.Unlock()
		state := j.state[key]
		state.Running = false
		state.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		state.Detail = detail
		if err != nil {
			state.Error = err.Error()
		} else {
			state.Error = ""
		}
	}()

	return true
}

// Report updates a running job's progress. Safe to call from the job's own
// goroutine while a client polls State concurrently.
func (j *Runner) Report(key string, done, total int, label string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	state, ok := j.state[key]
	if !ok {
		return
	}
	state.Progress = &Progress{Done: done, Total: total, Label: label}
}

func (j *Runner) State(key string) State {
	j.mu.Lock()
	defer j.mu.Unlock()
	if state, ok := j.state[key]; ok {
		// Copy the progress pointer's contents too: handing the caller the
		// live pointer would let it read a value being written.
		snapshot := *state
		if state.Progress != nil {
			progress := *state.Progress
			snapshot.Progress = &progress
		}
		return snapshot
	}
	return State{}
}
