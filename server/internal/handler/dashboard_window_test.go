package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// mustLoc loads a timezone or fails the test — the tests below pin exact
// instants, so a zone that failed to load must stop everything rather than
// silently degrade to UTC.
func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return loc
}

// pinDayWindowClockAt pins the request-side clock at an instant in loc and
// returns it. parseDashboardWindow reads dayWindowNow for the future bound
// and the legacy days cutoff, so both sides of every assertion below agree
// on one moment.
func pinDayWindowClockAt(t *testing.T, clock string, loc *time.Location) time.Time {
	t.Helper()
	at, err := time.ParseInLocation("2006-01-02 15:04", clock, loc)
	if err != nil {
		t.Fatalf("parse clock %q: %v", clock, err)
	}
	pinDayWindowClock(t, at)
	return at
}

// requestWindow runs parseDashboardWindow against a recorder so the 400
// paths can assert the status code.
func requestWindow(
	query string,
	tzName string,
	exact bool,
) (dashboardWindow, bool, *httptest.ResponseRecorder) {
	r := httptest.NewRequest("GET", "/api/dashboard/usage/daily?"+query, nil)
	w := httptest.NewRecorder()
	win, ok := parseDashboardWindow(w, r, tzName, exact)
	return win, ok, w
}

// TestParseDashboardWindowExplicit pins the explicit start/end contract:
// inclusive local calendar dates, since at local midnight of start, until at
// local midnight of the day AFTER end (exclusive), with the future bound
// read against the pinned clock in the viewer's zone.
func TestParseDashboardWindowExplicit(t *testing.T) {
	tokyo := mustLoc(t, "Asia/Tokyo")
	pinDayWindowClockAt(t, "2026-03-10 15:00", tokyo)

	win, ok, w := requestWindow("start=2026-03-01&end=2026-03-05", "Asia/Tokyo", false)
	if !ok {
		t.Fatalf("expected ok, got 400: %s", w.Body.String())
	}
	if !win.since.Valid || !win.until.Valid {
		t.Fatalf("expected both bounds valid")
	}
	wantSince := time.Date(2026, 3, 1, 0, 0, 0, 0, tokyo)
	wantUntil := time.Date(2026, 3, 6, 0, 0, 0, 0, tokyo)
	if !win.since.Time.Equal(wantSince) {
		t.Errorf("since: got %s, want %s", win.since.Time, wantSince)
	}
	if !win.until.Time.Equal(wantUntil) {
		t.Errorf("until: got %s, want %s", win.until.Time, wantUntil)
	}
}

// TestParseDashboardWindowEndsToday allows the current window: end == today
// in the viewer's zone is not "the future".
func TestParseDashboardWindowEndsToday(t *testing.T) {
	la := mustLoc(t, "America/Los_Angeles")
	pinDayWindowClockAt(t, "2026-03-10 06:00", la) // still 03-09 in Tokyo

	win, ok, w := requestWindow("start=2026-03-08&end=2026-03-10", "America/Los_Angeles", true)
	if !ok {
		t.Fatalf("expected ok, got 400: %s", w.Body.String())
	}
	wantUntil := time.Date(2026, 3, 11, 0, 0, 0, 0, la)
	if !win.until.Time.Equal(wantUntil) {
		t.Errorf("until: got %s, want %s", win.until.Time, wantUntil)
	}
}

// TestParseDashboardWindowOverridesDays pins the precedence contract: when
// explicit start/end ride along with days, the explicit window wins and days
// is ignored. Old clients never send start/end, so the precedence is
// unobservable to them — but it must be fixed and tested, not accidental.
func TestParseDashboardWindowOverridesDays(t *testing.T) {
	tokyo := mustLoc(t, "Asia/Tokyo")
	pinDayWindowClockAt(t, "2026-03-10 15:00", tokyo)

	win, ok, w := requestWindow("days=1&start=2026-03-01&end=2026-03-05", "Asia/Tokyo", false)
	if !ok {
		t.Fatalf("expected ok, got 400: %s", w.Body.String())
	}
	wantSince := time.Date(2026, 3, 1, 0, 0, 0, 0, tokyo)
	if !win.since.Time.Equal(wantSince) {
		t.Errorf("since: got %s, want %s (days must be ignored)", win.since.Time, wantSince)
	}
}

