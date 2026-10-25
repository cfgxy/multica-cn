package execenv

import (
	"strings"
	"testing"
)

// Uploaded attachments are already bound to the new issue. Their names and
// types reach the agent for context, but attachment URLs stay out of the body.
func TestRenderQuickCreateContextDoesNotRepeatUploadedAttachments(t *testing.T) {
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
		"## 原始附件",
		"已上传附件（随新任务自动绑定）",
		"1000013602.jpg",
		"image/jpeg",
		"spec.pdf",
		"application/pdf",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("renderQuickCreateContext output missing %q\n---\n%s", want, md)
		}
	}

	for _, unwanted := range []string{
		"019aff3c-1111-7000-8000-000000000001",
		"019aff3c-1111-7000-8000-000000000002",
		"https://multica.example.com/api/attachments/019aff3c-1111-7000-8000-000000000001/download",
		"https://multica.example.com/api/attachments/019aff3c-1111-7000-8000-000000000002/download",
		"![",
		"!file[",
	} {
		if strings.Contains(md, unwanted) {
			t.Errorf("renderQuickCreateContext repeated attachment detail %q\n---\n%s", unwanted, md)
		}
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
