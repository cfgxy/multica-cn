package computer

import (
	"fmt"
	"golang.org/x/crypto/ssh"
	"net"
	"net/url"
	"strings"
)

// Settings belongs to one authenticated human. Linux passwords are supplied
// only for an operation, never persisted in this credential bundle.
type Settings struct {
	GitName       string `json:"git_name"`
	GitEmail      string `json:"git_email"`
	GitLabURL     string `json:"gitlab_url"`
	GitSSHKey     string `json:"git_ssh_key"`
	GitKnownHosts string `json:"git_known_hosts"`
	GitLabToken   string `json:"gitlab_token"`
	ModelEnv      string `json:"model_env"`
	MulticaPAT    string `json:"multica_pat"`
}

func (s Settings) Validate() error {
	if s.GitSSHKey != "" {
		if _, err := ssh.ParsePrivateKey([]byte(s.GitSSHKey)); err != nil {
			return fmt.Errorf("a valid unencrypted SSH private key is required")
		}
		if strings.TrimSpace(s.GitKnownHosts) == "" || strings.ContainsRune(s.GitKnownHosts, 0) {
			return fmt.Errorf("SSH known hosts are required")
		}
	}
	if err := validateGit(s.GitName, s.GitEmail); err != nil {
		return err
	}
	if _, err := ValidateModelEnv(s.ModelEnv); err != nil {
		return err
	}
	for _, v := range []string{s.GitLabToken, s.MulticaPAT} {
		if err := rejectControls("credential", v); err != nil {
			return err
		}
	}
	if s.MulticaPAT != "" && !strings.HasPrefix(s.MulticaPAT, "mul_") {
		return fmt.Errorf("a personal Multica token is required")
	}
	u, err := url.Parse(s.GitLabURL)
	if (s.GitLabURL != "" || s.GitLabToken != "") && (err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https")) {
		return fmt.Errorf("a GitLab HTTP(S) origin is required")
	}
	return nil
}

type Machine struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	SSHUser string `json:"ssh_user"`
}

func (m Machine) Validate() error {
	if strings.TrimSpace(m.Name) == "" || len(m.Name) > 100 {
		return fmt.Errorf("Computer name is required")
	}
	if net.ParseIP(m.Host) == nil {
		if err := validateComputerID(m.Host); err != nil {
			return fmt.Errorf("invalid host")
		}
		if strings.HasPrefix(m.Host, "-") {
			return fmt.Errorf("invalid host")
		}
	}
	if m.Port < 1 || m.Port > 65535 {
		return fmt.Errorf("invalid port")
	}
	return validateUsername(m.SSHUser)
}
