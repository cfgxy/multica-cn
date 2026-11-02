package scheduler

import (
	"testing"
	"time"
)

func TestKnowledgeGovernanceJobUsesDurableDailyScheduling(t *testing.T) {
	job := KnowledgeGovernanceJob(nil)
	if job.Name != JobNameKnowledgeGovernance {
		t.Fatalf("job name = %q, want %q", job.Name, JobNameKnowledgeGovernance)
	}
	if job.Cadence != 24*time.Hour || job.CatchUpMode != CatchUpLatestOnly {
		t.Fatalf("unexpected scheduling policy: cadence=%s catch_up=%v", job.Cadence, job.CatchUpMode)
	}
	if !job.AllowStaleReentry || job.MaxAttempts != 2 || len(job.RetryBackoff) != 1 || job.RetryBackoff[0] != 15*time.Minute {
		t.Fatalf("unexpected retry policy: stale=%t attempts=%d backoff=%v", job.AllowStaleReentry, job.MaxAttempts, job.RetryBackoff)
	}
	if job.Handler == nil || job.Scopes == nil {
		t.Fatal("knowledge governance job must provide scheduler handler and scope")
	}
}
