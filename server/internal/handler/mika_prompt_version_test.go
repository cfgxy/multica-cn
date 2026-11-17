package handler

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
)

// The snapshot is a restorable carrier, while the fingerprint tracks the
// product-owned static layer. Concurrent claims must share one version.
func TestMikaSnapshotConcurrentAndReleaseFingerprint(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := dbfx.Agent(t, "mika-snapshot-concurrent", handlerTestRuntimeID(t), map[string]any{"instructions": "workspace notes"})
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM prompt_version WHERE scope='agent' AND scope_id=$1`, agentID)
	})
	const n = 8
	versions := make([]int32, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			versions[i], errs[i] = testHandler.ensureMikaPromptSnapshot(ctx, parseUUID(testWorkspaceID), parseUUID(agentID), "workspace notes", "binary-v1\nworkspace notes")
		}(i)
	}
	wg.Wait()
	for i := range n {
		if errs[i] != nil || versions[i] <= 0 || versions[i] != versions[0] {
			t.Fatalf("concurrent snapshot %d: version=%d first=%d err=%v", i, versions[i], versions[0], errs[i])
		}
	}
	v2, err := testHandler.ensureMikaPromptSnapshot(ctx, parseUUID(testWorkspaceID), parseUUID(agentID), "workspace notes", "binary-v2\nworkspace notes")
	if err != nil || v2 <= versions[0] {
		t.Fatalf("new static layer: version=%d previous=%d err=%v", v2, versions[0], err)
	}
	var content string
	if err := testPool.QueryRow(ctx, `SELECT content FROM prompt_version WHERE scope='agent' AND scope_id=$1 AND version=$2`, agentID, v2).Scan(&content); err != nil || content != "workspace notes" {
		t.Fatalf("restorable carrier: content=%q err=%v", content, err)
	}
}

func TestMikaSnapshotFailureDoesNotUseUnrelatedLatest(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := createClaimReclaimRuntime(t, ctx, "mika-snapshot-failure")
	agentID, issueID := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "mika-snapshot-failure")
	insertPromptVersionRow(t, ctx, "agent", agentID, 7)
	var taskID string
	if err := testPool.QueryRow(ctx, `INSERT INTO agent_task_queue (agent_id,runtime_id,issue_id,status,priority) VALUES ($1,$2,$3,'queued',0) RETURNING id`, agentID, runtimeID, issueID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id=$1`, taskID) })
	// Zero represents a failed fixed snapshot; it must suppress latest lookup.
	testHandler.recordClaimedPromptVersions(ctx, parseUUID(taskID), map[string]string{"agent": agentID}, map[string]int32{"agent": 0})
	var raw []byte
	if err := testPool.QueryRow(ctx, `SELECT prompt_versions FROM agent_task_queue WHERE id=$1`, taskID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var versions map[string]int32
	if err := json.Unmarshal(raw, &versions); err != nil {
		t.Fatal(err)
	}
	if _, exists := versions["agent"]; exists {
		t.Fatalf("failed snapshot attributed unrelated latest version: %v", versions)
	}
}
