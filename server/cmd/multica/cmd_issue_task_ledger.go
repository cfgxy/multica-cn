package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

// Issue AC CLI commands: multica issue ac {list|extract|add|verify|reject|delete}
var issueACCmd = &cobra.Command{
	Use:   "ac",
	Short: "Work with issue acceptance criteria (AC)",
	Long:  "Manage structured acceptance criteria for an issue, extract AC from description, verify with evidence, or reject.",
}

var issueACListCmd = &cobra.Command{
	Use:   "list <issue-id>",
	Short: "List acceptance criteria for an issue",
	Args:  exactArgs(1),
	RunE:  runIssueACList,
}

var issueACExtractCmd = &cobra.Command{
	Use:   "extract <issue-id>",
	Short: "Extract acceptance criteria from issue description",
	Args:  exactArgs(1),
	RunE:  runIssueACExtract,
}

var issueACAddCmd = &cobra.Command{
	Use:   "add <issue-id>",
	Short: "Add a structured acceptance criterion",
	Args:  exactArgs(1),
	RunE:  runIssueACAdd,
}

var issueACVerifyCmd = &cobra.Command{
	Use:   "verify <issue-id> <ac-id-or-key>",
	Short: "Verify an acceptance criterion with evidence",
	Args:  exactArgs(2),
	RunE:  runIssueACVerify,
}

var issueACRejectCmd = &cobra.Command{
	Use:   "reject <issue-id> <ac-id-or-key>",
	Short: "Reject an acceptance criterion with a reason",
	Args:  exactArgs(2),
	RunE:  runIssueACReject,
}

var issueACDeleteCmd = &cobra.Command{
	Use:   "delete <issue-id> <ac-id>",
	Short: "Delete an acceptance criterion",
	Args:  exactArgs(2),
	RunE:  runIssueACDelete,
}

// Issue TODO CLI commands: multica issue todo {list|add|update|reopen|delete}
var issueTodoCmd = &cobra.Command{
	Use:   "todo",
	Short: "Work with issue dynamic TODO ledger",
	Long:  "Manage dynamic execution TODOs, link ACs, attach evidence, track next steps, and handle blockers.",
}

var issueTodoListCmd = &cobra.Command{
	Use:   "list <issue-id>",
	Short: "List TODOs for an issue",
	Args:  exactArgs(1),
	RunE:  runIssueTodoList,
}

var issueTodoAddCmd = &cobra.Command{
	Use:   "add <issue-id>",
	Short: "Add a dynamic TODO item",
	Args:  exactArgs(1),
	RunE:  runIssueTodoAdd,
}

var issueTodoUpdateCmd = &cobra.Command{
	Use:   "update <issue-id> <todo-id>",
	Short: "Update status, next step, evidence, or blocker on a TODO item",
	Args:  exactArgs(2),
	RunE:  runIssueTodoUpdate,
}

var issueTodoReopenCmd = &cobra.Command{
	Use:   "reopen <issue-id> <todo-id>",
	Short: "Reopen a completed or blocked TODO item",
	Args:  exactArgs(2),
	RunE:  runIssueTodoReopen,
}

var issueTodoDeleteCmd = &cobra.Command{
	Use:   "delete <issue-id> <todo-id>",
	Short: "Delete a TODO item",
	Args:  exactArgs(2),
	RunE:  runIssueTodoDelete,
}

// Issue Ledger Summary: multica issue ledger <issue-id>
var issueLedgerCmd = &cobra.Command{
	Use:   "ledger <issue-id>",
	Short: "Show unified task ledger (ACs, dynamic TODOs, and audit trail)",
	Args:  exactArgs(1),
	RunE:  runIssueLedger,
}

