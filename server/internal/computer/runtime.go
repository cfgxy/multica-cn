package computer

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"regexp"
	"strings"
)

const RuntimePath = "$HOME/.local/bin:$HOME/.kimi-code/bin:$HOME/.grok/bin:/usr/local/bin:/usr/bin:/bin"

var runtimeCommandRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.+-]*$`)

type RuntimeProbe struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	State   string `json:"state"`
	Code    string `json:"code"`
}

// ProbeRuntime distinguishes a missing executable from a failed version command.
// Only parsed metadata is returned; stderr may contain credentials and is discarded.
func (s SSHRemote) ProbeRuntime(ctx context.Context, username, command string) (RuntimeProbe, error) {
	if err := validateUsername(username); err != nil {
		return RuntimeProbe{}, err
	}
	if !runtimeCommandRE.MatchString(command) {
		return RuntimeProbe{}, errors.New("invalid runtime command name")
	}
	script := `import json,shutil,subprocess,sys
p=shutil.which(sys.argv[1])
r={"path":p or "","version":"","state":"missing","code":"cli_missing"}
if p:
    r.update(state="version_failed",code="version_check_failed")
    try:
        c=subprocess.run([p,"--version"],capture_output=True,text=True,timeout=15)
        if c.returncode==0 and c.stdout.strip():
            r.update(state="installed",code="",version=c.stdout.strip()[:512])
    except (OSError,subprocess.TimeoutExpired): pass
