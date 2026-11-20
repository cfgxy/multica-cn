package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"

	"github.com/multica-ai/multica/server/pkg/agent"
)

func provenanceTextHash(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func sessionGateProvenance(task Task) (decision, source string) {
	switch {
	case strings.TrimSpace(task.PriorContextBrief) != "":
		return "prior_context_brief", "prior_context_brief"
	case task.PriorSessionResumeUnavailable:
		return "resume_unavailable", "resume_unavailable"
	case task.PriorSessionID != "":
		return "resume", "session"
	default:
		return "fresh", ""
	}
}

func persistWorkerProvenance(opts agent.ExecOptions, result agent.Result, logger *slog.Logger) {
	if opts.Supervision == nil || result.Provenance == nil || opts.Supervision.RunID == "" {
		return
	}
	writer, ok := opts.Supervision.Supervisor.(agent.WorkerProvenanceWriter)
	if !ok {
		return
	}
	provenance := *result.Provenance
	provenance.RunID = opts.Supervision.RunID
	if provenance.TaskID == "" {
		provenance.TaskID = opts.Supervision.TaskID
	}
	if provenance.RuntimeID == "" {
		provenance.RuntimeID = opts.Supervision.RuntimeID
	}
	if provenance.Generation == 0 {
		provenance.Generation = opts.Supervision.Generation
	}
	if err := writer.WriteWorkerProvenance(opts.Supervision.RunID, provenance); err != nil && logger != nil {
		logger.Warn("failed to persist worker provenance", "error", err, "run_id", opts.Supervision.RunID)
	}
}
