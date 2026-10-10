package computer

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// CredentialFiles is what the bastion writes into the Linux user's home.
// Mode 0600, temp file then rename. Secrets stay out of the unit file and
// out of process arguments.
type CredentialFiles struct {
	GitSSHKey     string
	GitKnownHosts string
	Gitconfig     string
	GitLabToken   string
	ModelEnv      string
	MulticaConfig string
}

// RenderFiles builds the on-disk payloads.
// GitLab token is NOT embedded in gitconfig; a helper reads the token file.
// Multica PAT is written into the managed CLI profile config.json, which the
// systemd unit explicitly selects. A side file the daemon never reads is not enough.
func RenderFiles(serverURL, gitName, gitEmail, gitlabURL, gitlabToken, modelEnv, pat string) (CredentialFiles, error) {
	if err := validateGit(gitName, gitEmail); err != nil {
		return CredentialFiles{}, err
	}
	env, err := ValidateModelEnv(modelEnv)
	if err != nil {
		return CredentialFiles{}, err
	}
	if strings.TrimSpace(serverURL) == "" || strings.ContainsAny(serverURL, " ") {
		return CredentialFiles{}, fmt.Errorf("server url is required")
	}
	if err := rejectControls("server url", serverURL); err != nil {
		return CredentialFiles{}, err
	}
	if strings.ContainsAny(pat, " ") {
		return CredentialFiles{}, fmt.Errorf("multica pat is required")
	}
	if err := rejectControls("multica pat", pat); err != nil {
		return CredentialFiles{}, err
	}
	token := strings.TrimSpace(gitlabToken)
	if err := rejectControls("gitlab token", token); err != nil {
		return CredentialFiles{}, err
	}
	if token != "" {
		if err := rejectControls("gitlab url", gitlabURL); err != nil {
			return CredentialFiles{}, err
		}
	}
	config := map[string]string{"server_url": serverURL}
	if pat != "" {
		config["token"] = pat
	}
	cfg, err := json.Marshal(config)
	if err != nil {
		return CredentialFiles{}, err
	}
	git := ""
	if gitName != "" || gitEmail != "" {
		git = "[user]\n"
	}
	if gitName != "" {
		git += "\tname = " + strconv.Quote(gitName) + "\n"
	}
	if gitEmail != "" {
		git += "\temail = " + strconv.Quote(gitEmail) + "\n"
	}
	if gitlabURL != "" {
		git += "[multica]\n\tgitlabUrl = " + strconv.Quote(gitlabURL) + "\n"
	}
	if token != "" {
		helper, err := gitlabCredentialHelper(gitlabURL)
		if err != nil {
			return CredentialFiles{}, err
		}
		git += "[credential]\n\thelper = " + helper + "\n"
	}
	return CredentialFiles{
		Gitconfig:     git,
		GitLabToken:   token,
		ModelEnv:      env,
		MulticaConfig: string(cfg) + "\n",
	}, nil
}

// gitlabCredentialHelper returns a quoted gitconfig helper that answers only
// the configured GitLab origin. Other hosts get no password.
func gitlabCredentialHelper(gitlabURL string) (string, error) {
	u, err := url.Parse(gitlabURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("gitlab url is required")
	}
	if strings.ContainsAny(u.Scheme, "'\\\"\n ") || strings.ContainsAny(u.Host, "'\\\"\n ") {
		return "", fmt.Errorf("gitlab url has unsupported characters")
	}
	shell := `!f() { test "$1" = get || exit 0; proto=; host=; while IFS= read -r line; do case "$line" in protocol=*) proto=${line#protocol=};; host=*) host=${line#host=};; esac; done; test "$proto" = '` + u.Scheme + `' && test "$host" = '` + u.Host + `' || exit 0; echo username=oauth2; echo "password=$(tr -d '\n' < "$HOME/.config/multica-provision/gitlab.token")"; }; f`
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(shell) + `"`, nil
}

// DaemonUnit is a systemd template. %i is the Linux username.
// The token lives in config.json on disk, not in Environment=.
const DaemonUnit = `[Unit]
Description=Multica daemon for %i
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
UMask=0077
User=%i
Group=%i
WorkingDirectory=/home/%i
Environment=HOME=/home/%i
Environment=XDG_CONFIG_HOME=/home/%i/.config
Environment=PATH=/home/%i/.local/bin:/home/%i/.kimi-code/bin:/home/%i/.grok/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
EnvironmentFile=-/home/%i/.config/multica-provision/model.env
ExecStart=/usr/local/bin/multica daemon start --foreground
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`
