package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var issueNoActionCmd = &cobra.Command{
	Use:   "no-action <issue-id>",
	Short: "Record that the current ordinary issue task needs no material action",
	Args:  exactArgs(1),
	RunE:  runIssueNoAction,
}

func runIssueNoAction(cmd *cobra.Command, args []string) error {
	reason, _ := cmd.Flags().GetString("reason")
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fmt.Errorf("--reason is required")
	}
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()
	issueRef, err := resolveIssueRef(ctx, client, args[0])
	if err != nil {
		return fmt.Errorf("resolve issue: %w", err)
	}
	var result map[string]any
	if err := client.PostJSON(ctx, "/api/issues/"+url.PathEscape(issueRef.ID)+"/no-action", map[string]any{"reason": reason}, &result); err != nil {
		return fmt.Errorf("record no_action: %w", err)
	}
	fmt.Fprintf(os.Stderr, "No action recorded for issue %s.\n", issueRef.Display)
	output, _ := cmd.Flags().GetString("output")
	if output == "json" {
		return cli.PrintJSON(os.Stdout, result)
	}
	return nil
}

func init() {
	issueCmd.AddCommand(issueNoActionCmd)
	issueNoActionCmd.Flags().String("reason", "", "Short reason no material action is needed")
	issueNoActionCmd.Flags().String("output", "json", "Output format: table or json")
}
