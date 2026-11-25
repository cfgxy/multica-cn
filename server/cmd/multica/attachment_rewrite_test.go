package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/cli"
)

func TestRewriteUploadedAttachmentRefs(t *testing.T) {
	t.Chdir(t.TempDir())

	const attID = "11111111-1111-4111-8111-111111111111"
	const pdfID = "22222222-2222-4222-8222-222222222222"
	uploaded := []uploadedAttachment{
		{
			localPath: "./shots/demo.png",
			resp: cli.AttachmentResponse{
				ID:          attID,
				Filename:    "demo.png",
				MarkdownURL: "https://cdn.example.test/api/attachments/" + attID + "/download",
			},
		},
		{
			localPath: "docs/report.pdf",
			resp: cli.AttachmentResponse{
				ID:       pdfID,
				Filename: "report.pdf",
			},
		},
	}

	input := strings.Join([]string{
		"Verification screenshot:",
		"",
		"![shot](attachment://demo.png)",
		"![by relpath](attachment://./shots/demo.png?v=1#top)",
		"![by id](attachment://" + attID + ")",
		"![plain relative](./shots/demo.png)",
		"![basename only](demo.png)",
		"!file[report.pdf](attachment://report.pdf)",
		"",
		"| Column | Preview |",
		"| --- | --- |",
		"| Table | ![table](attachment://demo.png) |",
		"",
		"Inline code `![example](attachment://demo.png)` must not change.",
		"Unrelated [readme](README.md) and [site](https://example.com) must stay.",
		"",
		"```md",
		"![fenced](attachment://demo.png)",
		"!file[report.pdf](attachment://report.pdf)",
		"```",
	}, "\n")

	got := rewriteUploadedAttachmentRefs(input, uploaded)
	imgURL := "https://cdn.example.test/api/attachments/" + attID + "/download"
	pdfURL := "/api/attachments/" + pdfID + "/download"

	want := strings.Join([]string{
		"Verification screenshot:",
		"",
		"![shot](" + imgURL + ")",
		"![by relpath](" + imgURL + ")",
		"![by id](" + imgURL + ")",
		"![plain relative](" + imgURL + ")",
		"![basename only](" + imgURL + ")",
		"!file[report.pdf](" + pdfURL + ")",
		"",
		"| Column | Preview |",
		"| --- | --- |",
		"| Table | ![table](" + imgURL + ") |",
		"",
		"Inline code `![example](attachment://demo.png)` must not change.",
		"Unrelated [readme](README.md) and [site](https://example.com) must stay.",
		"",
		"```md",
		"![fenced](attachment://demo.png)",
		"!file[report.pdf](attachment://report.pdf)",
		"```",
	}, "\n")

	if got != want {
		t.Fatalf("rewritten markdown mismatch.\ngot:\n%s\n\nwant:\n%s", got, want)
	}
}

