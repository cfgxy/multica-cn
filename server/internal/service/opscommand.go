// RUYI-645: server-side business rules for the read-only node-ops command
// channel. This file owns the seven submission gates (design §4), the
// submission-key idempotency window, receipt finalization and the periodic
// sweeps. Storage primitives live in pkg/db/queries/ops_command.sql; the
// whitelist itself lives in pkg/opskind and is shared verbatim with the
// daemon.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/opskind"
)

// Channel timing constants (design §4). The pending TTL and the lease are
// the same 120s: a pending command whose node never heartbeats within 120s
// expires, and a claimed command without a receipt within 120s times out.
// Neither path re-dispatches (assertion 5: never auto-resend, no catch-up
// after reconnect).
const (
	OpsCommandPendingTTL      = 120 * time.Second
	OpsCommandLease           = 120 * time.Second
	OpsCommandMaxPerBeat      = 3
	OpsCommandQuotaPerRuntime = 5
	OpsIdempotencyWindow      = 10 * time.Minute
	OpsResultMaxBytes         = 256 * 1024
	OpsDefaultTimeoutS        = 30
	OpsMaxTimeoutS            = 300
)

// Submission gate failures. The handler maps these to HTTP statuses.
var (
	ErrOpsRuntimeOffline     = errors.New("runtime is offline")
	ErrOpsKindNotAllowed     = errors.New("command kind is not in the read-only whitelist")
	ErrOpsCapabilityMissing  = errors.New("node does not report the capabilities this command needs")
	ErrOpsSlotInvalid        = errors.New("command arguments failed validation")
	ErrOpsUnitNotManaged     = errors.New("unit is not in the node's last reported service inventory")
	ErrOpsTimeoutInvalid     = errors.New("timeout_s outside [1,300]")
	ErrOpsSubmissionConflict = errors.New("submission_key was already used with different arguments")
	ErrOpsQuotaExceeded      = errors.New("too many in-flight commands for this runtime")
	ErrOpsEpochMismatch      = errors.New("receipt lease epoch does not match the current claim")
	ErrOpsResultTooLarge     = errors.New("command result exceeds the 256KB cap")
)

// OpsSubmitter describes the actor on a member-API submission.
type OpsSubmitter struct {
	Type string // service.AuditActorMember | service.AuditActorAgent
	ID   pgtype.UUID
	Role string // workspace role of the caller (authorization checked by the handler)
}

// OpsCommandInput is a validated-transport submission: raw kind/args still
// need the gate treatment.
type OpsCommandInput struct {
	RuntimeID     pgtype.UUID
	WorkspaceID   pgtype.UUID
	DaemonID      string
	Kind          string
	ArgsJSON      []byte
	TimeoutS      int
	SubmissionKey string
	Submitter     OpsSubmitter
}

// SubmittedOpsCommand is the gate pipeline's outcome.
type SubmittedOpsCommand struct {
	Command    db.DaemonOpsCommand
	Idempotent bool // true when an existing in-window command was returned
}

