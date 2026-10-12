package computer

import (
	"encoding/json"
	"fmt"
)

// WithPassword authenticates an existing account under the same persistent
// attempt counter used by provisioning. Infrastructure errors are refunded.
func WithPassword(remote Remote, store AttemptStore, computerID, username, password string, work func() error) error {
	return store.WithKey(computerID, username, func(a Attempt) error {
		_, allowed, err := a.Reserve(defaultFailureLimit)
		if err != nil {
			return err
		}
		if !allowed {
			return fmt.Errorf("password_locked")
		}
		ok, err := remote.PasswordMatches(username, password)
		if err != nil {
			_ = a.Refund()
			return ClassifiedError(ErrorCode(err, "account_unavailable"))
		}
		if !ok {
			return fmt.Errorf("password mismatch")
		}
		if err := a.Reset(); err != nil {
			return err
		}
		return work()
	})
}

// ReadSettings reads only known credential locations as the target UID. Never
// attach command output or JSON errors to an error: both may contain secrets.
func (s SSHRemote) ReadSettings(username string) (Settings, error) {
	if err := validateUsername(username); err != nil {
		return Settings{}, err
	}
	if err := validateComputerID(s.DaemonID); err != nil {
		return Settings{}, err
	}
	argv, err := s.argv("sudo -n runuser -u " + ShellQuote(username) + " -- python3 -c " + ShellQuote(readSettingsScript) + " " + ShellQuote("computer-"+s.DaemonID))
	if err != nil {
		return Settings{}, err
	}
	out, err := s.execute(argv, "")
	if err != nil {
		return Settings{}, ClassifiedError(ErrorCode(err, "credential_transfer_failed"))
	}
	var settings Settings
	if len(out) > 65536 || json.Unmarshal([]byte(out), &settings) != nil {
		return Settings{}, ClassifiedError("credential_transfer_failed")
	}
	return settings, nil
}

// ReadDaemonToken reads only the managed profile, not Git or model credentials.
func (s SSHRemote) ReadDaemonToken(username string) (string, error) {
	if err := validateUsername(username); err != nil {
		return "", err
	}
	if err := validateComputerID(s.DaemonID); err != nil {
		return "", err
	}
	argv, err := s.argv("sudo -n runuser -u " + ShellQuote(username) + " -- python3 -c " + ShellQuote(readDaemonTokenScript) + " " + ShellQuote("computer-"+s.DaemonID))
	if err != nil {
		return "", err
	}
	out, err := s.execute(argv, "")
	if err != nil {
		return "", ClassifiedError(ErrorCode(err, "credential_transfer_failed"))
	}
	var token string
	if len(out) > 65536 || json.Unmarshal([]byte(out), &token) != nil {
		return "", ClassifiedError("credential_transfer_failed")
	}
	return token, nil
}

// WriteDaemonConfig updates the managed daemon profile without rewriting credentials.
// The token travels through stdin, never command arguments or error messages.
func (s SSHRemote) WriteDaemonConfig(username, serverURL, token string) error {
	if err := validateUsername(username); err != nil {
		return err
	}
	if err := validateComputerID(s.DaemonID); err != nil {
		return err
	}
	files, err := RenderFiles(serverURL, "", "", "", "", "", token)
	if err != nil {
		return ClassifiedError("credential_transfer_failed")
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(files.MulticaConfig), &config); err != nil {
		return ClassifiedError("credential_transfer_failed")
	}
	config["workspace_id"] = s.WorkspaceID
	config["health_port"] = s.HealthPort
	payload, err := json.Marshal(config)
	if err != nil {
		return ClassifiedError("credential_transfer_failed")
	}
	argv, err := s.argv("sudo -n runuser -u " + ShellQuote(username) + " -- python3 -c " + ShellQuote(writeDaemonConfigScript) + " " + ShellQuote("computer-"+s.DaemonID))
	if err != nil {
		return err
	}
	if _, err := s.execute(argv, string(payload)); err != nil {
		return ClassifiedError(ErrorCode(err, "credential_transfer_failed"))
	}
	return nil
}