// TestParseDashboardWindowLegacyDays pins backward compatibility: with no
// start/end the window degrades to exactly what the pre-existing cutoff
// parsers produce, and until is invalid so the SQL drops the upper bound —
// the shape every existing caller relies on.
func TestParseDashboardWindowLegacyDays(t *testing.T) {
	tokyo := mustLoc(t, "Asia/Tokyo")
	at := pinDayWindowClockAt(t, "2026-03-10 15:00", tokyo)

	t.Run("daily series keeps the N+1 headroom", func(t *testing.T) {
		win, ok, w := requestWindow("days=7", "Asia/Tokyo", false)
		if !ok {
			t.Fatalf("expected ok, got 400: %s", w.Body.String())
		}
		want := sinceFromDays(at, 7, tokyo)
		if !win.since.Valid || !win.since.Time.Equal(want) {
			t.Errorf("since: got %v, want %s", win.since, want)
		}
		if win.until.Valid {
			t.Errorf("legacy window must have no upper bound, got %s", win.until.Time)
		}
	})

	t.Run("per-agent rollups use the exact cutoff", func(t *testing.T) {
		win, ok, w := requestWindow("days=7", "Asia/Tokyo", true)
		if !ok {
			t.Fatalf("expected ok, got 400: %s", w.Body.String())
		}
		want := sinceFromDays(at, 6, tokyo)
		if !win.since.Valid || !win.since.Time.Equal(want) {
			t.Errorf("since: got %v, want %s", win.since, want)
		}
		if win.until.Valid {
			t.Errorf("legacy window must have no upper bound, got %s", win.until.Time)
		}
	})

	t.Run("default days=30 when the parameter is absent", func(t *testing.T) {
		win, ok, w := requestWindow("", "Asia/Tokyo", false)
		if !ok {
			t.Fatalf("expected ok, got 400: %s", w.Body.String())
		}
		want := sinceFromDays(at, 30, tokyo)
		if !win.since.Valid || !win.since.Time.Equal(want) {
			t.Errorf("since: got %v, want %s", win.since, want)
		}
	})
}

// TestParseDashboardWindowDST covers a window spanning a spring-forward
// boundary: bounds stay anchored at genuine local midnights on both sides
// (2025-03-09 is the 23-hour day in Los Angeles).
func TestParseDashboardWindowDST(t *testing.T) {
	la := mustLoc(t, "America/Los_Angeles")
	pinDayWindowClockAt(t, "2025-03-15 12:00", la)

	win, ok, w := requestWindow("start=2025-03-08&end=2025-03-10", "America/Los_Angeles", false)
	if !ok {
		t.Fatalf("expected ok, got 400: %s", w.Body.String())
	}
	wantSince := time.Date(2025, 3, 8, 0, 0, 0, 0, la) // PST, -08:00
	wantUntil := time.Date(2025, 3, 11, 0, 0, 0, 0, la) // PDT, -07:00
	if !win.since.Time.Equal(wantSince) {
		t.Errorf("since: got %s, want %s", win.since.Time, wantSince)
	}
	if !win.until.Time.Equal(wantUntil) {
		t.Errorf("until: got %s, want %s", win.until.Time, wantUntil)
	}
}

