package outerloop

import "testing"

func TestClassifyReply(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    Intent
	}{
		// --- stop directives ---
		{"bare 停止", "停止", IntentStop},
		{"polite stop", "好的，停止吧", IntentStop},
		{"不用了", "不用了，这个方案先放一放", IntentStop},
		{"取消", "取消这个任务", IntentStop},
		{"中止", "中止", IntentStop},
		{"到此为止", "先到此为止", IntentStop},
		{"english stop", "stop", IntentStop},
		{"english abort", "please abort this run", IntentStop},
		{"english cancel", "cancel", IntentStop},
		{"stop priority over continue", "先取消当前计划，后续再继续", IntentStop},

		// --- negated stop tokens are continue ---
		{"不要停止", "不要停止，继续推进", IntentContinue},
		{"别取消", "别取消，接着来", IntentContinue},
		{"english do not stop", "do not stop, keep going", IntentContinue},
		{"english don't cancel", "don't cancel this", IntentContinue},

		// --- continue / unmatched (default resume, review-confirmed) ---
		{"empty", "", IntentContinue},
		{"plain continue", "继续", IntentContinue},
		{"go on", "继续，按第二轮的方向推进", IntentContinue},
		{"directive", "把测试补上再收尾", IntentContinue},
		{"english continue", "continue with the plan", IntentContinue},
		{"question", "现在进度如何？", IntentContinue},

		// --- technical prose mentioning 停止 also stops: the rule is
		// whole-content token matching without syntax understanding; this
		// locks the accepted trade-off (a misread continue costs one wasted
		// round, a misread stop costs one member re-open) ---
		{"prose mention with directive elsewhere", "服务在停止时会丢请求，请修复", IntentStop},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyReply(tt.content); got != tt.want {
				t.Fatalf("ClassifyReply(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}
