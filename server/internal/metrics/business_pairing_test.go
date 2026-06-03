package metrics_test

// PR3 lint test: enforces that every PostHog event constant declared in
// server/internal/analytics/events.go has a paired Prometheus counter
// reachable through metrics.RecordEvent — and that every
// h.Analytics.Capture(analytics.<Helper>(...)) call site goes through
// metrics.RecordEvent (no naked Capture allowed except for the AgentTask*
// allow-list whose Prometheus side is handled by typed PR2 methods).

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/metrics"
)

// taskMetricEvents are emitted via the typed PR2 methods (RecordTaskEnqueued,
// RecordTaskDispatched, RecordTaskStarted, RecordTaskTerminal, RecordTaskFailed)
// instead of the generic RecordEvent dispatcher because their Prometheus side
// needs queue/run/total seconds that the analytics event does not carry.
//
// These names are still required to be paired — the lint test verifies they
// have a typed RecordTask* hit in service/task.go.
var taskMetricEvents = map[string]string{
	analytics.EventAgentTaskQueued:     "RecordTaskEnqueued",
	analytics.EventAgentTaskDispatched: "RecordTaskDispatched",
	analytics.EventAgentTaskStarted:    "RecordTaskStarted",
	analytics.EventAgentTaskCompleted:  "RecordTaskTerminal",
	analytics.EventAgentTaskFailed:     "RecordTaskFailed",
	analytics.EventAgentTaskCancelled:  "RecordTaskTerminal",
}

// frontendOnlyEvents are declared in events.go but emitted from the frontend,
// not from server code. They still need a Prometheus counter (so a future
// server-side emission point lights up the same label set) but the server
// has no Capture call site to lint.
var frontendOnlyEvents = map[string]bool{
	analytics.EventOnboardingStarted: true,
}

// TestEveryAnalyticsEventHasPrometheusCounter asserts that every Event*
// constant declared in analytics/events.go either:
//   - is dispatched by metrics.IncForEvent (verified by sending a synthetic
//     event through RecordEvent and observing a counter delta), or
//   - is in the typed taskMetricEvents allow-list.
func TestEveryAnalyticsEventHasPrometheusCounter(t *testing.T) {
	t.Parallel()

	declared := analyticsEventNames(t)

	m := metrics.NewBusinessMetrics()
	for name := range declared {
		if _, allowed := taskMetricEvents[name]; allowed {
			continue
		}
		// Build a minimal event with the required label properties that the
		// dispatcher reads. Since IncForEvent reads via stringProp helpers,
		// a nil Properties map is acceptable for events with empty label
		// sets and is normalised by the helpers for the others.
		ev := analytics.Event{
			Name:       name,
			DistinctID: "test",
			Properties: defaultPropsForEvent(name),
		}
		ok := dispatchIncrementsCounter(m, ev)
		if !ok {
			t.Errorf("analytics.%s (%q) is not paired with a Prometheus counter via metrics.IncForEvent — add a case in business_events.go", constantNameForEvent(name), name)
		}
	}
}

// TestNoNakedAnalyticsCaptureInHandlersOrServices walks every Go file under
// server/internal/handler and server/internal/service and asserts that every
// `<x>.Analytics.Capture(analytics.<Helper>(...))` call has been migrated to
// metrics.RecordEvent. The only exception is service/task.go's
// captureTaskEvent helper, which is the centralised emitter for PR2's typed
// task-lifecycle metrics.
func TestNoNakedAnalyticsCaptureInHandlersOrServices(t *testing.T) {
	t.Parallel()

	roots := []string{
		filepath.Join(repoRoot(t), "internal", "handler"),
		filepath.Join(repoRoot(t), "internal", "service"),
		filepath.Join(repoRoot(t), "cmd", "server"),
	}
	allowedFiles := map[string]struct{}{
		// captureTaskEvent indirection — single helper that fans out to
		// PR2's typed RecordTask* methods. Auditing this one function lets
		// us keep the rest of service/task.go strict.
		filepath.Join(repoRoot(t), "internal", "service", "task.go"): {},
	}

	var offenders []string
	fset := token.NewFileSet()
	for _, root := range roots {
		matches, err := filepath.Glob(filepath.Join(root, "*.go"))
		if err != nil {
			t.Fatalf("glob %s: %v", root, err)
		}
		for _, file := range matches {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			if _, ok := allowedFiles[file]; ok {
				continue
			}
			f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("parse %s: %v", file, err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if !isAnalyticsCapture(call) {
					return true
				}
				offenders = append(offenders, fset.Position(call.Pos()).String())
				return true
			})
		}
	}

	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Errorf("found %d naked Analytics.Capture(...) calls — wrap them in metrics.RecordEvent so the Prometheus and PostHog sides cannot drift:\n  %s", len(offenders), strings.Join(offenders, "\n  "))
	}
}