print(json.dumps(r))`
	cmd := "sudo -n runuser -u " + ShellQuote(username) + " -- sh -c " + ShellQuote("export PATH=\""+RuntimePath+"\"; exec python3 -c "+ShellQuote(script)+" "+ShellQuote(command))
	out, err := s.RunCommandContext(ctx, cmd)
	if err != nil {
		return RuntimeProbe{}, err
	}
	var p RuntimeProbe
	err = json.Unmarshal([]byte(out), &p)
	if err == nil && p.State != "missing" && p.State != "version_failed" && p.State != "installed" {
		err = errors.New("invalid probe response")
	}
	return p, err
}

// ClassifiedError carries only a fixed public code, never command output.
type ClassifiedError string

func (e ClassifiedError) Error() string { return ErrorSummary(string(e)) }

// ErrorCode maps transport and known prerequisite failures to safe public codes.
// Callers store a fixed summary rather than arbitrary SSH output.
func ErrorCode(err error, fallback string) string {
	if err == nil {
		return ""
	}
	var classified ClassifiedError
	if errors.As(err, &classified) {
		return string(classified)
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "ssh_timeout"
	}
	var exit *exec.ExitError
	if fallback == "installer_failed" && errors.As(err, &exit) && (exit.ExitCode() == 124 || exit.ExitCode() == 137) {
		return "installer_timeout"
	}
	if errors.As(err, &exit) && exit.ExitCode() == 255 {
		return "ssh_unreachable"
	}
	s := strings.ToLower(err.Error())
	if fallback == "installer_failed" && (strings.Contains(s, "npm error code e502") || strings.Contains(s, "npm err! code e502") || strings.Contains(s, "curl: (22) the requested url returned error: 502")) {
		return "runtime_download_failed"
	}
	for _, pair := range [][2]string{
		{"context deadline exceeded", "ssh_timeout"}, {"context canceled", "cancelled"}, {"exit status 255", "ssh_unreachable"}, {"exit status 43", "account_unavailable"}, {"package_manager_failed", "package_manager_failed"}, {"sudo:", "sudo_failed"}, {"unknown user", "account_missing"}, {"does not exist", "account_missing"},
		{"authentication failure", "account_unavailable"}, {"password mismatch", "password_mismatch"},
		{"locked", "password_locked"}, {"node.js and npm", "node_npm_missing"}, {"bun: not found", "bun_missing"},
		{"apt-get", "package_manager_failed"}, {"dpkg", "package_manager_failed"},
		{"timed out", "ssh_timeout"}, {"connection refused", "ssh_unreachable"}, {"host key verification failed", "ssh_unreachable"},
	} {
		if strings.Contains(s, pair[0]) {
			return pair[1]
		}
	}
	return fallback
}

func ErrorSummary(code string) string {
	switch code {
	case "model_config_conflict":
		return "Configuration changed on disk; reload before saving or testing."
	case "model_config_exists":
		return "Configuration already exists; reload it before editing."
	case "model_config_invalid":
		return "Check the configuration syntax and file paths."
	case "model_config_incomplete":
		return "Complete all configuration files before saving or testing."
	case "model_template_invalid":
		return "Ask an administrator to repair this runtime template."
	case "model_config_mapping_missing":
		return "Managed fields are missing; edit the existing configuration manually."
	case "model_selection_required":
		return "Choose a model and protocol supported by this API key and runtime."
	case "source_unauthorized":
		return "The directory credential is invalid or expired; ask an administrator to replace it."
	case "source_not_configured", "source_encryption_unavailable":
		return "Ask an administrator to configure the model key source and its encryption key."
	case "source_invalid_response":
		return "The key source response does not match the required runtime and model contract."
	case "source_changed":
		return "The key source changed; refresh the key list before trying again."
	case "model_key_unassigned", "model_key_runtime_forbidden", "model_key_metadata_missing":
		return "This key is not currently assigned to you for this runtime; refresh the key list."
	case "source_unavailable":
		return "The model key source is unavailable; ask an administrator to check its connection."
	case "model_key_changed":
		return "The key or connection settings changed after preview; load the models and preview again before saving."
	case "model_key_unavailable":
		return "Ask an administrator to assign an enabled API key for this runtime and verify the key source."
	case "model_environment_conflict":
		return "The Linux user's model environment overrides native files; remove conflicting model variables in Credentials and sync the daemon before testing."
	case "model_config_metadata_failed":
		return "Files may have been saved; reload before retrying because configuration metadata could not be stored."
	case "model_connection_failed":
		return "The CLI did not return a valid model reply; check the saved endpoint, model and key."
	case "model_discovery_failed":
		return "Model discovery failed; check the model service connection and retry."
	case "model_discovery_invalid":
		return "The model service returned an invalid model list."
	case "model_discovery_empty":
		return "No models were returned for this API key."
	case "model_auth_failed":
		return "The model service rejected authentication; check the API key and endpoint."
	case "model_rate_limited":
		return "The model service rate limited the request; retry later."
	case "model_test_timeout":
		return "The CLI connection test timed out after 60 seconds."
	case "model_test_output_limit":
		return "The CLI produced too much output; check runtime startup configuration."
	case "model_cli_protocol_failed":
		return "The CLI test protocol failed; check the installed runtime version."
	case "model_test_unsupported":
		return "This runtime does not support a managed connection test."
	case "account_missing":
		return "Linux account does not exist; provision it again."
	case "account_unavailable":
		return "Linux account is unavailable; ask an administrator to check expiry and login policy."
	case "password_mismatch":
		return "Password did not match; enter the Linux password again."
	case "password_locked":
		return "Too many password attempts; wait 15 minutes before retrying."
	case "ssh_unreachable", "ssh_timeout":
		return "Check the Computer connection with an administrator, then retry."
	case "sudo_failed":
		return "An administrator must restore passwordless sudo for the SSH operator."
	case "package_manager_failed":
		return "An administrator must repair the package manager before retrying provisioning."
	case "node_npm_missing":
		return "Install Node.js and npm in /usr/local/bin or /usr/bin, then retry."
	case "bun_missing":
		return "Install the supported standalone runtime or make Bun available in the managed PATH."
	case "cli_missing":
		return "CLI executable is missing; install the runtime."
	case "version_check_failed":
		return "CLI exists but its version command failed; repair its dependencies or reinstall."
	case "credentials_required":
		return "Write your own valid Multica PAT to this Linux user before starting the daemon."
	case "runtime_cache_unavailable":
		return "An administrator must configure the runtime cache server URL before installation."
	case "runtime_download_failed":
		return "Multica Server could not download the runtime; copy the error information and ask an administrator to check the server download logs, then retry."
	case "installer_timeout":
		return "Installation exceeded its time limit; check the Computer's access to the package source, then retry."
	case "credential_transfer_failed":
		return "Remote credentials are invalid; check the managed configuration files before retrying."
	case "daemon_offline":
		return "Daemon has not registered; check connectivity, then sync or upgrade."
	case "interrupted":
		return "Operation ended without a confirmed result; check the account and installed assets before retrying."
	case "cancelled":
		return "Operation was cancelled before execution; submit it again to retry."
	case "account_delete_failed":
		return "Account deletion failed; check running processes and permissions before retrying."
	default:
		return "Remote step failed; check prerequisites and retry the displayed operation."
	}
}
