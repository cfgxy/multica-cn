package computer

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed runtime_config_files.py
var runtimeConfigFilesScript string

type ModelConfigSnapshot struct {
	Files     map[string]*string `json:"files"`
	Revision  string             `json:"revision"`
	ErrorCode string             `json:"error_code,omitempty"`
}

func (s ModelConfigSnapshot) Exists() bool {
	for _, v := range s.Files {
		if v != nil {
			return true
		}
	}
	return false
}
func (s ModelConfigSnapshot) Contents() map[string]string {
	out := map[string]string{}
	for k, v := range s.Files {
		if v != nil {
			out[k] = *v
		}
	}
	return out
}

type modelFileRequest struct {
	Action           string            `json:"action"`
	Paths            []string          `json:"paths"`
	Files            map[string]string `json:"files,omitempty"`
	ExpectedRevision string            `json:"expected_revision,omitempty"`
	CreateOnly       bool              `json:"create_only"`
}

func (s SSHRemote) ReadModelConfig(username string, paths []string) (ModelConfigSnapshot, error) {
	return s.modelConfigFiles(username, modelFileRequest{Action: "read", Paths: paths})
}
func (s SSHRemote) SaveModelConfig(username string, paths []string, files map[string]string, revision string, createOnly bool) (ModelConfigSnapshot, error) {
	return s.modelConfigFiles(username, modelFileRequest{Action: "save", Paths: paths, Files: files, ExpectedRevision: revision, CreateOnly: createOnly})
}
func (s SSHRemote) modelConfigFiles(username string, in modelFileRequest) (ModelConfigSnapshot, error) {
	var out ModelConfigSnapshot
	if err := validateUsername(username); err != nil {
		return out, err
	}
	if len(in.Paths) == 0 || len(in.Paths) > 16 {
		return out, ClassifiedError("model_config_invalid")
	}
	for _, p := range in.Paths {
		if validateRuntimeConfigPath(p) != nil {
			return out, ClassifiedError("model_config_invalid")
		}
	}
	body, err := json.Marshal(in)
	if err != nil || len(body) > 4<<20 {
		return out, ClassifiedError("model_config_invalid")
	}
	argv, err := s.argv("sudo -n runuser -u " + ShellQuote(username) + " -- python3 -c " + ShellQuote(runtimeConfigFilesScript))
	if err != nil {
		return out, err
	}
	// Never attach stdout/stderr to an error: it may contain entire private files.
	raw, err := s.execute(argv, string(body))
	if err != nil {
		return out, ClassifiedError(ErrorCode(err, "credential_transfer_failed"))
	}
	if len(raw) > 8<<20 || json.Unmarshal([]byte(raw), &out) != nil {
		return ModelConfigSnapshot{}, ClassifiedError("credential_transfer_failed")
	}
	switch out.ErrorCode {
	case "model_config_conflict", "model_config_exists", "model_config_invalid", "credential_transfer_failed":
		return ModelConfigSnapshot{}, ClassifiedError(out.ErrorCode)
	case "":
		if len(out.Revision) != 64 || len(out.Files) != len(in.Paths) {
			return ModelConfigSnapshot{}, ClassifiedError("credential_transfer_failed")
		}
	default:
		return ModelConfigSnapshot{}, ClassifiedError("credential_transfer_failed")
	}
	return out, nil
}
func validateRuntimeConfigPath(p string) error {
	if p == "" || len(p) > 512 || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00\r\n\t") {
		return ClassifiedError("model_config_invalid")
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return ClassifiedError("model_config_invalid")
		}
	}
	return nil
}