// TestParseDashboardWindowRejectsInvalid pins every 400: half-provided
// pairs, non-ISO dates, inverted ranges, future bounds, and oversized spans.
// The window is a read-only filter, but a silently-wrong window is worse
// than a loud rejection — these are contract tests, not politeness.
func TestParseDashboardWindowRejectsInvalid(t *testing.T) {
	tokyo := mustLoc(t, "Asia/Tokyo")
	pinDayWindowClockAt(t, "2026-03-10 15:00", tokyo)

	cases := []struct {
		name  string
		query string
	}{
		{"start without end", "start=2026-03-01"},
		{"end without start", "end=2026-03-05"},
		{"non-ISO start", "start=03/01/2026&end=2026-03-05"},
		{"non-ISO end", "start=2026-03-01&end=not-a-date"},
		{"start after end", "start=2026-03-05&end=2026-03-01"},
		{"end in the future", "start=2026-03-01&end=2026-03-11"},
		{"span over 365 days", "start=2025-01-01&end=2026-03-10"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, ok, w := requestWindow(tc.query, "Asia/Tokyo", false)
			if ok {
				t.Fatalf("expected 400, got ok")
			}
			if w.Code != 400 {
				t.Errorf("status: got %d, want 400", w.Code)
			}
		})
	}
}

// dashboardWindowStatus runs a handler method against a raw query string and
// returns the recorder, so one helper covers the 200 and 400 assertions.
func dashboardWindowStatus(
	call func(w http.ResponseWriter, r *http.Request),
	query string,
) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	call(w, newRequest("GET", "/api/dashboard/usage/daily?"+query, nil))
	return w
}

