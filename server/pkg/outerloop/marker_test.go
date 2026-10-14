package outerloop

import "testing"

// TestHasEndMarker locks the marker contract: whole-line declarative hits,
// markdown wrapping tolerance, negation exclusion, and the accepted
// code-fence bias. Ticket status is not an input — the function takes only
// the report text (验收 1).
func TestHasEndMarker(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		// --- clean hits ---
		{"bare marker", "STAGE_COMPLETE", true},
		{"workspace convention line", "阶段状态：STAGE_COMPLETE", true},
		{"colon+period suffix", "阶段状态：STAGE_COMPLETE。", true},
		{"leading marker only", "STAGE_COMPLETE", true},
		{"multiline with marker line", "本轮完成实现与自测。\n阶段状态：STAGE_COMPLETE", true},
		{"whitespace padded", "   STAGE_COMPLETE   ", true},

		// --- markdown wrapping tolerance ---
		{"bold wrapped", "**阶段状态：STAGE_COMPLETE**", true},
		{"backtick wrapped", "`STAGE_COMPLETE`", true},
		{"blockquote prefix", "> 阶段状态：STAGE_COMPLETE", true},
		{"list item", "- 阶段状态：STAGE_COMPLETE", true},
		{"numbered list", "3. 阶段状态：STAGE_COMPLETE", true},
		{"heading prefix", "## 阶段状态：STAGE_COMPLETE", true},
		{"bracket wrapped", "【STAGE_COMPLETE】", true},
		{"corner-bracket wrapped", "「STAGE_COMPLETE」", true},
		{"bold full line", "**STAGE_COMPLETE**", true},

		// --- accepted bias: code fence content counts (early stop is the
		// cheaper wrong direction; locked per review note) ---
		{"marker inside code fence", "示例：\n```\nSTAGE_COMPLETE\n```\n以上仅为示例。", true},

		// --- negation exclusion (先失败样例) ---
		{"negated with 未写", "本阶段未写【STAGE_COMPLETE】，因子单仍在跑。", false},
		{"negated with 没写", "我没有写 STAGE_COMPLETE。", false},
		{"negated with 没有", "本轮没有 STAGE_COMPLETE。", false},
		{"negated with 不含", "以下报告不含 STAGE_COMPLETE 标记。", false},
		{"negated with 缺少", "缺少 STAGE_COMPLETE，故继续。", false},
		{"negated with 尚未", "尚未 STAGE_COMPLETE。", false},
		{"negated english not", "I did not write STAGE_COMPLETE.", false},
		{"negated english no", "no STAGE_COMPLETE in this report", false},
		{"negated english missing", "missing STAGE_COMPLETE, continuing", false},
		{"negated english without", "without STAGE_COMPLETE the loop continues", false},
		{"negated plus real marker elsewhere", "未写【STAGE_COMPLETE】。\n阶段状态：STAGE_COMPLETE", true},

		// --- structural misses ---
		{"empty", "", false},
		{"marker mid-line does not count", "阶段完成：STAGE_COMPLETE，下一步见下文。", false},
		{"plain prose", "阶段基本完成，还差验收。", false},
		{"marker is substring of longer token", "阶段状态：STAGE_COMPLETED", false},
		{"lowercase does not count", "stage_complete", false},
		{"unrelated mention", "按 STAGE_COMPLETE 约定，本行在中间提及。", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasEndMarker(tt.content); got != tt.want {
				t.Fatalf("HasEndMarker(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

// TestHasEndMarkerTicketStatusNotAnInput documents 验收 1's requirement that
// ticket state plays no role: the predicate's signature takes text only, so
// any caller-side temptation to branch on done/cancelled is a compile-time
// non-option. The assertion keeps the contract honest if the signature ever
// grows parameters.
func TestHasEndMarkerTicketStatusNotAnInput(t *testing.T) {
	same := "阶段状态：STAGE_COMPLETE"
	for _, ticketStatus := range []string{"done", "cancelled", "in_progress", "custom_done_key"} {
		if got := HasEndMarker(same); !got {
			t.Fatalf("marker detection must not depend on ticket status %q", ticketStatus)
		}
	}
}
