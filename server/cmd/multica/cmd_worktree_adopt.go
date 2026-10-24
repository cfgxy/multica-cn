package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var worktreeAdoptCmd = &cobra.Command{
	Use:   "adopt",
	Short: "Adopt an authorized suspended task branch into this continuation task",
	Args:  exactArgs(0),
	RunE:  runWorktreeAdopt,
}

var (
	worktreeAdoptSourceTask        string
	worktreeAdoptBranch            string
	worktreeAdoptExpectedRemoteSHA string
)

func init() {
	worktreeCmd.AddCommand(worktreeAdoptCmd)
	worktreeAdoptCmd.Flags().StringVar(&worktreeAdoptSourceTask, "source-task", "", "suspended task recorded as owning the branch (required)")
	worktreeAdoptCmd.Flags().StringVar(&worktreeAdoptBranch, "branch", "", "managed branch to adopt (required)")
	worktreeAdoptCmd.Flags().StringVar(&worktreeAdoptExpectedRemoteSHA, "expected-remote-sha", "", "exact expected origin branch SHA (required)")
}

func runWorktreeAdopt(cmd *cobra.Command, _ []string) error {
	for _, required := range []struct{ name, value string }{
		{"--source-task", strings.TrimSpace(worktreeAdoptSourceTask)},
		{"--branch", strings.TrimSpace(worktreeAdoptBranch)},
		{"--expected-remote-sha", strings.TrimSpace(worktreeAdoptExpectedRemoteSHA)},
	} {
		if required.value == "" {
			return fmt.Errorf("%s is required", required.name)
		}
	}
	port := strings.TrimSpace(os.Getenv("MULTICA_DAEMON_PORT"))
	if port == "" {
		return fmt.Errorf("MULTICA_DAEMON_PORT not set (worktree adoption requires an active daemon task)")
	}
	token := strings.TrimSpace(os.Getenv("MULTICA_TOKEN"))
	if token == "" {
		return fmt.Errorf("MULTICA_TOKEN not set (worktree adoption requires the active task credential)")
	}
	workDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}
	body, err := json.Marshal(map[string]string{
		"source_task_id":      strings.TrimSpace(worktreeAdoptSourceTask),
		"branch":              strings.TrimSpace(worktreeAdoptBranch),
		"expected_remote_sha": strings.TrimSpace(worktreeAdoptExpectedRemoteSHA),
		"workdir":             workDir,
	})
	if err != nil {
		return fmt.Errorf("encode adoption request: %w", err)
	}
	parentCtx := cmd.Context()
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(parentCtx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://127.0.0.1:"+port+"/worktree/adopt", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create daemon adoption request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read daemon adoption response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("branch adoption failed: %s", strings.TrimSpace(string(responseBody)))
	}
	var result struct {
		Branch           string `json:"branch"`
		RemoteSHA        string `json:"remote_sha"`
		SourceTaskID     string `json:"source_task_id"`
		ContinuationTask string `json:"continuation_task_id"`
		Record           string `json:"record"`
		BaseCommit       string `json:"base_commit"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return fmt.Errorf("parse daemon adoption response: %w", err)
	}
	if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
		return fmt.Errorf("write adoption result: %w", err)
	}
	return nil
}
