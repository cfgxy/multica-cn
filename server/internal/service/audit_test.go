package service

// Write-contract unit tests (RUYI-355 Phase 1 acceptance: every Validate
// rule has a test). Pure-contract coverage — the DB-touching append paths
// are exercised by the migration smoke and the handler feature tests.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func auditTestEvent() Event {
	return Event{
		ID:          dbid.NewV7(),
		WorkspaceID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
		Domain:      AuditDomainRun,
		EventType:   AuditRunCompleted,
		OccurredAt:  time.Now().UTC(),
		ActorType:   AuditActorSystem,
		TaskID:      pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
	}
}

func TestValidateAcceptsMinimalEventPerDomain(t *testing.T) {
	base := auditTestEvent()
	cases := []struct {
		name string
		mut  func(*Event)
	}{
		{"run", func(e *Event) {}},
		{"issue", func(e *Event) {
			e.Domain, e.EventType = AuditDomainIssue, AuditIssueCreated
			e.TaskID = pgtype.UUID{}
			e.IssueID = pgtype.UUID{Bytes: [16]byte{3}, Valid: true}
		}},
		{"agent", func(e *Event) {
			e.Domain, e.EventType = AuditDomainAgent, AuditAgentEnvUpdated
			e.TaskID = pgtype.UUID{}
			e.AgentID = pgtype.UUID{Bytes: [16]byte{4}, Valid: true}
			e.ActorType, e.ActorID = AuditActorMember, pgtype.UUID{Bytes: [16]byte{5}, Valid: true}
		}},
		{"runtime", func(e *Event) {
			e.Domain, e.EventType = AuditDomainRuntime, AuditRuntimeConnected
			e.TaskID = pgtype.UUID{}
			e.RuntimeID = pgtype.UUID{Bytes: [16]byte{6}, Valid: true}
		}},
		{"ops", func(e *Event) {
			e.Domain, e.EventType = AuditDomainOps, AuditOpsServerStarted
			e.TaskID = pgtype.UUID{}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := base
			tc.mut(&e)
			if err := e.Validate(); err != nil {
				t.Fatalf("minimal %s event must validate, got: %v", tc.name, err)
			}
		})
	}
}

func TestValidateRejectsContractViolations(t *testing.T) {
	cases := []struct {
		name    string
		mut     func(*Event)
		wantSub string
	}{
		{"missing workspace", func(e *Event) { e.WorkspaceID = pgtype.UUID{} }, "workspace_id is required"},
		{"unknown domain", func(e *Event) { e.Domain = "audit" }, "unknown domain"},
		{"missing event_type", func(e *Event) { e.EventType = "" }, "event_type is required"},
		{"event_type not domain-prefixed", func(e *Event) { e.EventType = "completed" }, "must be prefixed"},
		{"event_type wrong domain prefix", func(e *Event) { e.EventType = AuditIssueCreated }, "must be prefixed"},
		{"zero occurred_at", func(e *Event) { e.OccurredAt = time.Time{} }, "occurred_at is required"},
		{"unknown actor_type", func(e *Event) { e.ActorType = "robot" }, "unknown actor_type"},
		{"member without actor_id", func(e *Event) {
			e.ActorType, e.ActorID = AuditActorMember, pgtype.UUID{}
		}, "actor_id is required"},
		{"daemon without actor_id", func(e *Event) {
			e.ActorType, e.ActorID = AuditActorDaemon, pgtype.UUID{}
		}, "actor_id is required"},
		{"run without task_id", func(e *Event) { e.TaskID = pgtype.UUID{} }, "task_id is required"},
		{"runtime without runtime_id", func(e *Event) {
			e.Domain, e.EventType, e.TaskID = AuditDomainRuntime, AuditRuntimeGC, pgtype.UUID{}
			e.RuntimeID = pgtype.UUID{}
		}, "runtime_id is required"},
		{"issue without issue_id", func(e *Event) {
			e.Domain, e.EventType, e.TaskID = AuditDomainIssue, AuditIssueStatusChanged, pgtype.UUID{}
			e.IssueID = pgtype.UUID{}
		}, "issue_id is required"},
		{"agent without agent_id", func(e *Event) {
			e.Domain, e.EventType, e.TaskID = AuditDomainAgent, AuditAgentEnvRevealed, pgtype.UUID{}
			e.AgentID = pgtype.UUID{}
		}, "agent_id is required"},
		{"run.cancelled without reason", func(e *Event) {
			e.EventType = AuditRunCancelled
		}, "reason is required"},
		{"run.failed without reason", func(e *Event) {
			e.EventType = AuditRunFailed
		}, "reason is required"},
		{"run.cancel_requested without reason", func(e *Event) {
			e.EventType = AuditRunCancelRequested
		}, "reason is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := auditTestEvent()
			tc.mut(&e)
			err := e.Validate()
			if err == nil {
				t.Fatalf("expected contract violation, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q must mention %q", err, tc.wantSub)
			}
		})
	}
}