// SubmitOpsCommand runs the seven gates and inserts the pending row. The
// runtime row must already be loaded (the handler resolves it for
// authorization anyway); it is passed in so the offline gate needs no extra
// query. Inventory membership for unit slots is checked against the
// daemon's last successful report.
func SubmitOpsCommand(ctx context.Context, q *db.Queries, rt db.AgentRuntime, in OpsCommandInput) (SubmittedOpsCommand, error) {
	// Gate 1 — runtime online. A queued command for an offline node could
	// only ever expire, so submission is refused up front (409).
	if rt.Status != "online" {
		return SubmittedOpsCommand{}, ErrOpsRuntimeOffline
	}

	// Gate 2 — whitelist membership. Server-side half of assertion 1.
	spec := opskind.Lookup(opskind.Kind(in.Kind))
	if spec == nil {
		return SubmittedOpsCommand{}, ErrOpsKindNotAllowed
	}

	// Gate 3 — capability matrix from the runtime's registration metadata.
	caps, err := opsCapsFromMetadata(rt.Metadata)
	if err != nil {
		return SubmittedOpsCommand{}, fmt.Errorf("parse runtime ops caps: %w", err)
	}
	if !opskind.Available(opskind.Kind(in.Kind), caps) {
		return SubmittedOpsCommand{}, ErrOpsCapabilityMissing
	}

	// Gate 4 — typed argument slots. The unit slot additionally has to match
	// the node's last successfully reported service inventory, so an
	// operator can only address services the node actually manages.
	var args map[string]any
	if len(in.ArgsJSON) > 0 {
		if err := json.Unmarshal(in.ArgsJSON, &args); err != nil {
			return SubmittedOpsCommand{}, fmt.Errorf("%w: args is not a JSON object", ErrOpsSlotInvalid)
		}
	}
	if args == nil {
		args = map[string]any{}
	}
	normalized, err := opskind.NormalizeArgs(opskind.Kind(in.Kind), args)
	if err != nil {
		return SubmittedOpsCommand{}, fmt.Errorf("%w: %v", ErrOpsSlotInvalid, err)
	}
	if unit, ok := normalized["unit"].(string); ok {
		managed, err := unitInInventory(ctx, q, textToString(rt.DaemonID), unit)
		if err != nil {
			return SubmittedOpsCommand{}, err
		}
		if !managed {
			return SubmittedOpsCommand{}, fmt.Errorf("%w: %q", ErrOpsUnitNotManaged, unit)
		}
	}
	normJSON, err := json.Marshal(normalized)
	if err != nil {
		return SubmittedOpsCommand{}, err
	}

	// Gate 5 — timeout domain.
	timeoutS := in.TimeoutS
	if timeoutS == 0 {
		timeoutS = OpsDefaultTimeoutS
	}
	if timeoutS < 1 || timeoutS > OpsMaxTimeoutS {
		return SubmittedOpsCommand{}, ErrOpsTimeoutInvalid
	}

	// Gate 6 — submission-key idempotency (assertion 3).
	if in.SubmissionKey != "" {
		matches, err := q.FindOpsCommandBySubmissionKey(ctx, db.FindOpsCommandBySubmissionKeyParams{
			RuntimeID:     in.RuntimeID,
			SubmissionKey: pgtype.Text{String: in.SubmissionKey, Valid: true},
		})
		if err != nil {
			return SubmittedOpsCommand{}, err
		}
		for _, row := range matches {
			sameArgs := argsEquivalent(row.Args, normJSON)
			withinWindow := time.Since(row.CreatedAt.Time) < OpsIdempotencyWindow
			if sameArgs && (withinWindow || !isTerminalOpsStatus(row.Status)) {
				// Same key + same args: replay the original submission
				// instead of queueing a duplicate.
				return SubmittedOpsCommand{Command: row, Idempotent: true}, nil
			}
			if !sameArgs {
				return SubmittedOpsCommand{}, ErrOpsSubmissionConflict
			}
			// Terminal and older than the window: archive the key and fall
			// through to a fresh insert (MeshConsole archived_key semantics
			// adapted to the partial unique index).
			if err := q.ArchiveSubmissionKey(ctx, db.ArchiveSubmissionKeyParams{
				RuntimeID:     in.RuntimeID,
				SubmissionKey: pgtype.Text{String: in.SubmissionKey, Valid: true},
			}); err != nil {
				return SubmittedOpsCommand{}, err
			}
			break
		}
	}

	// Gate 7 — in-flight quota per runtime.
	inFlight, err := q.CountInFlightOpsCommands(ctx, in.RuntimeID)
	if err != nil {
		return SubmittedOpsCommand{}, err
	}
	if int(inFlight) >= OpsCommandQuotaPerRuntime {
		return SubmittedOpsCommand{}, ErrOpsQuotaExceeded
	}

	commandID := dbid.NewV7()
	command, err := q.InsertOpsCommand(ctx, db.InsertOpsCommandParams{
		ID:              commandID,
		WorkspaceID:     in.WorkspaceID,
		RuntimeID:       in.RuntimeID,
		DaemonID:        textToString(rt.DaemonID),
		Kind:            in.Kind,
		Args:            normJSON,
		TimeoutS:        int32(timeoutS),
		SubmittedByType: in.Submitter.Type,
		SubmittedByID:   in.Submitter.ID,
		SubmissionKey:   pgtype.Text{String: in.SubmissionKey, Valid: in.SubmissionKey != ""},
	})
	if err != nil {
		return SubmittedOpsCommand{}, err
	}

	TryAppendAuditEvents(ctx, q, Event{
		ID:          dbid.NewV7(),
		WorkspaceID: in.WorkspaceID,
		Domain:      AuditDomainOps,
		EventType:   AuditOpsCommandSubmitted,
		OccurredAt:  time.Now(),
		ActorType:   in.Submitter.Type,
		ActorID:     in.Submitter.ID,
		RuntimeID:   in.RuntimeID,
		Details: mustJSON(map[string]any{
			"command_id": uuidString(commandID),
			"kind":       in.Kind,
			"args":       normalized,
			"timeout_s":  timeoutS,
			"idempotent": false,
		}),
	})

	return SubmittedOpsCommand{Command: command}, nil
}

