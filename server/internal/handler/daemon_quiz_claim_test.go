package handler

import (
	"context"
	"encoding/json"
	"testing"
)

// The claim-side quiz branch extracts the question only from contexts that
// declare kind=="quiz" — a quick_create payload or any foreign shape must
// never surface as a measurement assignment (RUYI-286).
func TestQuizPromptFromContext(t *testing.T) {
	quizPrompt := "What does the A2 isolation guarantee exclude from a quiz run's payload?"
	raw, _ := json.Marshal(map[string]any{
		"kind":          "quiz",
		"quiz_batch_id": "0198c0de-0000-7000-8000-000000000001",
		"quiz_item_id":  "0198c0de-0000-7000-8000-000000000002",
		"quiz_prompt":   quizPrompt,
	})
	got, ok := quizPromptFromContext(raw)
	if !ok || got != quizPrompt {
		t.Fatalf("quizPromptFromContext(quiz) = (%q, %v), want the prompt, true", got, ok)
	}

	rawQC, _ := json.Marshal(map[string]any{"type": "quick_create", "prompt": "create an issue"})
	if got, ok := quizPromptFromContext(rawQC); ok {
		t.Fatalf("quizPromptFromContext(quick_create) = (%q, %v), want false", got, ok)
	}

	if _, ok := quizPromptFromContext([]byte(`{not json`)); ok {
		t.Fatal("malformed context must not parse as quiz")
	}

	rawNoPrompt, _ := json.Marshal(map[string]any{"kind": "quiz"})
	if got, ok := quizPromptFromContext(rawNoPrompt); ok || got != "" {
		t.Fatalf("quiz context without prompt = (%q, %v), want empty, false", got, ok)
	}

	if _, ok := quizPromptFromContext(nil); ok {
		t.Fatal("nil context must not parse as quiz")
	}
}

// RUYI-286: claiming a quiz task surfaces the question as quiz_prompt without
// flipping any quick-create field — quiz attribution stays quiz's own kind.
func TestClaimTask_QuizContextPopulatesQuizPromptOnly(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID, runtimeID, daemonID := createRuntimeGuardAgent(t, ctx)

	quizPrompt := "Explain in two sentences when a measurement run may influence production statistics."
	quizContext, _ := json.Marshal(map[string]any{
		"kind":          "quiz",
		"quiz_batch_id": "0198c0de-0000-7000-8000-000000000001",
		"quiz_item_id":  "0198c0de-0000-7000-8000-000000000002",
		"quiz_prompt":   quizPrompt,
	})

	dbfx.Exec(t, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, context)
		VALUES ($1, $2, 'queued', 0, $3)
	`, agentID, runtimeID, quizContext)

	task := claimTaskForRuntimeGuard(t, runtimeID, daemonID)
	if task.QuizPrompt != quizPrompt {
		t.Fatalf("quiz task quiz_prompt = %q, want %q", task.QuizPrompt, quizPrompt)
	}
	if task.QuickCreatePrompt != "" {
		t.Fatalf("quiz task leaked into the quick-create prompt field: %q", task.QuickCreatePrompt)
	}
	if task.ThreadName != "" {
		t.Fatalf("quiz task must not take the quick-create thread name, got %q", task.ThreadName)
	}
}
