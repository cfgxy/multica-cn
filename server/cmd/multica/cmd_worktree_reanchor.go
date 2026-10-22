package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/daemon/execenv"
)

var worktreeReanchorCmd = &cobra.Command{
	Use:   "reanchor",
	Short: "Re-anchor a managed branch onto a verified content commit, with an audit trail",
	Long: "Re-anchor moves a managed task/conversation branch from a stale local chain onto a " +
		"verified content commit that sits directly on the shared mainline tip, so the next managed " +
		"task starts from the mainline base instead of replaying the stale chain. The old tip and the " +
		"old branch record are archived under refs/multica/reanchor/ before anything moves, the " +
		"branch record's checkpoint is re-pinned to the new tip (same user-state snapshot, same " +
		"owner), and one line is appended to .multica/reanchor-audit.jsonl. It refuses to run while " +
		"a delivery-guard refusal marker holds the branch.\n\n" +
		"The target must be exactly one commit ahead of --expected-base; pass the freshly fetched " +
		"mainline tip for --expected-base and the verified content commit for --target.",
	RunE: runWorktreeReanchor,
}

var (
	reanchorBranch       string
	reanchorTarget       string
	reanchorExpectedBase string
	reanchorNoteText     string
	reanchorIssue        string
	reanchorRepoRoot     string
)

func init() {
	worktreeCmd.AddCommand(worktreeReanchorCmd)
	worktreeReanchorCmd.Flags().StringVar(&reanchorBranch, "branch", "", "managed branch to re-anchor (required)")
	worktreeReanchorCmd.Flags().StringVar(&reanchorTarget, "target", "", "verified content commit to stand on (required)")
	worktreeReanchorCmd.Flags().StringVar(&reanchorExpectedBase, "expected-base", "", "verified mainline tip the target must sit directly on (required)")
	worktreeReanchorCmd.Flags().StringVar(&reanchorNoteText, "note", "", "free-text reason recorded on the branch record and the audit line")
	worktreeReanchorCmd.Flags().StringVar(&reanchorIssue, "issue", "", "issue identifier for the audit trail")
	worktreeReanchorCmd.Flags().StringVar(&reanchorRepoRoot, "repo-root", "", "git root to operate on (default: current working directory)")
}

func runWorktreeReanchor(cmd *cobra.Command, _ []string) error {
	for _, required := range []struct{ name, value string }{
		{"--branch", reanchorBranch},
		{"--target", reanchorTarget},
		{"--expected-base", reanchorExpectedBase},
	} {
		if required.value == "" {
			return fmt.Errorf("%s is required", required.name)
		}
	}
	root := reanchorRepoRoot
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get current directory: %w", err)
		}
		root = cwd
	}
	note := execenv.ReanchorNote{
		OperatorAgent: os.Getenv("MULTICA_AGENT_ID"),
		OperatorTask:  os.Getenv("MULTICA_TASK_ID"),
		Issue:         reanchorIssue,
		Reason:        reanchorNoteText,
	}
	result, err := execenv.ReanchorBranch(root, reanchorBranch, reanchorTarget, reanchorExpectedBase, note)
	if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
		return fmt.Errorf("write reanchor result: %w", err)
	}
	return err
}
