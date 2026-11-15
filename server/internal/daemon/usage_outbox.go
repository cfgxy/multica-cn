package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type pendingTaskUsage struct {
	TaskID string           `json:"task_id"`
	Usage  []TaskUsageEntry `json:"usage"`
}

func usageOutboxPath(root, taskID string) string {
	digest := sha256.Sum256([]byte(taskID))
	return filepath.Join(root, ".usage-outbox", hex.EncodeToString(digest[:])+".json")
}

// reportUsageDurably writes before sending. The server replaces cumulative
// snapshots atomically, so retrying the exact report never adds usage twice.
func (d *Daemon) reportUsageDurably(ctx context.Context, taskID string, usage []TaskUsageEntry) error {
	if len(usage) == 0 {
		return nil
	}
	d.usageOutboxMu.Lock()
	defer d.usageOutboxMu.Unlock()
	path := usageOutboxPath(d.cfg.WorkspacesRoot, taskID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("persist usage outbox directory: %w", err)
	}
	raw, err := json.Marshal(pendingTaskUsage{TaskID: taskID, Usage: usage})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".usage-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	if err := d.client.ReportTaskUsage(ctx, taskID, usage); err != nil {
		return err
	}
	return os.Remove(path)
}

func (d *Daemon) flushUsageOutbox(ctx context.Context) {
	entries, err := os.ReadDir(filepath.Join(d.cfg.WorkspacesRoot, ".usage-outbox"))
	if err != nil {
		return
	}
	d.usageOutboxMu.Lock()
	defer d.usageOutboxMu.Unlock()
	processed := 0
	for _, entry := range entries {
		if ctx.Err() != nil || processed >= 32 {
			return
		}
		if !entry.Type().IsRegular() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(d.cfg.WorkspacesRoot, ".usage-outbox", entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var report pendingTaskUsage
		if json.Unmarshal(raw, &report) != nil || report.TaskID == "" || len(report.Usage) == 0 || usageOutboxPath(d.cfg.WorkspacesRoot, report.TaskID) != path {
			continue
		}
		processed++
		requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = d.client.ReportTaskUsage(requestCtx, report.TaskID, report.Usage)
		cancel()
		if err == nil {
			_ = os.Remove(path)
		}
	}
}

func (d *Daemon) usageOutboxLoop(ctx context.Context) {
	d.flushUsageOutbox(ctx)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.flushUsageOutbox(ctx)
		}
	}
}
