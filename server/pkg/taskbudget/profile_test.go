package taskbudget

import (
	"testing"
	"time"
)

func TestParseProfile(t *testing.T) {
	for _, raw := range []string{`{"budget_profile":null}`, `{"budget_profile":[]}`, `{"budget_profile":{"max_runs":-1}}`, `{"budget_profile":{"max_runs":"2"}}`, `{"budget_profile":{"max_runs":1.5}}`, `{"budget_profile":{"max_runs":null}}`, `{"budget_profile":{"max_runs":9223372036854775808}}`, `{"budget_profile":{"per_run_timeout_seconds":9223372036854775807}}`, `{"budget_profile":{"typo":1}}`} {
		if _, err := ParseRuntimeConfig([]byte(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, raw := range []string{`{}`, `{"budget_profile":{}}`, `{"budget_profile":{"max_runs":0,"max_total_tokens":2,"max_cost_usd_ticks":3,"per_run_timeout_seconds":4}}`} {
		if _, err := ParseRuntimeConfig([]byte(raw)); err != nil {
			t.Errorf("rejected %s: %v", raw, err)
		}
	}
}

func TestProfileDenial(t *testing.T) {
	p := Profile{MaxRuns: 3, MaxTotalTokens: 100, MaxCostUSDTicks: 50}
	cases := []struct {
		runs                    int64
		tokens, cost            string
		unknown, closed, denied bool
	}{
		{0, "0", "0", false, false, false}, {3, "0", "0", false, false, true},
		{1, "100", "0", false, false, true}, {1, "0", "50", false, false, true},
		{1, "0", "0", true, false, true}, {0, "0", "0", false, true, true},
		{1, "92233720368547758080", "0", false, false, true},
	}
	for _, c := range cases {
		if got := p.Denial(c.runs, c.tokens, c.cost, c.unknown, c.closed) != ""; got != c.denied {
			t.Errorf("%+v: denied=%v", c, got)
		}
	}
	if got := (Profile{}).Denial(100, "999", "999", true, false); got != "" {
		t.Fatal(got)
	}
}

func TestProfileSnapshotAndTimeout(t *testing.T) {
	s, err := FromContext([]byte(`{"head_sha":"abc","budget_scope":{"scope_id":"scope","profile":{"max_runs":2}}}`))
	if err != nil || s.Profile.MaxRuns != 2 {
		t.Fatalf("%+v %v", s, err)
	}
	for _, raw := range []string{`{"budget_scope":{}}`, `{"budget_scope":{"scope_id":"s"}}`, `{"budget_scope":{"scope_id":"s","profile":{"max_runs":-2}}}`} {
		if _, err := FromContext([]byte(raw)); err == nil {
			t.Errorf("accepted corrupt snapshot %s", raw)
		}
	}
	for _, version := range []string{`2`, `0`, `-1`, `null`, `"1"`, `1.5`} {
		if _, err := FromContext([]byte(`{"budget_scope":{"schema_version":` + version + `,"scope_id":"s","profile":{}}}`)); err == nil {
			t.Errorf("accepted unknown schema version %s", version)
		}
	}
	s, err = FromContext([]byte(`{"budget_scope":{"schema_version":1,"scope_id":"s","profile":{}}}`))
	if err != nil || s.SchemaVersion != 1 {
		t.Fatalf("version 1 rejected: %+v %v", s, err)
	}
	if legacy, err := FromContext([]byte(`{"head_sha":"legacy"}`)); err != nil || legacy != nil {
		t.Fatal("unbudgeted legacy acquired a snapshot")
	}
	p := Profile{PerRunTimeoutSeconds: 8}
	if p.Timeout(time.Hour) != 8*time.Second || p.Timeout(time.Second) != time.Second || p.Timeout(0) != 8*time.Second || (Profile{}).Timeout(time.Hour) != time.Hour {
		t.Fatal("timeout cap mismatch")
	}
}
