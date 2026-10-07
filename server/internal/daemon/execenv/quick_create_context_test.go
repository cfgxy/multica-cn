package execenv

import (
	"strings"
	"testing"
)

// RUYI-478: quick-create attachments arrive as full rows (id, filename,
// content type, durable markdown URL). The brief must surface them so the
// delegated create-run can inline images into the new issue's description —
// the whole reason the image previously landed only in the attachment area
// is that nothing told the agent the attachments existed.
func TestRenderQuickCreateContextRendersAttachmentMetas(t *testing.T) {
	t.Parallel()
	ctx := TaskContextForEnv{
		QuickCreatePrompt: "create a task with a screenshot",
		QuickCreateAttachments: []QuickCreateAttachmentForEnv{
			{
				ID:          "019aff3c-1111-7000-8000-000000000001",
				Filename:    "1000013602.jpg",
				ContentType: "image/jpeg",
				MarkdownURL: "https://multica.example.com/api/attachments/019aff3c-1111-7000-8000-000000000001/download",
			},
			{
				ID:          "019aff3c-1111-7000-8000-000000000002",
				Filename:    "spec.pdf",
				ContentType: "application/pdf",
				MarkdownURL: "https://multica.example.com/api/attachments/019aff3c-1111-7000-8000-000000000002/download",
			},
		},
	}
	md := renderQuickCreateContext(ctx)

	for _, want := range []string{
		"## Attachments",
		// every attachment exposes its id, filename and a referenceable
		// markdown form
		"`019aff3c-1111-7000-8000-000000000001`",
		"1000013602.jpg",
		"image/jpeg",
		"![1000013602.jpg](https://multica.example.com/api/attachments/019aff3c-1111-7000-8000-000000000001/download)",
		"`019aff3c-1111-7000-8000-000000000002`",
		"spec.pdf",
		"application/pdf",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("renderQuickCreateContext output missing %q\n---\n%s", want, md)
		}
	}

	// the image/file distinction must be visible as a criterion, and the
	// non-image attachment must not get an image embed
	if !strings.Contains(md, "image/") || !strings.Contains(md, "application/pdf") {
		t.Errorf("renderQuickCreateContext does not separate image from file attachments by content type\n---\n%s", md)
	}
	if strings.Contains(md, "![spec.pdf]") {
		t.Errorf("non-image attachment rendered as an image embed\n---\n%s", md)
	}

	// quick-create hard-guardrails forbid every CLI call other than
	// `issue create`, so the brief must never route the agent through
	// `multica attachment download` on this surface
	if strings.Contains(md, "attachment download") {
		t.Errorf("quick-create brief tells the agent to download attachments; only `issue create` is permitted on this surface\n---\n%s", md)
	}
}

// No-attachment quick-create tasks must render byte-identically to the
// pre-RUYI-478 output: the section only exists when attachments exist.
func TestRenderQuickCreateContextWithoutAttachmentsUnchanged(t *testing.T) {
	t.Parallel()
	md := renderQuickCreateContext(TaskContextForEnv{QuickCreatePrompt: "create a plain task"})

	want := "# Quick Create\n\n**Trigger:** Quick-create modal\n\n## User input\n\n> create a plain task\n\n"
	if md != want {
		t.Errorf("no-attachment quick-create context changed\n--- got ---\n%q\n--- want ---\n%q", md, want)
	}
	if strings.Contains(md, "Attachments") {
		t.Errorf("no-attachment quick-create context grew an attachments section\n---\n%s", md)
	}
}
