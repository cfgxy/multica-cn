package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/multica-ai/multica/server/internal/testutil"
)

type usageFaultTx struct {
	pgx.Tx
	writes int
}

func (tx *usageFaultTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "-- name: UpsertTaskUsage :exec") {
		tx.writes++
		if tx.writes == 2 {
			return tx.Tx.Exec(ctx, "SELECT 1 / 0")
		}
	}
	return tx.Tx.Exec(ctx, sql, args...)
}

type usageFaultStarter struct{ inner txStarter }

func (s usageFaultStarter) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.inner.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &usageFaultTx{Tx: tx}, nil
}

func TestTaskUsageReportAtomicAcrossModelsAndCommit(t *testing.T) {
	for _, fault := range []string{"second_model", "commit"} {
		t.Run(fault, func(t *testing.T) {
			_, taskID := seedNULTask(t, "atomic usage")
			dbfx.Cleanup(t, `DELETE FROM task_usage WHERE task_id=$1`, taskID)
			body := map[string]any{"usage": []map[string]any{
				{"provider": "fake", "model": "low", "input_tokens": 1, "cost_usd_ticks": 1},
				{"provider": "fake", "model": "high", "input_tokens": 1000, "cost_usd_ticks": 1000},
			}}
			h := *testHandler
			if fault == "commit" {
				h.TxStarter = rollbackOnCommitTxStarter{pool: testPool}
			} else {
				h.TxStarter = usageFaultStarter{inner: testHandler.TxStarter}
			}
			request := func() *http.Request { return daemonTaskRequest(t, "/api/daemon/tasks/"+taskID+"/usage", taskID, body) }
			testutil.Call(t, h.ReportTaskUsage, request()).Want(http.StatusInternalServerError)
			if n := dbfx.Count(t, `SELECT count(*) FROM task_usage WHERE task_id=$1`, taskID); n != 0 {
				t.Fatalf("partial report persisted %d rows", n)
			}
			for i := 0; i < 2; i++ {
				testutil.Call(t, testHandler.ReportTaskUsage, request()).Want(http.StatusOK)
			}
			var total int64
			dbfx.QueryRow(t, `SELECT sum(input_tokens) FROM task_usage WHERE task_id=$1`, taskID).Scan(&total)
			if total != 1001 || dbfx.Count(t, `SELECT count(*) FROM task_usage WHERE task_id=$1`, taskID) != 2 {
				t.Fatalf("retry double-counted or lost report: total=%d", total)
			}
		})
	}
}
