package service

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestTaskSystemCommentsIncludeResponsibilityMention(t *testing.T) {
	for _, path := range []string{"fail_task", "daemon_alert"} {
		for _, role := range []string{"agent", "squad_worker"} {
			t.Run(path+"/"+role, func(t *testing.T) {
				ctx := context.Background()
				pool := newResolveOriginatorPool(t)
				workspaceID, userID, agentID, issueID := seedAttributionFixture(t, pool)
				var runtimeID string
				if err := pool.QueryRow(ctx, `SELECT runtime_id::text FROM agent WHERE id = $1`, agentID).Scan(&runtimeID); err != nil {
					t.Fatalf("load runtime: %v", err)
				}

				commentAgentID := agentID
				var squadID any
				if role == "squad_worker" {
					if err := pool.QueryRow(ctx, `
						INSERT INTO agent (workspace_id, name, runtime_mode, runtime_config, runtime_id, visibility,
							max_concurrent_tasks, owner_id, instructions, custom_env, custom_args)
						VALUES ($1, 'comment-worker', 'cloud', '{}'::jsonb, $2, 'workspace', 1, $3, '', '{}'::jsonb, '[]'::jsonb)
						RETURNING id`, workspaceID, runtimeID, userID).Scan(&commentAgentID); err != nil {
						t.Fatalf("seed worker: %v", err)
					}
					if err := pool.QueryRow(ctx, `
						INSERT INTO squad (workspace_id, name, leader_id, creator_id)
						VALUES ($1, 'comment-responsibility-squad', $2, $3)
						RETURNING id`, workspaceID, agentID, userID).Scan(&squadID); err != nil {
						t.Fatalf("seed squad: %v", err)
					}
				}

				var taskID pgtype.UUID
				if err := pool.QueryRow(ctx, `
					INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority, attempt, max_attempts, squad_id)
					VALUES ($1, $2, $3, 'running', 0, 1, 1, $4)
					RETURNING id`, commentAgentID, runtimeID, issueID, squadID).Scan(&taskID); err != nil {
					t.Fatalf("seed task: %v", err)
				}
				t.Cleanup(func() {
					_, _ = pool.Exec(context.Background(), `DELETE FROM comment WHERE source_task_id = $1`, taskID)
					_, _ = pool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID)
				})

				svc := NewTaskService(db.New(pool), pool, nil, events.New())
				switch path {
				case "fail_task":
					if _, err := svc.FailTask(ctx, taskID, "runtime failed", "", "", "", "agent_error.process_failure", false, "", ""); err != nil {
						t.Fatalf("FailTask: %v", err)
					}
				case "daemon_alert":
					if err := svc.AddDaemonTaskSystemComment(ctx, taskID, "watchdog warning"); err != nil {
						t.Fatalf("AddDaemonTaskSystemComment: %v", err)
					}
				}

				var content, commentType string
				if err := pool.QueryRow(ctx, `
					SELECT content, type FROM comment WHERE source_task_id = $1 ORDER BY created_at DESC LIMIT 1`, taskID,
				).Scan(&content, &commentType); err != nil {
					t.Fatalf("load system comment: %v", err)
				}
				wantID := agentID
				if !strings.Contains(content, "mention://agent/"+wantID) {
					t.Fatalf("system comment = %q, want responsibility mention for %s", content, wantID)
				}
				if commentType != "system" {
					t.Fatalf("comment type = %q, want system", commentType)
				}
			})
		}
	}
}