func TestAppendAuditEventsValidatesBeforeWrite(t *testing.T) {
	// A nil Queries must never be dereferenced: the empty-batch early return
	// and the per-event validation both happen before any write attempt.
	if err := AppendAuditEvents(context.Background(), nil); err != nil {
		t.Fatalf("empty batch must be a no-op, got: %v", err)
	}
	bad := auditTestEvent()
	bad.WorkspaceID = pgtype.UUID{}
	if err := AppendAuditEvents(context.Background(), nil, bad); err == nil {
		t.Fatalf("invalid event must fail validation, not reach the write")
	}
}

func TestTaskCancelAttribution(t *testing.T) {
	reason, actor := TaskCancelAttribution(true)
	if reason != AuditReasonUserRequested || actor != AuditActorMember {
		t.Fatalf("user-initiated cancel must attribute member/user_requested, got %s/%s", actor, reason)
	}
	reason, actor = TaskCancelAttribution(false)
	if reason != AuditReasonServerRepair || actor != AuditActorSystem {
		t.Fatalf("server cancel must attribute system/server_repair, got %s/%s", actor, reason)
	}
}

func TestAuditActorFor(t *testing.T) {
	id := pgtype.UUID{Bytes: [16]byte{8}, Valid: true}
	if aType, aID := AuditActorFor(AuditActorMember, id); aType != AuditActorMember || !aID.Valid {
		t.Fatalf("known actor with id must pass through")
	}
	if aType, _ := AuditActorFor(AuditActorAgent, id); aType != AuditActorAgent {
		t.Fatalf("agent actor with id must pass through")
	}
	if aType, _ := AuditActorFor(AuditActorDaemon, id); aType != AuditActorDaemon {
		t.Fatalf("daemon actor with id must pass through")
	}
	if aType, _ := AuditActorFor(AuditActorMember, pgtype.UUID{}); aType != AuditActorSystem {
		t.Fatalf("known actor without id must degrade to system, got %s", aType)
	}
	if aType, _ := AuditActorFor("robot", id); aType != AuditActorSystem {
		t.Fatalf("unknown actor type must degrade to system, got %s", aType)
	}
	if aType, _ := AuditActorFor(AuditActorSystem, pgtype.UUID{}); aType != AuditActorSystem {
		t.Fatalf("system actor must stay system")
	}
}

func TestMemberOrSystemActor(t *testing.T) {
	id := pgtype.UUID{Bytes: [16]byte{9}, Valid: true}
	if aType, aID := memberOrSystemActor(id); aType != AuditActorMember || aID != id {
		t.Fatalf("valid user id must attribute member")
	}
	if aType, aID := memberOrSystemActor(pgtype.UUID{}); aType != AuditActorSystem || aID.Valid {
		t.Fatalf("empty user id must attribute system without id")
	}
}

