package metrics

import "strings"

// PR3 normalizers. All inputs go through fixed allow-lists so a misbehaving
// caller cannot inflate metric cardinality. Every "unknown" / "other" bucket
// keeps the series count bounded even under enum drift.

var (
	knownPlatforms = map[string]string{
		"server":  "server",
		"web":     "web",
		"desktop": "desktop",
		"cli":     "cli",
		"mobile":  "mobile",
		"ios":     "ios",
		"unknown": "unknown",
	}

	knownOnboardingPaths = map[string]string{
		"full":            "full",
		"runtime_skipped": "runtime_skipped",
		"cloud_waitlist":  "cloud_waitlist",
		"skip_existing":   "skip_existing",
		"invite_accept":   "invite_accept",
		"unknown":         "unknown",
	}

	knownAutopilotCadences = map[string]string{
		"hourly":  "hourly",
		"daily":   "daily",
		"weekly":  "weekly",
		"monthly": "monthly",
		"manual":  "manual",
		"webhook": "webhook",
		"unknown": "unknown",
	}

	knownAutopilotTriggers = map[string]string{
		"schedule": "schedule",
		"webhook":  "webhook",
		"manual":   "manual",
		"unknown":  "unknown",
	}

	knownAutopilotSkipReasons = map[string]string{
		"already_running":          "already_running",
		"recent_run":               "recent_run",
		"runtime_offline":          "runtime_offline",
		"throttled":                "throttled",
		"max_concurrency":          "max_concurrency",
		"trigger_disabled":         "trigger_disabled",
		"signature_invalid":        "signature_invalid",
		"unknown":                  "unknown",
		"other":                    "other",
	}

	knownWebhookProviders = map[string]string{
		"github":  "github",
		"generic": "generic",
		"gitlab":  "gitlab",
		"stripe":  "stripe",
		"other":   "other",
	}

	knownWebhookDeliveryStatuses = map[string]string{
		"queued":     "queued",
		"dispatched": "dispatched",
		"failed":     "failed",
		"rejected":   "rejected",
		"ignored":    "ignored",
		"duplicate":  "duplicate",
		"other":      "other",
	}

	knownGithubEventKinds = map[string]string{
		"pull_request":         "pull_request",
		"pull_request_review":  "pull_request_review",
		"issues":               "issues",
		"issue_comment":        "issue_comment",
		"push":                 "push",
		"installation":         "installation",
		"installation_repositories": "installation_repositories",
		"check_run":            "check_run",
		"check_suite":          "check_suite",
		"ping":                 "ping",
		"other":                "other",
	}

	knownGithubActions = map[string]string{
		"opened":         "opened",
		"closed":         "closed",
		"reopened":       "reopened",
		"merged":         "merged",
		"synchronize":    "synchronize",
		"edited":         "edited",
		"submitted":      "submitted",
		"created":        "created",
		"deleted":        "deleted",
		"labeled":        "labeled",
		"unlabeled":      "unlabeled",
		"assigned":       "assigned",
		"unassigned":     "unassigned",
		"requested":      "requested",
		"completed":      "completed",
		"none":           "none",
		"other":          "other",
	}

	knownGithubPRReviewResults = map[string]string{
		"approved":          "approved",
		"changes_requested": "changes_requested",
		"commented":         "commented",
		"dismissed":         "dismissed",
		"other":             "other",
	}

	knownCloudRuntimeOps = map[string]string{
		"provision": "provision",
		"terminate": "terminate",
		"status":    "status",
		"gateway":   "gateway",
		"billing":   "billing",
		"fleet":     "fleet",
		"other":     "other",
	}

	knownCloudRuntimeStatuses = map[string]string{
		"ok":      "ok",
		"4xx":     "4xx",
		"5xx":     "5xx",
		"timeout": "timeout",
		"error":   "error",
	}

	knownDaemonWSKinds = map[string]string{
		"heartbeat":     "heartbeat",
		"task_claim":    "task_claim",
		"task_complete": "task_complete",
		"task_usage":    "task_usage",
		"task_progress": "task_progress",
		"task_messages": "task_messages",
		"log":           "log",
		"other":         "other",
	}

	knownFeedbackKinds = map[string]string{
		"bug":     "bug",
		"feature": "feature",
		"general": "general",
		"praise":  "praise",
		"other":   "other",
	}

	knownContactSalesSources = map[string]string{
		"page":         "page",
		"onboarding":   "onboarding",
		"agents_page":  "agents_page",
		"unknown":      "unknown",
		"other":        "other",
	}
)

func normalizeFromAllowList(value string, allowList map[string]string, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if normalized, ok := allowList[value]; ok {
		return normalized
	}
	return fallback
}

func NormalizePlatform(value string) string {
	return normalizeFromAllowList(value, knownPlatforms, "unknown")
}

func NormalizeOnboardingPath(value string) string {
	return normalizeFromAllowList(value, knownOnboardingPaths, "unknown")
}

func NormalizeAutopilotCadence(value string) string {
	return normalizeFromAllowList(value, knownAutopilotCadences, "unknown")
}

func NormalizeAutopilotTrigger(value string) string {
	return normalizeFromAllowList(value, knownAutopilotTriggers, "unknown")
}

func NormalizeAutopilotSkipReason(value string) string {
	return normalizeFromAllowList(value, knownAutopilotSkipReasons, "other")
}

func NormalizeWebhookProvider(value string) string {
	return normalizeFromAllowList(value, knownWebhookProviders, "other")
}

func NormalizeWebhookDeliveryStatus(value string) string {
	return normalizeFromAllowList(value, knownWebhookDeliveryStatuses, "other")
}

func NormalizeGithubEventKind(value string) string {
	return normalizeFromAllowList(value, knownGithubEventKinds, "other")
}

func NormalizeGithubAction(value string) string {
	if strings.TrimSpace(value) == "" {
		return "none"
	}
	return normalizeFromAllowList(value, knownGithubActions, "other")
}

func NormalizeGithubPRReviewResult(value string) string {
	return normalizeFromAllowList(value, knownGithubPRReviewResults, "other")
}

func NormalizeCloudRuntimeOp(value string) string {
	return normalizeFromAllowList(value, knownCloudRuntimeOps, "other")
}

// NormalizeCloudRuntimeStatus collapses an HTTP status code or symbolic
// outcome string into the fixed bucket set {ok, 4xx, 5xx, timeout, error}.
// Empty / unknown collapses to "error".
func NormalizeCloudRuntimeStatus(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if normalized, ok := knownCloudRuntimeStatuses[value]; ok {
		return normalized
	}
	if len(value) == 3 {
		switch value[0] {
		case '2':
			return "ok"
		case '4':
			return "4xx"
		case '5':
			return "5xx"
		}
	}
	return "error"
}

// CloudRuntimeStatusForCode maps an HTTP status code to its bucket label.
// Used by cloudruntime client instrumentation.
func CloudRuntimeStatusForCode(code int) string {
	switch {
	case code >= 200 && code < 400:
		return "ok"
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500 && code < 600:
		return "5xx"
	default:
		return "error"
	}
}

func NormalizeDaemonWSKind(value string) string {
	return normalizeFromAllowList(value, knownDaemonWSKinds, "other")
}

func NormalizeFeedbackKind(value string) string {
	return normalizeFromAllowList(value, knownFeedbackKinds, "other")
}

func NormalizeContactSalesSource(value string) string {
	return normalizeFromAllowList(value, knownContactSalesSources, "other")
}