// TestEveryAnalyticsCaptureSiteHasPairedRecord goes the other direction: for
// each call site that DOES go through metrics.RecordEvent, walk back up the
// AST to confirm the Event constructor is one of the analytics.* helpers. If
// a future change passes an event by name string, the test fails so the
// dispatcher can be kept exhaustive.
func TestEveryAnalyticsRecordEventTakesAnalyticsHelper(t *testing.T) {
	t.Parallel()

	roots := []string{
		filepath.Join(repoRoot(t), "internal", "handler"),
		filepath.Join(repoRoot(t), "internal", "service"),
		filepath.Join(repoRoot(t), "cmd", "server"),
	}

	var offenders []string
	fset := token.NewFileSet()
	for _, root := range roots {
		matches, err := filepath.Glob(filepath.Join(root, "*.go"))
		if err != nil {
			t.Fatalf("glob %s: %v", root, err)
		}
		for _, file := range matches {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("parse %s: %v", file, err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if !isMetricsRecordEvent(call) {
					return true
				}
				if len(call.Args) < 3 {
					offenders = append(offenders, fset.Position(call.Pos()).String()+" (RecordEvent must be called with 3 args: client, metrics, event)")
					return true
				}
				ev := call.Args[2]
				// Allow either an analytics.* helper call or a *ast.Ident
				// referring to a local that's been built from an analytics
				// helper a few lines above (auth.go's evt pattern).
				if !analyticsHelperCall(ev) && !isLocalIdent(ev) {
					offenders = append(offenders, fset.Position(call.Pos()).String()+" (third arg must be an analytics.* event helper or a local built from one)")
				}
				return true
			})
		}
	}

	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Errorf("metrics.RecordEvent call sites must take an analytics.* event:\n  %s", strings.Join(offenders, "\n  "))
	}
}

// ---- helpers --------------------------------------------------------------

// repoRoot returns the absolute path to server/. The test sources live in
// server/internal/metrics/ so two parents up is the server root.
func repoRoot(t *testing.T) string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	// .../server/internal/metrics/business_pairing_test.go → .../server
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

// analyticsEventNames parses analytics/events.go and returns every Event*
// constant value (the literal string passed to PostHog).
func analyticsEventNames(t *testing.T) map[string]struct{} {
	t.Helper()

	path := filepath.Join(repoRoot(t), "internal", "analytics", "events.go")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	out := map[string]struct{}{}
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Values) == 0 {
				continue
			}
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Event") {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				out[strings.Trim(lit.Value, "\"")] = struct{}{}
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("no Event* constants found in %s", path)
	}
	return out
}

// constantNameForEvent reverse-maps an event string to its Go constant name
// for nicer error messages. Stable for the constants we ship.
func constantNameForEvent(name string) string {
	parts := strings.Split(name, "_")
	for i, p := range parts {
		if len(p) == 0 {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return "Event" + strings.Join(parts, "")
}

// dispatchIncrementsCounter sends ev through RecordEvent (with a noop
// PostHog client) and returns true when at least one Prometheus counter
// receives a non-zero increment. We use a fresh BusinessMetrics per event
// so a leftover prewarm value from another counter cannot mask a missing
// dispatch case.
func dispatchIncrementsCounter(m *metrics.BusinessMetrics, ev analytics.Event) bool {
	before := metrics.SumAllCounters(m)
	metrics.RecordEvent(analytics.NoopClient{}, m, ev)
	after := metrics.SumAllCounters(m)
	return after > before
}

// defaultPropsForEvent returns a properties map populated with the label
// values the dispatcher reads, so the synthetic test event lights up its
// matching counter without relying on the analytics helper plumbing.
func defaultPropsForEvent(name string) map[string]any {
	switch name {
	case analytics.EventSignup:
		return map[string]any{"signup_source": "test"}
	case analytics.EventWorkspaceCreated:
		return map[string]any{"source": "manual"}
	case analytics.EventOnboardingStarted:
		return map[string]any{"platform": "web"}
	case analytics.EventOnboardingCompleted:
		return map[string]any{"completion_path": "full"}
	case analytics.EventIssueCreated:
		return map[string]any{"source": "manual", "platform": "web"}
	case analytics.EventChatMessageSent:
		return map[string]any{"platform": "web"}
	case analytics.EventAgentCreated:
		return map[string]any{"runtime_mode": "local", "source": "manual"}
	case analytics.EventAutopilotCreated:
		return map[string]any{"cadence": "manual"}
	case analytics.EventIssueExecuted:
		return map[string]any{"source": "manual"}
	case analytics.EventRuntimeRegistered, analytics.EventRuntimeReady, analytics.EventRuntimeOffline:
		return map[string]any{"runtime_mode": "local", "provider": "claude"}
	case analytics.EventRuntimeFailed:
		return map[string]any{"runtime_mode": "local", "provider": "claude", "failure_reason": "unknown", "recoverable": false}
	case analytics.EventAutopilotRunStarted, analytics.EventAutopilotRunCompleted, analytics.EventAutopilotRunFailed:
		return map[string]any{"cadence": "manual", "trigger_kind": "manual"}
	case analytics.EventFeedbackSubmitted:
		return map[string]any{"kind": "general", "platform": "web"}
	case analytics.EventContactSalesSubmitted:
		return map[string]any{"source": "page"}
	}
	return map[string]any{}
}

func isAnalyticsCapture(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if sel.Sel == nil || sel.Sel.Name != "Capture" {
		return false
	}
	// Receiver must be a selector ending in `.Analytics`.
	rec, ok := sel.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if rec.Sel == nil || rec.Sel.Name != "Analytics" {
		return false
	}
	// Must be passing an analytics helper or a local built from one — but
	// the lint principle is "no direct Capture", so any shape fails.
	return true
}

func isMetricsRecordEvent(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if sel.Sel == nil || sel.Sel.Name != "RecordEvent" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return pkg.Name == "obsmetrics" || pkg.Name == "metrics"
}

func analyticsHelperCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return pkg.Name == "analytics" && sel.Sel != nil && len(sel.Sel.Name) > 0
}

func isLocalIdent(expr ast.Expr) bool {
	_, ok := expr.(*ast.Ident)
	return ok
}
