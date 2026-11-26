package taskusagebackfill_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/taskusagebackfill"
)

func TestHourlyWatermarkBootstrapRollsUpPostSchemaUsage(t *testing.T) {
	ctx, pool := hourlyBootstrapTestPool(t, "post_schema")
	if err := applyMigrationsUpTo(ctx, pool, "102_task_usage_hourly_pipeline"); err != nil {
		t.Fatalf("apply migrations to 102: %v", err)
	}
	for _, version := range []string{
		"213_task_usage_authoritative_cost",
		"272_rollup_task_usage_hourly_xact_lock",
	} {
		if err := tryApplyMigration(ctx, pool, version); err != nil {
			t.Fatalf("apply %s: %v", version, err)
		}
	}

	_, runtimeID, _, taskID := seedTaskUsageFixture(t, ctx, pool)
	var eventAt time.Time
	if err := pool.QueryRow(ctx, `
		INSERT INTO task_usage (
			task_id, provider, model,
			input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
			cost_usd_ticks, created_at, updated_at
		) VALUES ($1, 'xai', 'grok-test', 300, 150, 75, 15, 2000000000,
		          now() - INTERVAL '10 minutes', now() - INTERVAL '10 minutes')
		RETURNING created_at
	`, taskID).Scan(&eventAt); err != nil {
		t.Fatalf("seed post-schema task_usage: %v", err)
	}

	if err := tryApplyMigration(ctx, pool, "985_task_usage_hourly_watermark_bootstrap"); err != nil {
		t.Fatalf("apply watermark bootstrap: %v", err)
	}
	var watermark time.Time
	if err := pool.QueryRow(ctx, `
		SELECT watermark_at FROM task_usage_hourly_rollup_state WHERE id = 1
	`).Scan(&watermark); err != nil {
		t.Fatalf("read watermark: %v", err)
	}
	if !watermark.Equal(eventAt) {
		t.Fatalf("watermark = %s, want earliest event %s", watermark.UTC(), eventAt.UTC())
	}

	if _, err := pool.Exec(ctx, `SELECT rollup_task_usage_hourly()`); err != nil {
		t.Fatalf("run hourly rollup: %v", err)
	}
	var inputTokens, outputTokens, costTicks int64
	if err := pool.QueryRow(ctx, `
		SELECT input_tokens, output_tokens, cost_usd_ticks
		  FROM task_usage_hourly
		 WHERE runtime_id = $1 AND provider = 'xai' AND model = 'grok-test'
	`, runtimeID).Scan(&inputTokens, &outputTokens, &costTicks); err != nil {
		t.Fatalf("read hourly usage: %v", err)
	}
	if inputTokens != 300 || outputTokens != 150 || costTicks != 2000000000 {
		t.Fatalf("hourly usage = input=%d output=%d cost=%d, want 300/150/2000000000", inputTokens, outputTokens, costTicks)
	}
}

func TestHourlyWatermarkBootstrapPreservesHistoricalBackfill(t *testing.T) {
	ctx, pool := hourlyBootstrapTestPool(t, "historical")
	if err := applyMigrationsUpTo(ctx, pool, "102_task_usage_hourly_pipeline"); err != nil {
		t.Fatalf("apply migrations to 102: %v", err)
	}

	_, _, _, taskID := seedTaskUsageFixture(t, ctx, pool)
	if _, err := pool.Exec(ctx, `
		INSERT INTO task_usage (
			task_id, provider, model,
			input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
			created_at
		) VALUES ($1, 'openai', 'gpt-test', 10, 20, 0, 0,
		          now() - INTERVAL '2 hours')
	`, taskID); err != nil {
		t.Fatalf("seed historical task_usage: %v", err)
	}

	if err := tryApplyMigration(ctx, pool, "985_task_usage_hourly_watermark_bootstrap"); err != nil {
		t.Fatalf("apply watermark bootstrap: %v", err)
	}
	var watermark time.Time
	if err := pool.QueryRow(ctx, `
		SELECT watermark_at FROM task_usage_hourly_rollup_state WHERE id = 1
	`).Scan(&watermark); err != nil {
		t.Fatalf("read watermark: %v", err)
	}
	if !watermark.Equal(time.Date(1970, time.January, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("historical usage advanced watermark to %s", watermark.UTC())
	}

	res, err := taskusagebackfill.Hook(ctx, pool, taskusagebackfill.HookOptions{})
	if err != nil {
		t.Fatalf("historical backfill: %v", err)
	}
	if res.SlicesProcessed == 0 || !res.WatermarkStamped {
		t.Fatalf("historical backfill result = %+v, want completed slice and watermark stamp", res)
	}
	var hourlyRows int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM task_usage_hourly`).Scan(&hourlyRows); err != nil {
		t.Fatalf("count hourly rows: %v", err)
	}
	if hourlyRows == 0 {
		t.Fatal("historical backfill did not create hourly usage")
	}
}

func hourlyBootstrapTestPool(t *testing.T, label string) (context.Context, *pgxpool.Pool) {
	t.Helper()
	adminURL := os.Getenv("DATABASE_URL")
	if adminURL == "" {
		adminURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	ctx := context.Background()
	if !databaseReachable(ctx, adminURL) {
		t.Skip("integration test requires Postgres at DATABASE_URL")
	}

	tmpDB := fmt.Sprintf("multica_ruyi872_%s_%d", label, time.Now().UnixNano())
	if err := createDatabase(ctx, adminURL, tmpDB); err != nil {
		t.Fatalf("create temp database %s: %v", tmpDB, err)
	}
	t.Cleanup(func() {
		if err := dropDatabase(context.Background(), adminURL, tmpDB); err != nil {
			t.Logf("drop temp database %s: %v", tmpDB, err)
		}
	})

	pool, err := pgxpool.New(ctx, replaceDatabase(adminURL, tmpDB))
	if err != nil {
		t.Fatalf("connect temp database: %v", err)
	}
	t.Cleanup(pool.Close)
	return ctx, pool
}