func TestRunEventFromDimsCarriesReasonAndValidates(t *testing.T) {
	e := runEventFromDims(
		pgtype.UUID{Bytes: [16]byte{1}, Valid: true}, // workspace
		pgtype.UUID{Bytes: [16]byte{3}, Valid: true}, // issue
		pgtype.UUID{Bytes: [16]byte{2}, Valid: true}, // task
		pgtype.UUID{Bytes: [16]byte{4}, Valid: true}, // agent
		pgtype.UUID{Bytes: [16]byte{5}, Valid: true}, // runtime
		AuditRunFailed, AuditReasonRuntimeOffline, AuditActorDaemon,
		pgtype.UUID{Bytes: [16]byte{6}, Valid: true},
	)
	if e.Reason == nil || *e.Reason != AuditReasonRuntimeOffline {
		t.Fatalf("dims constructor must carry the reason")
	}
	if err := e.Validate(); err != nil {
		t.Fatalf("dims-built failed event must validate, got: %v", err)
	}

	withDetails := e.WithDetails(JSONDetails(map[string]string{"wait_reason": "dir_missing"}))
	if !strings.Contains(string(withDetails.Details), "dir_missing") {
		t.Fatalf("WithDetails must set the payload")
	}
	if broken := e.WithDetails([]byte("not-json")); string(broken.Details) != "not-json" {
		t.Fatalf("WithDetails passes the payload through unvalidated by design")
	}
	if got := string(jsonDetails(make(chan int))); got != "{}" {
		t.Fatalf("unmarshalable details must degrade to {}, got %s", got)
	}

	ref := dbid.NewV7().String()
	withTrigger := e.WithTrigger("rerun_of_task", ref)
	if withTrigger.TriggerKind == nil || *withTrigger.TriggerKind != "rerun_of_task" {
		t.Fatalf("WithTrigger must set kind")
	}
	if withTrigger.TriggerRef == nil || *withTrigger.TriggerRef != ref {
		t.Fatalf("WithTrigger must set ref")
	}
}

func TestOpsServerStartedEventShape(t *testing.T) {
	ws := pgtype.UUID{Bytes: [16]byte{10}, Valid: true}
	e := OpsServerStartedEvent(ws, "v1.2.3", "abc123")
	if err := e.Validate(); err != nil {
		t.Fatalf("ops.server_started must validate, got: %v", err)
	}
	if e.Domain != AuditDomainOps || e.EventType != AuditOpsServerStarted {
		t.Fatalf("deployment anchor must be ops-namespaced")
	}
	if e.ActorType != AuditActorSystem || e.ActorID.Valid {
		t.Fatalf("deployment anchor acts as system without id")
	}
	if !strings.Contains(string(e.Details), "v1.2.3") || !strings.Contains(string(e.Details), "abc123") {
		t.Fatalf("details must carry version and commit, got %s", e.Details)
	}
}

func TestRuntimeEventFromRowAndDims(t *testing.T) {
	rt := db.AgentRuntime{
		ID:          pgtype.UUID{Bytes: [16]byte{11}, Valid: true},
		WorkspaceID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
	}
	ev := RuntimeEventFromRow(AuditRuntimeConnected, AuditActorDaemon, rt)
	if err := ev.Validate(); err != nil {
		t.Fatalf("row-built runtime event must validate, got: %v", err)
	}
	if ev.RuntimeID != rt.ID || ev.WorkspaceID != rt.WorkspaceID {
		t.Fatalf("event dims must come from the row")
	}

	ws := pgtype.UUID{Bytes: [16]byte{12}, Valid: true}
	dims := RuntimeEventFromDims(AuditRuntimeReconnectExhausted, AuditReasonReconnectExhausted,
		AuditActorSystem, pgtype.UUID{}, ws, rt.ID)
	if err := dims.Validate(); err != nil {
		t.Fatalf("dims-built runtime event must validate, got: %v", err)
	}
	if dims.Reason == nil || *dims.Reason != AuditReasonReconnectExhausted {
		t.Fatalf("dims constructor must carry the reason")
	}
	if dims.ActorID.Valid {
		t.Fatalf("system actor must carry no id")
	}
}
