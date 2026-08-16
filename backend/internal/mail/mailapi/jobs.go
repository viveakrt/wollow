package mailapi

import "wollow/backend/internal/platform/jobs"

// The job runner moved to internal/platform/jobs when Money grew a
// classification pass of its own — two copies of "is it still running" is
// exactly the drift the merge was meant to end.
//
// These aliases keep mailapi's own call sites and its JSON shape unchanged:
// JobState is what the sync and classify endpoints marshal, and the bulk-job
// tests decode into it by name.
type (
	JobState    = jobs.State
	JobProgress = jobs.Progress
	jobRunner   = jobs.Runner
)

func newJobRunner() *jobRunner { return jobs.NewRunner() }
