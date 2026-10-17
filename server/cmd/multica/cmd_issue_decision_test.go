package main

import (
	"strings"
	"testing"

	"github.com/spf13/pflag"
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
		{"200-rune label accepted", "Q", []string{"A", strings.Repeat("长", 200)}, nil, false},
		{"201-rune label rejected", "Q", []string{"A", strings.Repeat("长", 201)}, nil, true},
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

func TestParseDecisionContextJSON(t *testing.T) {
	value, err := parseDecisionContextJSON(`{"why_now":"window closes","facts":[{"statement":"build passed","evidence":["CI 1"]}]}`)
	if err != nil {
		t.Fatalf("parse structured context: %v", err)
	}
	facts, ok := value["facts"].([]any)
	if !ok || len(facts) != 1 {
		t.Fatalf("facts = %#v, want one fact", value["facts"])
	}
	empty, err := parseDecisionContextJSON(`{}`)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("parse empty optional context: value=%#v err=%v", empty, err)
	}
	for _, raw := range []string{"null", "[]", "not-json"} {
		if _, err := parseDecisionContextJSON(raw); err == nil {
			t.Fatalf("parseDecisionContextJSON(%q) succeeded, want error", raw)
		}
	}
}

func TestDecisionContextFlagIsOptional(t *testing.T) {
	f := issueDecisionAddCmd.Flags().Lookup("context")
	if f == nil {
		t.Fatal("--context flag is not registered on the decision add command")
	}
	if f.DefValue != "" {
		t.Fatalf("--context default = %q, want empty for backward-compatible calls", f.DefValue)
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

// RUYI-691: option labels are free-form text that may legitimately contain
// English commas (e.g. "{provider_id, model, advanced}") and braces, so each
// --option occurrence must arrive as exactly one verbatim option. The
// StringSlice flag type CSV-splits every occurrence; StringArray does not.
func TestOptionFlagKeepsEachOccurrenceVerbatim(t *testing.T) {
	flags := issueDecisionAddCmd.Flags()
	f := flags.Lookup("option")
	if f == nil {
		t.Fatal("--option flag is not registered on the decision add command")
	}
	sv, ok := f.Value.(pflag.SliceValue)
	if !ok {
		t.Fatalf("--option flag value does not implement pflag.SliceValue, got %T", f.Value)
	}

	// Each CLI occurrence of --option calls Value.Set once; parse the real
	// flag set exactly as the CLI would and read the parsed labels back.
	parse := func(t *testing.T, labels ...string) []string {
		t.Helper()
		if err := sv.Replace(nil); err != nil {
			t.Fatalf("reset --option: %v", err)
		}
		f.Changed = false
		args := make([]string, 0, 2*len(labels))
		for _, label := range labels {
			args = append(args, "--option", label)
		}
		if err := flags.Parse(args); err != nil {
			t.Fatalf("parse %q: %v", args, err)
		}
		return sv.GetSlice()
	}

	t.Run("english commas and braces stay one option", func(t *testing.T) {
		const structured = "目标模型：agent 覆盖统一为 {provider_id, model, advanced} 结构"
		got := parse(t, structured, "回滚")
		if len(got) != 2 {
			t.Fatalf("--option given twice produced %d options (%q), want 2 — a label was split", len(got), got)
		}
		if got[0] != structured {
			t.Fatalf("option 0 = %q, want verbatim %q", got[0], structured)
		}
		if got[1] != "回滚" {
			t.Fatalf("option 1 = %q, want %q", got[1], "回滚")
		}
	})

	t.Run("chinese commas stay one option", func(t *testing.T) {
		got := parse(t, "方案甲，含中文逗号", "方案乙")
		if len(got) != 2 || got[0] != "方案甲，含中文逗号" {
			t.Fatalf("got %q, want the label kept whole", got)
		}
	})

	t.Run("plain labels keep count and order", func(t *testing.T) {
		got := parse(t, "Approve", "Reject", "Needs info")
		if len(got) != 3 || got[0] != "Approve" || got[2] != "Needs info" {
			t.Fatalf("got %q, want [Approve Reject Needs info]", got)
		}
	})

	t.Run("production reader matches the registered flag type", func(t *testing.T) {
		got := parse(t, "{provider_id, model, advanced}", "回滚")
		viaReader, err := flags.GetStringArray("option")
		if err != nil {
			t.Fatalf("GetStringArray (runIssueDecisionAdd's reader) failed on the registered --option flag: %v", err)
		}
		if len(viaReader) != len(got) {
			t.Fatalf("reader saw %d options, parse produced %d", len(viaReader), len(got))
		}
	})
}
