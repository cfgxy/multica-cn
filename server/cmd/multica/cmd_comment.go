package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

// commentCmd groups comment-level operations that are not scoped under a single
// issue (resolve / unresolve act on a comment id directly). Listing and adding
// comments stay under `issue comment` because they read/write an issue's
// timeline; resolving a thread is a comment-id operation, so it lives here.
var commentCmd = &cobra.Command{
	Use:   "comment",
	Short: "Work with comments",
}

var commentResolveCmd = &cobra.Command{
	Use:   "resolve <comment-id>",
	Short: "Mark a comment thread as resolved",
	Long: "Resolve a root comment (thread). Use this once a thread is fully " +
		"handled so it drops out of the unresolved set. The id must be the " +
		"thread root, not a reply — the server rejects replies.",
	Args: exactArgs(1),
	RunE: runCommentResolve,
}

func init() {
	commentResolveCmd.Flags().String("output", "json", "Output format: table or json")
	commentCmd.AddCommand(commentResolveCmd)
}

func runCommentResolve(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	commentID := args[0]

	var result map[string]any
	if err := client.PostJSON(ctx, "/api/comments/"+commentID+"/resolve", nil, &result); err != nil {
		return fmt.Errorf("resolve comment: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Comment %s resolved.\n", commentID)

	output, _ := cmd.Flags().GetString("output")
	if output == "table" {
		return nil
	}
	return cli.PrintJSON(os.Stdout, result)
}
