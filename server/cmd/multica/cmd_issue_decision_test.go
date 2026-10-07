package main

import (
	"strings"
	"testing"
)

func TestValidateDecisionOptions(t *testing.T) {
	validOpts := []string{"Approve", "Reject", "Needs info"}

	cases := []struct {
		name        string
		question    string
		options     []string
		recommended []int
		wantErr     bool
	}{
		{"two options ok", "Ship now?", []string{"Yes", "No"}, nil, false},
		{"four options ok", "Ship now?", []string{"A", "B", "C", "D"}, []int{0, 2}, false},
		{"one option rejected", "Ship now?", []string{"Yes"}, nil, true},
		{"five options rejected", "Ship now?", []string{"A", "B", "C", "D", "E"}, nil, true},
		{"empty question rejected", "", validOpts, nil, true},
		{"empty label rejected", "Q", []string{"A", ""}, nil, true},
		{"duplicate labels rejected", "Q", []string{"A", "A"}, nil, true},
		{"recommendation out of range rejected", "Q", validOpts, []int{3}, true},
		{"negative recommendation rejected", "Q", validOpts, []int{-1}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDecisionOptions(tc.question, tc.options, tc.recommended)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateDecisionOptions(%q, %v, %v) error = %v, wantErr %v",
					tc.question, tc.options, tc.recommended, err, tc.wantErr)
			}
		})
	}
}

// RUYI-514: the run-scoped retry key must be stable across retries of the
// same decision (even with rewritten flags) and distinct across runs,
// issues, and questions.
func TestDeriveDecisionIdempotencyKey(t *testing.T) {
	const (
		taskID  = "01a113f8-745c-768c-a2b5-31d6ffffae54"
		issueID = "01a113f0-0000-7000-8000-000000000123"
	)

	t.Run("stable across retries with rewritten flags", func(t *testing.T) {
		a := deriveDecisionIdempotencyKey(taskID, issueID, "扩量矩阵选哪个候选？")
		b := deriveDecisionIdempotencyKey(taskID, issueID, "扩量矩阵选哪个候选？")
		if a != b {
			t.Fatalf("same inputs derived different keys: %s vs %s", a, b)
		}
	})

	t.Run("whitespace around the question does not change the key", func(t *testing.T) {
		a := deriveDecisionIdempotencyKey(taskID, issueID, "  Which plan ships? ")
		b := deriveDecisionIdempotencyKey(taskID, issueID, "Which plan ships?")
		if a != b {
			t.Fatalf("whitespace changed the key: %s vs %s", a, b)
		}
	})

	t.Run("distinct per run, issue, and question", func(t *testing.T) {
		base := deriveDecisionIdempotencyKey(taskID, issueID, "Q")
		if other := deriveDecisionIdempotencyKey("01a113f8-0000-0000-0000-00000000ffff", issueID, "Q"); other == base {
			t.Fatal("different run must derive a different key")
		}
		if other := deriveDecisionIdempotencyKey(taskID, "01a113f0-0000-7000-8000-000000000999", "Q"); other == base {
			t.Fatal("different issue must derive a different key")
		}
		if other := deriveDecisionIdempotencyKey(taskID, issueID, "Q2"); other == base {
			t.Fatal("different question must derive a different key")
		}
	})

	t.Run("key shape stays within the server limit", func(t *testing.T) {
		longTask := strings.Repeat("t", 200)
		key := deriveDecisionIdempotencyKey(longTask, issueID, "Q")
		if !strings.HasPrefix(key, "task-"+longTask+"-") {
			t.Fatalf("key lacks the task prefix: %q", key)
		}
		if len(key) > 255 {
			t.Fatalf("key length %d exceeds the server's 255-char client_request_id ceiling", len(key))
		}
	})
}
