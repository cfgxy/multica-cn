package outerloop

import "testing"

// TestIsPipelineClass covers the full 27-reason taxonomy plus the direction
// guarantees for unknown reasons: agent-side escalates, platform-side
// self-heals (RUYI-660 M2).
func TestIsPipelineClass(t *testing.T) {
	pipeline := []string{
		// platform / scheduler side
		"queued_expired",
		"runtime_offline",
		"runtime_reconnect_timeout",
		"runtime_recovery",
		"timeout",
		"iteration_limit",
		"api_invalid_request",
		"skill_bundle_unavailable",
		"environment_prepare_failed",
		"invalid_task_identity",
		"delivery_guard",
		// agent-side transient trio
		"agent_error.provider_capacity_or_rate_limit",
		"agent_error.provider_server_error",
		"agent_error.provider_network",
	}
	human := []string{
		// platform-side carve-outs
		"agent_blocked",
		"runtime_cli_timeout",
		// agent-side, human-required
		"agent_error.provider_auth_or_access",
		"agent_error.provider_quota_limit",
		"agent_error.process_failure",
		"agent_error.empty_or_unparseable_output",
		"agent_error.agent_timeout",
		"agent_error.context_overflow",
		"agent_error.missing_config",
		"agent_error.model_not_found_or_unavailable",
		"agent_error.runtime_version_unsupported",
		"agent_error.runtime_missing_executable",
		"agent_error.unknown",
	}
	for _, r := range pipeline {
		if !IsPipelineClass(r) {
			t.Errorf("IsPipelineClass(%q) = false, want true", r)
		}
	}
	for _, r := range human {
		if IsPipelineClass(r) {
			t.Errorf("IsPipelineClass(%q) = true, want false", r)
		}
	}
	// Direction guarantees: empty and unknown agent-side escalate; unknown
	// platform-side self-heals.
	if IsPipelineClass("") {
		t.Error("empty reason must escalate (fail toward human)")
	}
	if !IsPipelineClass("some_future_platform_reason") {
		t.Error("future platform-side reason must self-heal")
	}
	if IsPipelineClass("agent_error.some_future_subreason") {
		t.Error("future agent-side reason must escalate (fail toward human)")
	}
}
