package computer

import (
	_ "embed"
	"encoding/json"

	"github.com/multica-ai/multica/server/internal/sub2api"
)

//go:embed model_connection_test.py
var modelConnectionScript string

type ModelConnectionTest struct {
	OperationID  string `json:"operation_id"`
	Status       string `json:"status"`
	ErrorCode    string `json:"error_code"`
	DurationMS   int64  `json:"duration_ms"`
	FileRevision string `json:"file_revision"`
	Model        string `json:"model"`
}

func (s SSHRemote) TestModelConnection(username, kind string, snapshot ModelConfigSnapshot, model, selector string) (ModelConnectionTest, error) {
	var out ModelConnectionTest
	if validateUsername(username) != nil || !sub2api.KnownRuntime(kind) || model == "" || selector == "" {
		return out, ClassifiedError("model_config_invalid")
	}
	argv, err := s.argv("sudo -n runuser -u " + ShellQuote(username) + " -- python3 -c " + ShellQuote(modelConnectionScript))
	if err != nil {
		return out, err
	}
	body, _ := json.Marshal(map[string]string{"kind": kind, "model": model, "model_selector": selector})
	raw, err := s.execute(argv, string(body))
	if err != nil {
		return out, ClassifiedError(ErrorCode(err, "model_connection_failed"))
	}
	if len(raw) > 4096 || json.Unmarshal([]byte(raw), &out) != nil {
		return out, ClassifiedError("model_connection_failed")
	}
	if out.Status != "succeeded" && out.Status != "failed" {
		return out, ClassifiedError("model_connection_failed")
	}
	if out.Status == "failed" && !sub2api.Contains([]string{"model_connection_failed", "model_environment_conflict", "cli_missing", "model_test_unsupported", "model_test_timeout", "model_test_output_limit", "model_cli_protocol_failed", "model_auth_failed", "model_rate_limited"}, out.ErrorCode) {
		out.ErrorCode = "model_connection_failed"
	}
	out.FileRevision = snapshot.Revision
	out.Model = model
	return out, nil
}
