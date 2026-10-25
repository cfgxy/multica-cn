package handler

import (
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/legislation"
)

func TestDirectRevisionPreviewTokenBindsRenderedRevision(t *testing.T) {
	request := DirectClauseRevisionRequest{
		Content:                     "- **每日同步**：预览后的最终正文。",
		ExpectedClauseVersion:       3,
		ExpectedClauseContentSHA256: strings.Repeat("a", 64),
		ExpectedCarrierSHA256:       strings.Repeat("b", 64),
	}
	prepared := directClauseRevisionPrepared{
		clause:  resolvedClause{ID: parseUUID("00000000-0000-0000-0000-000000000001"), Version: 3},
		current: "旧载体",
		next:    "新载体",
	}
	request.PreviewSHA256 = directRevisionPreviewSHA256(prepared, request, "owner-a")
	if !matchesDirectRevisionPreview(request, prepared, "owner-a") {
		t.Fatal("matching preview token was rejected")
	}
	if matchesDirectRevisionPreview(request, prepared, "owner-b") {
		t.Fatal("preview token accepted a different owner")
	}

	request.Content = "- **每日同步**：未预览的替换正文。"
	if matchesDirectRevisionPreview(request, prepared, "owner-a") {
		t.Fatal("preview token accepted different revision content")
	}
}

func TestDirectRevisionStructuralGateDoesNotRequireProposalAnswers(t *testing.T) {
	result := safeRunStructuralGate(
		"# 工作原则\n\n- **每日同步**：旧正文。\n",
		"# 工作原则\n\n- **每日同步**：经 Owner 确认的新正文。\n",
		nil,
		legislation.ProposalInput{
			ChangeKind: "revise_clause",
			ClauseName: "每日同步",
			ClauseText: "- **每日同步**：经 Owner 确认的新正文。",
		},
	)
	if !result.OK() {
		t.Fatalf("direct revision structural gate rejected a reviewed revision without proposal answers: %+v", result.Errors)
	}
}