// ClaimOpsCommands atomically claims up to limit pending commands for a
// runtime and bumps their lease epochs. Safe under concurrent server
// instances: rows are locked FOR UPDATE SKIP LOCKED inside the claim
// UPDATE, so no command is ever handed to two claimants and claimants never
// wait on each other (no double-claim, no deadlock).
func ClaimOpsCommands(ctx context.Context, q *db.Queries, runtimeID pgtype.UUID, limit int) ([]db.ClaimPendingOpsCommandsRow, error) {
	if limit <= 0 {
		limit = OpsCommandMaxPerBeat
	}
	return q.ClaimPendingOpsCommands(ctx, db.ClaimPendingOpsCommandsParams{
		RuntimeID: runtimeID,
		Limit:     int32(limit),
	})
}

// OpsReceipt is the daemon's result POST body.
type OpsReceipt struct {
	Status     string `json:"status"` // succeeded | failed | rejected
	ExitCode   *int   `json:"exit_code,omitempty"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	Error      string `json:"error,omitempty"` // reject reason for status=rejected
	LeaseEpoch int64  `json:"lease_epoch"`

	// Route identifiers resolved by the handler from the URL path.
	CommandID string
	RuntimeID string
}

// FinalizeOpsCommandReceipt applies a daemon receipt to a claimed row. The
// (command_id, runtime_id, lease_epoch) triple must match the current claim
// generation; anything else is a stale or misplaced receipt and is rejected
// with an audit record instead of overwriting state (assertion 3).
func FinalizeOpsCommandReceipt(ctx context.Context, q *db.Queries, receipt OpsReceipt) (db.DaemonOpsCommand, error) {
	switch receipt.Status {
	case "succeeded", "failed", "rejected":
	default:
		return db.DaemonOpsCommand{}, fmt.Errorf("receipt status %q is not terminal", receipt.Status)
	}

	var resultJSON []byte
	resultBytes := 0
	if receipt.Status != "rejected" {
		payload, err := json.Marshal(map[string]any{
			"stdout":    receipt.Stdout,
			"stderr":    receipt.Stderr,
			"truncated": receipt.Truncated,
		})
		if err != nil {
			return db.DaemonOpsCommand{}, err
		}
		if len(payload) > OpsResultMaxBytes {
			return db.DaemonOpsCommand{}, ErrOpsResultTooLarge
		}
		resultJSON = payload
		resultBytes = len(payload)
	}

	var exitCode pgtype.Int4
	if receipt.ExitCode != nil {
		exitCode = pgtype.Int4{Int32: int32(*receipt.ExitCode), Valid: true}
	}
	var rejectReason pgtype.Text
	if receipt.Status == "rejected" {
		rejectReason = pgtype.Text{String: receipt.Error, Valid: receipt.Error != ""}
	}

	commandID := pgtype.UUID{}
	if err := commandID.Scan(receipt.CommandID); err != nil {
		return db.DaemonOpsCommand{}, err
	}
	runtimeUUID, err := util.ParseUUID(receipt.RuntimeID)
	if err != nil {
		return db.DaemonOpsCommand{}, err
	}
	rows, err := q.FinalizeOpsCommandReceipt(ctx, db.FinalizeOpsCommandReceiptParams{
		ID:           commandID,
		RuntimeID:    runtimeUUID,
		Status:       receipt.Status,
		Result:       resultJSON,
		ExitCode:     exitCode,
		RejectReason: rejectReason,
		LeaseEpoch:   receipt.LeaseEpoch,
	})
	if err != nil {
		return db.DaemonOpsCommand{}, err
	}
	if len(rows) == 0 {
		// Late or misplaced receipt. The audit needs a workspace to satisfy
		// the contract, so attribute only when the command row still exists;
		// daemons carry no audit UUID, so the actor maps to system
		// (AuditActorFor) with the daemon context in details.
		if prior, getErr := q.GetOpsCommand(ctx, commandID); getErr == nil {
			TryAppendAuditEvents(ctx, q, Event{
				ID:          dbid.NewV7(),
				WorkspaceID: prior.WorkspaceID,
				Domain:      AuditDomainOps,
				EventType:   AuditOpsCommandFinalized,
				OccurredAt:  time.Now(),
				ActorType:   AuditActorSystem,
				RuntimeID:   runtimeUUID,
				Reason:      strPtr("late_or_mismatched_receipt"),
				Details: mustJSON(map[string]any{
					"command_id":  receipt.CommandID,
					"outcome":     "late_receipt_rejected",
					"lease_epoch": receipt.LeaseEpoch,
				}),
			})
		}
		return db.DaemonOpsCommand{}, ErrOpsEpochMismatch
	}
	row := rows[0]

	// Details stay free of result content (design §3): status metadata only.
	TryAppendAuditEvents(ctx, q, Event{
		ID:          dbid.NewV7(),
		WorkspaceID: row.WorkspaceID,
		Domain:      AuditDomainOps,
		EventType:   AuditOpsCommandFinalized,
		OccurredAt:  time.Now(),
		ActorType:   AuditActorSystem,
		RuntimeID:   runtimeUUID,
		Details: mustJSON(map[string]any{
			"command_id":   receipt.CommandID,
			"kind":         row.Kind,
			"status":       row.Status,
			"exit_code":    exitCode,
			"lease_epoch":  row.LeaseEpoch,
			"result_bytes": resultBytes,
			"reported_by":  "daemon",
		}),
	})

	command, err := q.GetOpsCommand(ctx, commandID)
	if err != nil {
		return db.DaemonOpsCommand{}, err
	}
	return command, nil
}

// SweepOpsCommands finalizes the two drift states in bounded batches:
// unclaimed pending rows past the TTL expire, claimed rows past the lease
// time out. Called from the runtime sweeper's periodic round (no new timer)
// and lazily from the member read paths.
func SweepOpsCommands(ctx context.Context, q *db.Queries) (expired, timedOut int) {
	expiredRows, err := q.ExpireStalePendingOpsCommands(ctx, db.ExpireStalePendingOpsCommandsParams{
		TtlSeconds: int32(OpsCommandPendingTTL.Seconds()),
		BatchSize:  100,
	})
	if err != nil {
		slog.Warn("ops command sweep: expire pending failed", "error", err)
	}
	for _, row := range expiredRows {
		TryAppendAuditEvents(ctx, q, Event{
			ID:          dbid.NewV7(),
			WorkspaceID: row.WorkspaceID,
			Domain:      AuditDomainOps,
			EventType:   AuditOpsCommandFinalized,
			OccurredAt:  time.Now(),
			ActorType:   AuditActorSystem,
			RuntimeID:   row.RuntimeID,
			Reason:      strPtr("pending_ttl_exceeded"),
			Details: mustJSON(map[string]any{
				"command_id": uuidString(row.ID),
				"kind":       row.Kind,
				"status":     "expired",
			}),
		})
	}
	expired = len(expiredRows)

	timeoutRows, err := q.TimeoutStaleClaimedOpsCommands(ctx, db.TimeoutStaleClaimedOpsCommandsParams{
		LeaseSeconds: int32(OpsCommandLease.Seconds()),
		BatchSize:    100,
	})
	if err != nil {
		slog.Warn("ops command sweep: timeout claimed failed", "error", err)
	}
	for _, row := range timeoutRows {
		TryAppendAuditEvents(ctx, q, Event{
			ID:          dbid.NewV7(),
			WorkspaceID: row.WorkspaceID,
			Domain:      AuditDomainOps,
			EventType:   AuditOpsCommandFinalized,
			OccurredAt:  time.Now(),
			ActorType:   AuditActorSystem,
			RuntimeID:   row.RuntimeID,
			Reason:      strPtr("lease_expired"),
			Details: mustJSON(map[string]any{
				"command_id":  uuidString(row.ID),
				"kind":        row.Kind,
				"status":      "timeout",
				"lease_epoch": row.LeaseEpoch,
			}),
		})
	}
	timedOut = len(timeoutRows)
	return expired, timedOut
}

// unitInInventory reports whether unit appears in the daemon's most recent
// successful inventory report. No report at all counts as unmanaged: the
// gate fails closed until the node has proven its service set.
func unitInInventory(ctx context.Context, q *db.Queries, daemonID, unit string) (bool, error) {
	inv, err := q.GetDaemonServiceInventory(ctx, daemonID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if inv.CollectStatus != "ok" || !inv.LastSuccessAt.Valid {
		return false, nil
	}
	var services []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(inv.Services, &services); err != nil {
		return false, fmt.Errorf("parse service inventory for %s: %w", daemonID, err)
	}
	for _, svc := range services {
		if svc.Name == unit {
			return true, nil
		}
	}
	return false, nil
}

// opsCapsFromMetadata reads the caps array the daemon reported at
// registration (stored in agent_runtime.metadata.ops_caps).
func opsCapsFromMetadata(metadata []byte) (map[string]bool, error) {
	caps := map[string]bool{}
	if len(metadata) == 0 {
		return caps, nil
	}
	var meta struct {
		OpsCaps []string `json:"ops_caps"`
	}
	if err := json.Unmarshal(metadata, &meta); err != nil {
		// Unreadable metadata must not fail the gate open or closed by
		// accident — surface the error and let the caller decide.
		return nil, err
	}
	for _, c := range meta.OpsCaps {
		caps[c] = true
	}
	return caps, nil
}

func isTerminalOpsStatus(status string) bool {
	switch status {
	case "succeeded", "failed", "rejected", "timeout", "expired":
		return true
	}
	return false
}

func argsEquivalent(a, b []byte) bool {
	var ma, mb map[string]any
	if err := json.Unmarshal(a, &ma); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &mb); err != nil {
		return false
	}
	aj, _ := json.Marshal(ma)
	bj, _ := json.Marshal(mb)
	return string(aj) == string(bj)
}

func strPtr(s string) *string { return &s }

func textToString(t pgtype.Text) string {
	if !t.Valid {
		return ""
	}
	return t.String
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}
