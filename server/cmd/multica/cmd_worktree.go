package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

var worktreeCmd = &cobra.Command{
	Use:   "worktree",
	Short: "Inspect a managed task worktree",
}

var worktreePreflightCmd = &cobra.Command{
	Use:   "preflight",
	Short: "Check whether the current task worktree can pass delivery guards",
	Args:  exactArgs(0),
	RunE:  runWorktreePreflight,
}

func runWorktreePreflight(cmd *cobra.Command, _ []string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}
	result, preflightErr := execenv.PreflightWorktree(cwd)
	if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
		return fmt.Errorf("write preflight result: %w", err)
	}
	return preflightErr
}
