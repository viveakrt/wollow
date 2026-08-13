package api

import (
	"context"
	"sync"
	"time"
)

// Long-running work (mailbox sync, classifying thousands of messages) cannot
// run inside a request: proxies time out and a client disconnect would cancel
// the request context mid-pass. Jobs run detached and are polled for status.

type JobState struct {
	Running    bool   `json:"running"`
	StartedAt  string `json:"startedAt,omitempty"`
	FinishedAt string `json:"finishedAt,omitempty"`
	Error      string `json:"error,omitempty"`
	// Detail carries job-specific results (sync counts, classified count).
	Detail any `json:"detail,omitempty"`
}

type jobRunner struct {
	mu    sync.Mutex
	state map[string]*JobState
}

func newJobRunner() *jobRunner {
	return &jobRunner{state: make(map[string]*JobState)}
}

// Start launches fn in the background under the given key unless a job with
// that key is already running. It reports whether it started a new job.
func (j *jobRunner) Start(key string, fn func(ctx context.Context) (any, error)) bool {
	j.mu.Lock()
	if existing, ok := j.state[key]; ok && existing.Running {
		j.mu.Unlock()
		return false
	}
	j.state[key] = &JobState{
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

func (j *jobRunner) State(key string) JobState {
	j.mu.Lock()
	defer j.mu.Unlock()
	if state, ok := j.state[key]; ok {
		return *state
	}
	return JobState{}
}