func TestRunIssueCommentAddRewritesInlineAttachmentsAndRejectsLocalPaths(t *testing.T) {
	const issueID = "11111111-1111-4111-8111-111111111111"
	const attID = "22222222-2222-4222-8222-222222222222"
	const pdfID = "33333333-3333-4333-8333-333333333333"
	const durableImg = "https://cdn.example.test/api/attachments/" + attID + "/download"

	var postedBody map[string]any
	uploadCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/issues/"+issueID:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": issueID, "identifier": "TST-1"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/upload-file":
			uploadCount++
			_, header, err := r.FormFile("file")
			if err != nil {
				t.Fatalf("missing file field: %v", err)
			}
			if header.Filename == "demo.png" {
				_ = json.NewEncoder(w).Encode(cli.AttachmentResponse{
					ID:          attID,
					Filename:    "demo.png",
					MarkdownURL: durableImg,
				})
				return
			}
			// Fallback stub without markdown_url.
			_ = json.NewEncoder(w).Encode(map[string]any{"id": pdfID})
		case r.Method == http.MethodPost && r.URL.Path == "/api/issues/"+issueID+"/comments":
			if err := json.NewDecoder(r.Body).Decode(&postedBody); err != nil {
				t.Fatalf("decode comment body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "comment-1"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	setCLITestServerEnv(t, srv.URL)
	t.Setenv("MULTICA_TOKEN", "mat_test-token")

	t.Chdir(t.TempDir())
	if err := os.MkdirAll("shots", 0o755); err != nil {
		t.Fatalf("mkdir shots: %v", err)
	}
	if err := os.WriteFile(filepath.Join("shots", "demo.png"), []byte("png"), 0o644); err != nil {
		t.Fatalf("write demo.png: %v", err)
	}
	if err := os.WriteFile("report.pdf", []byte("pdf"), 0o644); err != nil {
		t.Fatalf("write report.pdf: %v", err)
	}

	body := strings.Join([]string{
		"Result:",
		"",
		"![preview](attachment://demo.png)",
		"",
		"!file[report.pdf](attachment://report.pdf)",
		"",
		"Code sample `![preview](attachment://demo.png)` stays literal.",
	}, "\n")

	cmd := newIssueCommentAddTestCmd()
	_ = cmd.Flags().Set("content", body)
	_ = cmd.Flags().Set("attachment", "./shots/demo.png")
	_ = cmd.Flags().Set("attachment", "report.pdf")

	if err := runIssueCommentAdd(cmd, []string{issueID}); err != nil {
		t.Fatalf("runIssueCommentAdd: %v", err)
	}
	if uploadCount != 2 {
		t.Fatalf("uploadCount = %d, want 2", uploadCount)
	}

	gotContent, _ := postedBody["content"].(string)
	if !strings.Contains(gotContent, "![preview]("+durableImg+")") {
		t.Errorf("expected rewritten image URL in content, got:\n%s", gotContent)
	}
	if !strings.Contains(gotContent, "!file[report.pdf](/api/attachments/"+pdfID+"/download)") {
		t.Errorf("expected fallback download URL for file card in content, got:\n%s", gotContent)
	}
	if !strings.Contains(gotContent, "`![preview](attachment://demo.png)`") {
		t.Errorf("inline code sample must remain unchanged, got:\n%s", gotContent)
	}

	// guardLocalPathLinks must still reject file:// and absolute paths in agent task context.
	withAgentContext(t)
	cmdBad := newIssueCommentAddTestCmd()
	_ = cmdBad.Flags().Set("content", "![bad](file:///tmp/secret.png)")
	if err := runIssueCommentAdd(cmdBad, []string{issueID}); err == nil {
		t.Fatalf("expected file:// link to be rejected by guardLocalPathLinks")
	}
}

func TestRunIssueCreateUploadsAndRewritesDescriptionBeforePost(t *testing.T) {
	const attID = "44444444-4444-4444-8444-444444444444"
	const durableImg = "https://cdn.example.test/api/attachments/" + attID + "/download"

	var order []string
	var createBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/upload-file":
			order = append(order, "upload")
			_ = json.NewEncoder(w).Encode(cli.AttachmentResponse{
				ID:          attID,
				Filename:    "demo.png",
				MarkdownURL: durableImg,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/issues":
			order = append(order, "create")
			if err := json.NewDecoder(r.Body).Decode(&createBody); err != nil {
				t.Fatalf("decode create body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":         "issue-1",
				"identifier": "TST-2",
				"title":      "With inline image",
				"status":     "todo",
				"priority":   "none",
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	setCLITestServerEnv(t, srv.URL)
	t.Setenv("MULTICA_TOKEN", "test-token")

	t.Chdir(t.TempDir())
	if err := os.WriteFile("demo.png", []byte("png"), 0o644); err != nil {
		t.Fatalf("write demo.png: %v", err)
	}

	cmd := newIssueCreateTestCmd()
	_ = cmd.Flags().Set("title", "With inline image")
	_ = cmd.Flags().Set("description", "See ![shot](attachment://demo.png)")
	_ = cmd.Flags().Set("attachment", "demo.png")

	if err := runIssueCreate(cmd, nil); err != nil {
		t.Fatalf("runIssueCreate: %v", err)
	}
	if strings.Join(order, ",") != "upload,create" {
		t.Fatalf("request order = %v, want [upload create]", order)
	}
	if got, _ := createBody["description"].(string); got != "See ![shot]("+durableImg+")" {
		t.Errorf("description = %q, want %q", got, "See ![shot]("+durableImg+")")
	}
	ids, _ := createBody["attachment_ids"].([]any)
	if len(ids) != 1 || ids[0] != attID {
		t.Errorf("attachment_ids = %#v, want [%s]", ids, attID)
	}
}

func TestAttachmentFlagHelpMentionsInlineSyntax(t *testing.T) {
	for _, cmd := range []struct {
		name string
		flag string
	}{
		{"create", issueCreateCmd.Flags().Lookup("attachment").Usage},
		{"comment add", issueCommentAddCmd.Flags().Lookup("attachment").Usage},
	} {
		if !strings.Contains(cmd.flag, "attachment://<filename>") {
			t.Errorf("%s --attachment help should document attachment://<filename>, got %q", cmd.name, cmd.flag)
		}
	}
}
