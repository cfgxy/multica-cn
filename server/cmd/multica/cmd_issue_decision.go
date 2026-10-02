package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

// Decision cards (RUYI-345): an agent raises a structured question with 2-4
// options during a run; the workspace member answers by clicking an option in
// the web UI; the platform echoes the answer as a comment that mentions the
// creating agent, which resumes the run through the standard mention
// pipeline. This command is the creation channel — usable from any runtime.

var issueDecisionCmd = &cobra.Command{
	Use:   "decision",
	Short: "Work with issue decision cards",
	Long: "Decision cards put a structured question in front of the workspace owner.\n\n" +
		"Create a card with `add` during a run; the owner picks options in the web\n" +
		"comment stream; the platform replies with the chosen options and wakes the\n" +
		"creating agent via the usual mention pipeline. Cards cannot be answered\n" +
		"from the CLI by design — answering is the owner's act in the UI.",
}

var issueDecisionAddCmd = &cobra.Command{
	Use:   "add <issue-id>",
	Short: "Create a decision card on an issue",
	Args:  exactArgs(1),
	RunE:  runIssueDecisionAdd,
}

var issueDecisionListCmd = &cobra.Command{
	Use:   "list <issue-id>",
	Short: "List decision cards on an issue",
	Args:  exactArgs(1),
	RunE:  runIssueDecisionList,
}

func init() {
	issueCmd.AddCommand(issueDecisionCmd)
	issueDecisionCmd.AddCommand(issueDecisionAddCmd)
	issueDecisionCmd.AddCommand(issueDecisionListCmd)

	issueDecisionAddCmd.Flags().String("question", "", "The decision question (1-500 characters)")
	issueDecisionAddCmd.Flags().StringSlice("option", nil, "Option label (repeatable; exactly 2-4 options, each 1-200 characters)")
	issueDecisionAddCmd.Flags().Bool("multi", false, "Allow selecting multiple options (default single-select)")
	issueDecisionAddCmd.Flags().IntSlice("recommended", nil, "0-based indices of recommended options (repeatable)")
	issueDecisionAddCmd.Flags().String("source-comment", "", "Comment ID the card originates from (typically the current task's trigger comment)")
	issueDecisionAddCmd.Flags().String("output", "json", "Output format: table or json")

	issueDecisionListCmd.Flags().String("output", "json", "Output format: table or json")
}

// validateDecisionOptions mirrors the server-side create validation so the
// common typos fail offline; the server remains authoritative.
func validateDecisionOptions(question string, options []string, recommended []int) error {
	if len(question) == 0 || len([]rune(question)) > 500 {
		return fmt.Errorf("--question is required (1-500 characters)")
	}
	if len(options) < 2 || len(options) > 4 {
		return fmt.Errorf("--option must be given 2-4 times, got %d", len(options))
	}
	seen := make(map[string]struct{}, len(options))
	for _, o := range options {
		label := o
		if label == "" {
			return fmt.Errorf("--option labels must not be empty")
		}
		if len([]rune(label)) > 200 {
			return fmt.Errorf("--option labels must be at most 200 characters")
		}
		if _, dup := seen[label]; dup {
			return fmt.Errorf("--option labels must be unique")
		}
		seen[label] = struct{}{}
	}
	for _, idx := range recommended {
		if idx < 0 || idx >= len(options) {
			return fmt.Errorf("--recommended index %d out of range (0-%d)", idx, len(options)-1)
		}
	}
	return nil
}

func runIssueDecisionAdd(cmd *cobra.Command, args []string) error {
	question, _ := cmd.Flags().GetString("question")
	options, _ := cmd.Flags().GetStringSlice("option")
	multi, _ := cmd.Flags().GetBool("multi")
	recommended, _ := cmd.Flags().GetIntSlice("recommended")
	if err := validateDecisionOptions(question, options, recommended); err != nil {
		return err
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

	body := map[string]any{
		"question": question,
		"options":  options,
	}
	if multi {
		body["multi_select"] = true
	}
	if len(recommended) > 0 {
		body["recommended_indices"] = recommended
	}
	if sourceComment, _ := cmd.Flags().GetString("source-comment"); sourceComment != "" {
		body["source_comment_id"] = sourceComment
	}

	var result map[string]any
	if err := client.PostJSON(ctx, "/api/issues/"+issueRef.ID+"/decisions", body, &result); err != nil {
		return fmt.Errorf("add decision card: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Decision card created on issue %s.\n", issueRef.Display)

	output, _ := cmd.Flags().GetString("output")
	if output == "table" {
		if id, _ := result["id"].(string); id != "" {
			fmt.Fprintf(os.Stdout, "%s\n", id)
			return nil
		}
	}
	return cli.PrintJSON(os.Stdout, result)
}

func runIssueDecisionList(cmd *cobra.Command, args []string) error {
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

	var result []map[string]any
	if err := client.GetJSON(ctx, "/api/issues/"+issueRef.ID+"/decisions", &result); err != nil {
		return fmt.Errorf("list decision cards: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "table" {
		for _, card := range result {
			id, _ := card["id"].(string)
			status, _ := card["status"].(string)
			question, _ := card["question"].(string)
			fmt.Fprintf(os.Stdout, "%s\t%s\t%s\n", id, status, question)
		}
		return nil
	}
	return cli.PrintJSON(os.Stdout, result)
}