func init() {
	issueCmd.AddCommand(issueACCmd)
	issueCmd.AddCommand(issueTodoCmd)
	issueCmd.AddCommand(issueLedgerCmd)

	issueACCmd.AddCommand(issueACListCmd)
	issueACCmd.AddCommand(issueACExtractCmd)
	issueACCmd.AddCommand(issueACAddCmd)
	issueACCmd.AddCommand(issueACVerifyCmd)
	issueACCmd.AddCommand(issueACRejectCmd)
	issueACCmd.AddCommand(issueACDeleteCmd)

	issueTodoCmd.AddCommand(issueTodoListCmd)
	issueTodoCmd.AddCommand(issueTodoAddCmd)
	issueTodoCmd.AddCommand(issueTodoUpdateCmd)
	issueTodoCmd.AddCommand(issueTodoReopenCmd)
	issueTodoCmd.AddCommand(issueTodoDeleteCmd)

	// Flags for AC commands
	issueACListCmd.Flags().String("output", "table", "Output format: table or json")
	issueACExtractCmd.Flags().String("output", "json", "Output format: table or json")

	issueACAddCmd.Flags().String("key", "", "AC key (e.g. AC1)")
	issueACAddCmd.Flags().String("title", "", "AC title / acceptance criterion description")
	_ = issueACAddCmd.MarkFlagRequired("title")
	issueACAddCmd.Flags().String("status", "uncompleted", "AC status (uncompleted, pending_verification, verified, rejected)")
	issueACAddCmd.Flags().String("output", "json", "Output format: table or json")

	issueACVerifyCmd.Flags().String("note", "", "Verification note")
	issueACVerifyCmd.Flags().String("evidence-type", "text", "Evidence type (run, commit, pr, test, screenshot, text)")
	issueACVerifyCmd.Flags().String("evidence-url", "", "Evidence url or identifier")
	issueACVerifyCmd.Flags().String("evidence-note", "", "Evidence note")
	issueACVerifyCmd.Flags().String("output", "json", "Output format: table or json")

	issueACRejectCmd.Flags().String("reason", "", "Rejection reason (required)")
	_ = issueACRejectCmd.MarkFlagRequired("reason")
	issueACRejectCmd.Flags().String("note", "", "Rejection note")
	issueACRejectCmd.Flags().String("evidence-type", "text", "Evidence type")
	issueACRejectCmd.Flags().String("evidence-url", "", "Evidence url or identifier")
	issueACRejectCmd.Flags().String("evidence-note", "", "Evidence note")
	issueACRejectCmd.Flags().String("output", "json", "Output format: table or json")

	// Flags for TODO commands
	issueTodoListCmd.Flags().String("output", "table", "Output format: table or json")
	issueTodoListCmd.Flags().String("status", "", "Filter by status (pending, in_progress, completed, blocked)")

	issueTodoAddCmd.Flags().String("title", "", "TODO title / description")
	_ = issueTodoAddCmd.MarkFlagRequired("title")
	issueTodoAddCmd.Flags().String("status", "pending", "Initial status (pending, in_progress, completed, blocked)")
	issueTodoAddCmd.Flags().String("next-step", "", "Immediate next step")
	issueTodoAddCmd.Flags().String("blocked-reason", "", "Blocked reason if blocked")
	issueTodoAddCmd.Flags().StringSlice("ac", nil, "Associated AC keys or IDs (repeatable or comma-separated)")
	issueTodoAddCmd.Flags().String("evidence-type", "", "Evidence type")
	issueTodoAddCmd.Flags().String("evidence-url", "", "Evidence url")
	issueTodoAddCmd.Flags().String("evidence-note", "", "Evidence note")
	issueTodoAddCmd.Flags().String("output", "json", "Output format: table or json")

	issueTodoUpdateCmd.Flags().String("title", "", "Updated title")
	issueTodoUpdateCmd.Flags().String("status", "", "Updated status (pending, in_progress, completed, blocked)")
	issueTodoUpdateCmd.Flags().String("next-step", "", "Updated next step")
	issueTodoUpdateCmd.Flags().String("blocked-reason", "", "Updated blocked reason")
	issueTodoUpdateCmd.Flags().StringSlice("ac", nil, "Associated AC keys or IDs")
	issueTodoUpdateCmd.Flags().String("evidence-type", "", "Evidence type")
	issueTodoUpdateCmd.Flags().String("evidence-url", "", "Evidence url")
	issueTodoUpdateCmd.Flags().String("evidence-note", "", "Evidence note")
	issueTodoUpdateCmd.Flags().String("output", "json", "Output format: table or json")

	issueTodoReopenCmd.Flags().String("next-step", "", "Next step on reopen")
	issueTodoReopenCmd.Flags().String("note", "", "Reopen reason / note")
	issueTodoReopenCmd.Flags().String("output", "json", "Output format: table or json")

	// Flags for Ledger command
	issueLedgerCmd.Flags().String("output", "table", "Output format: table or json")
}