// MergeSettings updates supplied fields only; blank inputs preserve the remote
// value. Clearing credentials is deliberately not part of this operation.
func MergeSettings(existing, supplied Settings) Settings {
	pairs := []struct {
		dst *string
		src string
	}{
		{&existing.GitName, supplied.GitName}, {&existing.GitEmail, supplied.GitEmail},
		{&existing.GitLabURL, supplied.GitLabURL}, {&existing.GitLabToken, supplied.GitLabToken},
		{&existing.GitSSHKey, supplied.GitSSHKey}, {&existing.GitKnownHosts, supplied.GitKnownHosts},
		{&existing.ModelEnv, supplied.ModelEnv}, {&existing.MulticaPAT, supplied.MulticaPAT},
	}
	for _, pair := range pairs {
		if pair.src != "" {
			*pair.dst = pair.src
		}
	}
	return existing
}

const readPrivateFileScript = `import os,pwd,sys,json,stat,subprocess,re
home=pwd.getpwuid(os.getuid()).pw_dir
profile=sys.argv[1]
if not profile.startswith("computer-") or any(c not in "abcdefghijklmnopqrstuvwxyz0123456789-" for c in profile): raise RuntimeError("invalid profile")
def read(relative):
    fd=os.open(home,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
    try:
        parts=relative.split("/")
        for part in parts[:-1]:
            nxt=os.open(part,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW,dir_fd=fd)
            os.close(fd); fd=nxt
        f=os.open(parts[-1],os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK,dir_fd=fd)
        with os.fdopen(f,"r") as stream:
            if not stat.S_ISREG(os.fstat(stream.fileno()).st_mode): raise RuntimeError("invalid credential file")
            value=stream.read(32769)
            if len(value)>32768: raise RuntimeError("credential file too large")
            return value
    except FileNotFoundError: return ""
    finally: os.close(fd)
`

const readSettingsScript = readPrivateFileScript + `def git_value(text,key):
    if not text: return ""
    p=subprocess.run(["git","config","--no-includes","--file","-","--get",key],input=text,capture_output=True,text=True,timeout=5)
    if p.returncode not in (0,1): raise RuntimeError("invalid Git configuration")
    return p.stdout.strip()
base=".config/multica-provision/"
global_git=read(".gitconfig")
managed_git=read(base+"gitconfig")
def identity(key):
    return git_value(managed_git,key) or git_value(global_git,key)
helper=git_value(managed_git,"credential.helper")
origin=re.search(r'test "\$proto" = \'([^\']+)\' && test "\$host" = \'([^\']+)\'',helper)
model=[]
for line in read(base+"model.env").splitlines():
    key,sep,value=line.partition("=")
    if key not in ("ANTHROPIC_API_KEY","OPENAI_API_KEY","OPENROUTER_API_KEY","GEMINI_API_KEY","XAI_API_KEY"): continue
    if value.startswith('"'): value=json.loads(value)
    if not isinstance(value,str) or any(ord(c)<32 or ord(c)==127 for c in value): raise RuntimeError("invalid model value")
    model.append(key+"="+value)
raw=read(".multica/profiles/"+profile+"/config.json")
config=json.loads(raw) if raw else {}
if not isinstance(config,dict): raise RuntimeError("invalid Multica config")
result=dict(git_name=identity("user.name"),git_email=identity("user.email"),
    gitlab_url=git_value(managed_git,"multica.gitlabUrl") or ((origin[1]+"://"+origin[2]) if origin else ""),gitlab_token=read(base+"gitlab.token").strip(),
    git_ssh_key=read(base+"git.key"),git_known_hosts=read(base+"known_hosts"),
    model_env="\n".join(model),multica_pat=config.get("token",""))
encoded=json.dumps(result)
if len(encoded.encode())>65536: raise RuntimeError("credentials too large")
print(encoded)
`

const readDaemonTokenScript = readPrivateFileScript + `config=json.loads(read(".multica/profiles/"+profile+"/config.json") or "{}")
if not isinstance(config,dict): raise RuntimeError("invalid existing config")
token=config.get("token", "")
if not isinstance(token,str): raise RuntimeError("invalid token")
print(json.dumps(token))
`