// TestDashboardExplicitHistoricalWindow pins the start/end contract end to
// end against the database: a run completed on a PAST calendar day is
// returned when the request names a window covering that day (token rollup
// keyed on created_at, run time on completed_at), absent when the window
// excludes it, absent from the legacy current-window request, and present
// for a legacy wide-enough days=N request — the backward-compatibility
// positive control. The future/half-provided/invalid requests read 400.
func TestDashboardExplicitHistoricalWindow(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	loc := dashboardFixtureLoc(t)

	var runtimeID, agentID string
	dbfx.QueryRow(t, `
		SELECT id FROM agent_runtime WHERE workspace_id = $1 LIMIT 1
	`, testWorkspaceID).Scan(&runtimeID)
	dbfx.QueryRow(t, `
		SELECT id FROM agent WHERE workspace_id = $1 LIMIT 1
	`, testWorkspaceID).Scan(&agentID)

	// Pin the clock: "today" is 2026-03-10 in the fixture zone, so the run
	// below (2026-03-05) is five days back. days=30 reaches it; days=1 and
	// the 03-08..03-09 window do not.
	pinDayWindowClock(t, time.Date(2026, 3, 10, 12, 0, 0, 0, loc))
	pastDay := time.Date(2026, 3, 5, 0, 0, 0, 0, loc)
	started := pastDay.Add(10 * time.Minute)
	completed := pastDay.Add(20 * time.Minute)
	usageAt := started.Add(time.Minute)

	var issueID string
	dbfx.QueryRow(t, `
		INSERT INTO issue (workspace_id, title, creator_id, creator_type, number)
		VALUES (
			$1, 'dashboard historical window test', $2, 'member',
			(SELECT COALESCE(MAX(number), 0) + 1 FROM issue WHERE workspace_id = $1)
		)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&issueID)
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID) })

	taskID := dbfx.Task(t, agentID, testutil.Cols{
		"issue_id":     issueID,
		"runtime_id":   runtimeID,
		"status":       "completed",
		"started_at":   started,
		"completed_at": completed,
		"created_at":   started,
	})
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })
	dbfx.Exec(t, `
		INSERT INTO task_usage (task_id, provider, model, input_tokens, output_tokens, created_at)
		VALUES ($1, 'claude', 'claude-3-5-sonnet', 777, 0, $2)
	`, taskID, usageAt)

	// Aggregate the fixture into task_usage_hourly before any assertion —
	// production lags five minutes behind the same cron tick.
	dbfx.Exec(t, `
		SELECT rollup_task_usage_hourly_window('1970-01-01'::timestamptz, now() + interval '1 hour')
	`)

	t.Run("explicit single-day window returns the historical run", func(t *testing.T) {
		w := httptest.NewRecorder()
		testHandler.GetDashboardUsageDaily(w, newRequest("GET",
			"/api/dashboard/usage/daily?start=2026-03-05&end=2026-03-05&"+dashboardFixtureTZParam, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var rows []struct {
			Date        string `json:"date"`
			InputTokens int64  `json:"input_tokens"`
		}
		_ = json.NewDecoder(w.Body).Decode(&rows)
		if len(rows) == 0 {
			t.Fatalf("expected the 2026-03-05 bucket, got none")
		}
		for _, r := range rows {
			if r.Date != "2026-03-05" {
				t.Errorf("date %q outside the requested window", r.Date)
			}
		}
		total := int64(0)
		for _, r := range rows {
			total += r.InputTokens
		}
		if total < 777 {
			t.Errorf("expected >=777 tokens on 2026-03-05, got %d", total)
		}
	})

	t.Run("per-agent run time honors the explicit bounds", func(t *testing.T) {
		type row struct {
			AgentID      string `json:"agent_id"`
			TotalSeconds int64  `json:"total_seconds"`
			TaskCount    int32  `json:"task_count"`
		}
		read := func(query string) []row {
			w := httptest.NewRecorder()
			testHandler.GetDashboardAgentRunTime(w, newRequest("GET",
				"/api/dashboard/agent-runtime?"+query+"&"+dashboardFixtureTZParam, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("query %q: expected 200, got %d: %s", query, w.Code, w.Body.String())
			}
			var rows []row
			_ = json.NewDecoder(w.Body).Decode(&rows)
			return rows
		}
		count := func(rows []row) int32 {
			var n int32
			for _, r := range rows {
				n += r.TaskCount
			}
			return n
		}
		if got := count(read("start=2026-03-05&end=2026-03-05")); got != 1 {
			t.Errorf("historical window: task_count = %d, want 1", got)
		}
		if got := count(read("start=2026-03-08&end=2026-03-09")); got != 0 {
			t.Errorf("window excluding the run: task_count = %d, want 0", got)
		}
		if got := count(read("days=1")); got != 0 {
			t.Errorf("legacy current window: task_count = %d, want 0", got)
		}
		if got := count(read("days=30")); got != 1 {
			t.Errorf("legacy days=30 (compat control): task_count = %d, want 1", got)
		}
	})

	t.Run("per-agent failures window", func(t *testing.T) {
		w := httptest.NewRecorder()
		testHandler.GetDashboardFailuresByAgent(w, newRequest("GET",
			"/api/dashboard/failures/by-agent?start=2026-03-05&end=2026-03-05&"+dashboardFixtureTZParam, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var rows []struct {
			FailureReason string `json:"failure_reason"`
			TaskCount     int32  `json:"task_count"`
		}
		_ = json.NewDecoder(w.Body).Decode(&rows)
		succeeded := int32(0)
		for _, r := range rows {
			if r.FailureReason == "" {
				succeeded += r.TaskCount
			}
		}
		if succeeded != 1 {
			t.Errorf("succeeded bucket = %d, want 1", succeeded)
		}
	})

	t.Run("invalid windows read 400", func(t *testing.T) {
		cases := []string{
			"start=2026-03-05",
			"end=2026-03-05",
			"start=2026-03-05&end=03/06/2026",
			"start=2026-03-06&end=2026-03-05",
			"start=2026-03-01&end=2026-03-11", // end beyond pinned today (03-10)
		}
		for _, q := range cases {
			w := httptest.NewRecorder()
			testHandler.GetDashboardUsageDaily(w, newRequest("GET",
				"/api/dashboard/usage/daily?"+q+"&"+dashboardFixtureTZParam, nil))
			if w.Code != http.StatusBadRequest {
				t.Errorf("query %q: expected 400, got %d", q, w.Code)
			}
		}
	})
}