// ---------------- AC Handlers ----------------

func runIssueACList(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	issueRef, err := resolveIssueRef(ctx, client, args[0])
	if err != nil {
		return err
	}

	var acs []map[string]any
	if err := client.GetJSON(ctx, "/api/issues/"+issueRef.ID+"/ac", &acs); err != nil {
		return fmt.Errorf("list ACs: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "table" {
		headers := []string{"KEY", "STATUS", "TITLE", "VERIFIED_AT", "LINKED_TODOS"}
		rows := make([][]string, len(acs))
		for i, ac := range acs {
			linkedTodos := ""
			if lt, ok := ac["linked_todo_ids"].([]any); ok && len(lt) > 0 {
				var ids []string
				for _, id := range lt {
					ids = append(ids, fmt.Sprint(id)[:8])
				}
				linkedTodos = strings.Join(ids, ",")
			}
			rows[i] = []string{
				strVal(ac, "key"),
				strVal(ac, "status"),
				strVal(ac, "title"),
				strVal(ac, "verified_at"),
				linkedTodos,
			}
		}
		cli.PrintTable(os.Stdout, headers, rows)
		return nil
	}

	return cli.PrintJSON(os.Stdout, acs)
}

func runIssueACExtract(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	issueRef, err := resolveIssueRef(ctx, client, args[0])
	if err != nil {
		return err
	}

	var resp map[string]any
	if err := client.PostJSON(ctx, "/api/issues/"+issueRef.ID+"/ac/extract", map[string]any{}, &resp); err != nil {
		return fmt.Errorf("extract ACs: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "table" {
		fmt.Printf("Extracted %v ACs from issue description\n", resp["extracted_count"])
		if acList, ok := resp["acs"].([]any); ok {
			headers := []string{"KEY", "STATUS", "TITLE"}
			rows := make([][]string, len(acList))
			for i, a := range acList {
				if ac, ok := a.(map[string]any); ok {
					rows[i] = []string{
						strVal(ac, "key"),
						strVal(ac, "status"),
						strVal(ac, "title"),
					}
				}
			}
			cli.PrintTable(os.Stdout, headers, rows)
		}
		return nil
	}

	return cli.PrintJSON(os.Stdout, resp)
}

func runIssueACAdd(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	issueRef, err := resolveIssueRef(ctx, client, args[0])
	if err != nil {
		return err
	}

	key, _ := cmd.Flags().GetString("key")
	title, _ := cmd.Flags().GetString("title")
	status, _ := cmd.Flags().GetString("status")

	body := map[string]any{
		"title":  title,
		"status": status,
	}
	if key != "" {
		body["key"] = key
	}

	var created map[string]any
	if err := client.PostJSON(ctx, "/api/issues/"+issueRef.ID+"/ac", body, &created); err != nil {
		return fmt.Errorf("add AC: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "table" {
		fmt.Printf("Created AC: %s - %s [%s]\n", created["key"], created["title"], created["status"])
		return nil
	}
	return cli.PrintJSON(os.Stdout, created)
}

// resolveACID locates an AC by UUID or by key (e.g. "AC1")
func resolveACID(ctx context.Context, client *cli.APIClient, issueID, input string) (string, error) {
	if uuidRegexp.MatchString(input) {
		return input, nil
	}
	var acs []map[string]any
	if err := client.GetJSON(ctx, "/api/issues/"+issueID+"/ac", &acs); err != nil {
		return "", err
	}
	normInput := strings.ToUpper(strings.TrimSpace(input))
	for _, ac := range acs {
		if strings.ToUpper(strVal(ac, "key")) == normInput {
			return strVal(ac, "id"), nil
		}
	}
	return "", fmt.Errorf("acceptance criterion %q not found on issue", input)
}

func runIssueACVerify(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	issueRef, err := resolveIssueRef(ctx, client, args[0])
	if err != nil {
		return err
	}

	acID, err := resolveACID(ctx, client, issueRef.ID, args[1])
	if err != nil {
		return err
	}

	note, _ := cmd.Flags().GetString("note")
	evType, _ := cmd.Flags().GetString("evidence-type")
	evURL, _ := cmd.Flags().GetString("evidence-url")
	evNote, _ := cmd.Flags().GetString("evidence-note")

	body := map[string]any{
		"note": note,
	}
	if evURL != "" || evNote != "" {
		body["evidence"] = []map[string]any{
			{"type": evType, "url": evURL, "note": evNote},
		}
	}

	var updated map[string]any
	if err := client.PostJSON(ctx, "/api/issues/"+issueRef.ID+"/ac/"+acID+"/verify", body, &updated); err != nil {
		return fmt.Errorf("verify AC: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "table" {
		fmt.Printf("Verified %s: %s [Status: %s]\n", updated["key"], updated["title"], updated["status"])
		return nil
	}
	return cli.PrintJSON(os.Stdout, updated)
}

func runIssueACReject(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	issueRef, err := resolveIssueRef(ctx, client, args[0])
	if err != nil {
		return err
	}

	acID, err := resolveACID(ctx, client, issueRef.ID, args[1])
	if err != nil {
		return err
	}

	reason, _ := cmd.Flags().GetString("reason")
	note, _ := cmd.Flags().GetString("note")
	evType, _ := cmd.Flags().GetString("evidence-type")
	evURL, _ := cmd.Flags().GetString("evidence-url")
	evNote, _ := cmd.Flags().GetString("evidence-note")

	body := map[string]any{
		"reason": reason,
		"note":   note,
	}
	if evURL != "" || evNote != "" {
		body["evidence"] = []map[string]any{
			{"type": evType, "url": evURL, "note": evNote},
		}
	}

	var updated map[string]any
	if err := client.PostJSON(ctx, "/api/issues/"+issueRef.ID+"/ac/"+acID+"/reject", body, &updated); err != nil {
		return fmt.Errorf("reject AC: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "table" {
		fmt.Printf("Rejected %s: %s [Reason: %s]\n", updated["key"], updated["title"], updated["rejection_reason"])
		return nil
	}
	return cli.PrintJSON(os.Stdout, updated)
}

func runIssueACDelete(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	issueRef, err := resolveIssueRef(ctx, client, args[0])
	if err != nil {
		return err
	}
	acID, err := resolveACID(ctx, client, issueRef.ID, args[1])
	if err != nil {
		return err
	}

	if err := client.DeleteJSON(ctx, "/api/issues/"+issueRef.ID+"/ac/"+acID); err != nil {
		return fmt.Errorf("delete AC: %w", err)
	}
	fmt.Printf("Deleted AC %s\n", args[1])
	return nil
}

// ---------------- TODO Handlers ----------------

func runIssueTodoList(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	issueRef, err := resolveIssueRef(ctx, client, args[0])
	if err != nil {
		return err
	}

	var todos []map[string]any
	if err := client.GetJSON(ctx, "/api/issues/"+issueRef.ID+"/todos", &todos); err != nil {
		return fmt.Errorf("list TODOs: %w", err)
	}

	statusFilter, _ := cmd.Flags().GetString("status")
	if statusFilter != "" {
		var filtered []map[string]any
		for _, td := range todos {
			if strVal(td, "status") == statusFilter {
				filtered = append(filtered, td)
			}
		}
		todos = filtered
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "table" {
		headers := []string{"ID", "STATUS", "TITLE", "NEXT_STEP", "BLOCKED_REASON", "AC"}
		rows := make([][]string, len(todos))
		for i, td := range todos {
			acs := ""
			if acSlice, ok := td["ac_ids"].([]any); ok {
				var acStrings []string
				for _, a := range acSlice {
					acStrings = append(acStrings, fmt.Sprint(a))
				}
				acs = strings.Join(acStrings, ",")
			}
			rows[i] = []string{
				strVal(td, "id")[:8],
				strVal(td, "status"),
				strVal(td, "title"),
				strVal(td, "next_step"),
				strVal(td, "blocked_reason"),
				acs,
			}
		}
		cli.PrintTable(os.Stdout, headers, rows)
		return nil
	}

	return cli.PrintJSON(os.Stdout, todos)
}

func runIssueTodoAdd(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	issueRef, err := resolveIssueRef(ctx, client, args[0])
	if err != nil {
		return err
	}

	title, _ := cmd.Flags().GetString("title")
	status, _ := cmd.Flags().GetString("status")
	nextStep, _ := cmd.Flags().GetString("next-step")
	blockedReason, _ := cmd.Flags().GetString("blocked-reason")
	acIDs, _ := cmd.Flags().GetStringSlice("ac")

	body := map[string]any{
		"title":          title,
		"status":         status,
		"next_step":      nextStep,
		"blocked_reason": blockedReason,
		"ac_ids":         acIDs,
	}

	evType, _ := cmd.Flags().GetString("evidence-type")
	evURL, _ := cmd.Flags().GetString("evidence-url")
	evNote, _ := cmd.Flags().GetString("evidence-note")
	if evURL != "" || evNote != "" {
		body["evidence"] = []map[string]any{
			{"type": evType, "url": evURL, "note": evNote},
		}
	}

	var created map[string]any
	if err := client.PostJSON(ctx, "/api/issues/"+issueRef.ID+"/todos", body, &created); err != nil {
		return fmt.Errorf("add TODO: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "table" {
		fmt.Printf("Created TODO %s: %s [%s]\n", created["id"], created["title"], created["status"])
		return nil
	}
	return cli.PrintJSON(os.Stdout, created)
}

func runIssueTodoUpdate(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	issueRef, err := resolveIssueRef(ctx, client, args[0])
	if err != nil {
		return err
	}
	todoID := args[1]

	body := make(map[string]any)
	if cmd.Flags().Changed("title") {
		v, _ := cmd.Flags().GetString("title")
		body["title"] = v
	}
	if cmd.Flags().Changed("status") {
		v, _ := cmd.Flags().GetString("status")
		body["status"] = v
	}
	if cmd.Flags().Changed("next-step") {
		v, _ := cmd.Flags().GetString("next-step")
		body["next_step"] = v
	}
	if cmd.Flags().Changed("blocked-reason") {
		v, _ := cmd.Flags().GetString("blocked-reason")
		body["blocked_reason"] = v
	}
	if cmd.Flags().Changed("ac") {
		v, _ := cmd.Flags().GetStringSlice("ac")
		body["ac_ids"] = v
	}

	evType, _ := cmd.Flags().GetString("evidence-type")
	evURL, _ := cmd.Flags().GetString("evidence-url")
	evNote, _ := cmd.Flags().GetString("evidence-note")
	if evURL != "" || evNote != "" {
		body["evidence"] = []map[string]any{
			{"type": evType, "url": evURL, "note": evNote},
		}
	}

	var updated map[string]any
	if err := client.PatchJSON(ctx, "/api/issues/"+issueRef.ID+"/todos/"+todoID, body, &updated); err != nil {
		return fmt.Errorf("update TODO: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "table" {
		fmt.Printf("Updated TODO %s: %s [%s]\n", updated["id"], updated["title"], updated["status"])
		return nil
	}
	return cli.PrintJSON(os.Stdout, updated)
}

func runIssueTodoReopen(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	issueRef, err := resolveIssueRef(ctx, client, args[0])
	if err != nil {
		return err
	}
	todoID := args[1]

	nextStep, _ := cmd.Flags().GetString("next-step")
	note, _ := cmd.Flags().GetString("note")

	body := map[string]any{
		"next_step": nextStep,
		"note":      note,
	}

	var reopened map[string]any
	if err := client.PostJSON(ctx, "/api/issues/"+issueRef.ID+"/todos/"+todoID+"/reopen", body, &reopened); err != nil {
		return fmt.Errorf("reopen TODO: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "table" {
		fmt.Printf("Reopened TODO %s: status set to in_progress [Next step: %s]\n", reopened["id"], reopened["next_step"])
		return nil
	}
	return cli.PrintJSON(os.Stdout, reopened)
}

func runIssueTodoDelete(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	issueRef, err := resolveIssueRef(ctx, client, args[0])
	if err != nil {
		return err
	}
	todoID := args[1]

	if err := client.DeleteJSON(ctx, "/api/issues/"+issueRef.ID+"/todos/"+todoID); err != nil {
		return fmt.Errorf("delete TODO: %w", err)
	}
	fmt.Printf("Deleted TODO %s\n", todoID)
	return nil
}

// ---------------- Ledger Overview Handler ----------------

func runIssueLedger(cmd *cobra.Command, args []string) error {
	client, err := newAPIClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := cli.APIContext(context.Background())
	defer cancel()

	issueRef, err := resolveIssueRef(ctx, client, args[0])
	if err != nil {
		return err
	}

	var ledger map[string]any
	if err := client.GetJSON(ctx, "/api/issues/"+issueRef.ID+"/ledger", &ledger); err != nil {
		return fmt.Errorf("get ledger: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	if output == "table" {
		summary, _ := ledger["summary"].(map[string]any)
		fmt.Printf("=== Issue %s Task Execution Ledger ===\n", issueRef.Display)
		fmt.Printf("ACs: %v Total (%v Verified, %v Pending, %v Rejected)\n",
			summary["ac_total"], summary["ac_verified"], summary["ac_pending"], summary["ac_rejected"])
		fmt.Printf("TODOs: %v Total (%v Completed, %v In Progress, %v Pending, %v Blocked)\n",
			summary["todo_total"], summary["todo_completed"], summary["todo_in_progress"], summary["todo_pending"], summary["todo_blocked"])

		if nextSteps, ok := summary["current_next_steps"].([]any); ok && len(nextSteps) > 0 {
			fmt.Println("\nCurrent Next Steps:")
			for _, step := range nextSteps {
				fmt.Printf(" - %v\n", step)
			}
		}

		fmt.Println("\n--- Acceptance Criteria ---")
		if acList, ok := ledger["acs"].([]any); ok && len(acList) > 0 {
			headers := []string{"KEY", "STATUS", "TITLE"}
			rows := make([][]string, len(acList))
			for i, a := range acList {
				if ac, ok := a.(map[string]any); ok {
					rows[i] = []string{strVal(ac, "key"), strVal(ac, "status"), strVal(ac, "title")}
				}
			}
			cli.PrintTable(os.Stdout, headers, rows)
		} else {
			fmt.Println("No structured AC extracted yet. Run `multica issue ac extract <issue-id>`.")
		}

		fmt.Println("\n--- Dynamic TODOs ---")
		if todoList, ok := ledger["todos"].([]any); ok && len(todoList) > 0 {
			headers := []string{"ID", "STATUS", "TITLE", "NEXT STEP", "BLOCKED REASON"}
			rows := make([][]string, len(todoList))
			for i, t := range todoList {
				if td, ok := t.(map[string]any); ok {
					rows[i] = []string{
						strVal(td, "id")[:8],
						strVal(td, "status"),
						strVal(td, "title"),
						strVal(td, "next_step"),
						strVal(td, "blocked_reason"),
					}
				}
			}
			cli.PrintTable(os.Stdout, headers, rows)
		} else {
			fmt.Println("No TODO items recorded yet.")
		}
		return nil
	}

	return cli.PrintJSON(os.Stdout, ledger)
}
