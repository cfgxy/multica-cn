package handler

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dto "github.com/prometheus/client_model/go"

	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
)

// captureSuppressObservability swaps in a WARN-capturing default logger and a
// fresh metrics registry for one test, so both halves of the honored-suppress
// trace can be asserted against the same write.
func captureSuppressObservability(t *testing.T) (*bytes.Buffer, *obsmetrics.BusinessMetrics) {
	t.Helper()

	logs := &bytes.Buffer{}
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	m := obsmetrics.NewBusinessMetrics()
	prevMetrics := testHandler.Metrics
	testHandler.Metrics = m
	t.Cleanup(func() { testHandler.Metrics = prevMetrics })

	return logs, m
}

// suppressedRunCount reads multica_issue_run_suppressed_total for one label
// pair. A missing family is a failure, not a zero: the metric is prewarmed, so
// its absence means it was never registered.
func suppressedRunCount(t *testing.T, m *obsmetrics.BusinessMetrics, source, actorType string) float64 {
	t.Helper()
	fam, ok := obsmetrics.GatherForTest(t, m)["multica_issue_run_suppressed_total"]
	if !ok {
		t.Fatal("metric family multica_issue_run_suppressed_total is not registered")
	}
	for _, metric := range fam.GetMetric() {
		labels := map[string]string{}
		for _, pair := range metric.GetLabel() {
			labels[pair.GetName()] = pair.GetValue()
		}
		if labels["source"] == source && labels["actor_type"] == actorType {
			return metric.GetCounter().GetValue()
		}
	}
	t.Fatalf("no series with source=%q actor_type=%q in %s", source, actorType, familyLabels(fam))
	return 0
}

func familyLabels(fam *dto.MetricFamily) string {
	var parts []string
	for _, metric := range fam.GetMetric() {
		var pairs []string
		for _, pair := range metric.GetLabel() {
			pairs = append(pairs, pair.GetName()+"="+pair.GetValue())
		}
		parts = append(parts, "{"+strings.Join(pairs, ",")+"}")
	}
	return strings.Join(parts, " ")
}

func promoteOutOfBacklog(t *testing.T, issueID string, suppressRun bool) {
	t.Helper()
	body := map[string]any{"status": "todo"}
	if suppressRun {
		body["suppress_run"] = true
	}
	w := httptest.NewRecorder()
	req := withURLParam(newRequest("PUT", "/api/issues/"+issueID, body), "id", issueID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHonoredSuppressLogsWarnAndCounts is the RUYI-252 core case: a write that
// WOULD have started a run and was told not to leaves a WARN carrying every id
// needed to find it afterwards, plus a counter increment. Before this the path
// was entirely silent and RUYI-248 could only be reconstructed from agent
// session transcripts.
func TestHonoredSuppressLogsWarnAndCounts(t *testing.T) {
	agentID := seededReadyAgentID(t)
	issue := createIssueForTest(t, map[string]any{
		"title":         "suppress observability warn",
		"status":        "backlog",
		"assignee_type": "agent",
		"assignee_id":   agentID,
	})

	logs, m := captureSuppressObservability(t)
	promoteOutOfBacklog(t, issue.ID, true)

	if n := taskCountFor(t, issue.ID, agentID); n != 0 {
		t.Fatalf("suppressed promote enqueued %d task(s), want 0", n)
	}

	line := suppressWarnLine(t, logs.String())
	for _, want := range []string{
		"issue_id=" + issue.ID,
		"actor_type=member",
		"actor_id=" + testUserID,
		"trigger_source=status",
		"target_status=todo",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("suppress WARN missing %q: %s", want, line)
		}
	}

	if got := suppressedRunCount(t, m, "status", "member"); got != 1 {
		t.Fatalf("issue_run_suppressed_total{source=status,actor_type=member} = %v, want 1", got)
	}
}

// TestHonoredSuppressOnAssignSource covers the other trigger source: assigning
// a ready agent onto an active issue with suppress_run set.
func TestHonoredSuppressOnAssignSource(t *testing.T) {
	agentID := seededReadyAgentID(t)
	issue := createIssueForTest(t, map[string]any{"title": "suppress observability assign", "status": "todo"})

	logs, m := captureSuppressObservability(t)
	w := httptest.NewRecorder()
	req := withURLParam(newRequest("PUT", "/api/issues/"+issue.ID, map[string]any{
		"assignee_type": "agent",
		"assignee_id":   agentID,
		"suppress_run":  true,
	}), "id", issue.ID)
	testHandler.UpdateIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateIssue: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if n := taskCountFor(t, issue.ID, agentID); n != 0 {
		t.Fatalf("suppressed assign enqueued %d task(s), want 0", n)
	}
	if line := suppressWarnLine(t, logs.String()); !strings.Contains(line, "trigger_source=assign") {
		t.Fatalf("suppress WARN missing trigger_source=assign: %s", line)
	}
	if got := suppressedRunCount(t, m, "assign", "member"); got != 1 {
		t.Fatalf("issue_run_suppressed_total{source=assign,actor_type=member} = %v, want 1", got)
	}
}

// TestUnsuppressedPromoteStaysQuiet is the negative half: the ordinary enqueue
// path must not log the suppression WARN or move the counter. Without this the
// first assertion would still pass if the WARN fired on every write.
func TestUnsuppressedPromoteStaysQuiet(t *testing.T) {
	agentID := seededReadyAgentID(t)
	issue := createIssueForTest(t, map[string]any{
		"title":         "suppress observability quiet",
		"status":        "backlog",
		"assignee_type": "agent",
		"assignee_id":   agentID,
	})

	logs, m := captureSuppressObservability(t)
	promoteOutOfBacklog(t, issue.ID, false)

	if n := taskCountFor(t, issue.ID, agentID); n != 1 {
		t.Fatalf("plain promote enqueued %d task(s), want 1", n)
	}
	if strings.Contains(logs.String(), suppressWarnMessage) {
		t.Fatalf("plain promote logged the suppression WARN: %s", logs.String())
	}
	if got := suppressedRunCount(t, m, "status", "member"); got != 0 {
		t.Fatalf("issue_run_suppressed_total{source=status,actor_type=member} = %v, want 0", got)
	}
}

func suppressWarnLine(t *testing.T, logs string) string {
	t.Helper()
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, suppressWarnMessage) {
			if !strings.Contains(line, "level=WARN") {
				t.Fatalf("suppression log is not WARN level: %s", line)
			}
			return line
		}
	}
	t.Fatalf("no suppression WARN in logs: %s", logs)
	return ""
}
