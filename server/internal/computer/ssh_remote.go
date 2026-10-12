package computer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// CommandRunner executes one remote command. stdin is the only place a
// password or token may travel. argv must stay free of secrets. Implementations
// must stop when ctx is cancelled; SSHRemote supplies the command timeout.
type CommandRunner interface {
	Run(ctx context.Context, argv []string, stdin string) (stdout string, err error)
}

// SSHRemote talks to one computer as the passwordless sudo account.
// It does not dial anything until Run is called.
type SSHRemote struct {
	Host        string
	Port        int
	User        string
	KeyPath     string
	Timeout     time.Duration
	RunCmd      CommandRunner
	BinaryPath  string
	DaemonID    string
	WorkspaceID string
	HealthPort  int
	Context     context.Context
}

func (s SSHRemote) argv(remote string) ([]string, error) {
	if err := validateComputerID(s.Host); err != nil && net.ParseIP(s.Host) == nil {
		return nil, err
	}
	if strings.HasPrefix(s.Host, "-") {
		return nil, fmt.Errorf("invalid host")
	}
	if err := validateUsername(s.User); err != nil {
		return nil, err
	}
	if s.Port <= 0 || s.Port > 65535 {
		return nil, fmt.Errorf("ssh port is invalid")
	}
	if strings.ContainsAny(s.KeyPath, "\r\n") || strings.TrimSpace(s.KeyPath) == "" {
		return nil, fmt.Errorf("ssh key path is required")
	}
	return []string{
		"ssh",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "ConnectTimeout=10",
		"-i", s.KeyPath,
		"-p", strconv.Itoa(s.Port),
		s.User + "@" + s.Host,
		remote,
	}, nil
}

func (s SSHRemote) run(remote, stdin string, secrets ...string) (string, error) {
	argv, err := s.argv(remote)
	if err != nil {
		return "", err
	}
	out, err := s.execute(argv, stdin)
	out = redactText(out, secrets...)
	if err != nil {
		detail := strings.TrimSpace(out)
		if len(detail) > 4096 {
			detail = "…" + detail[len(detail)-4096:]
		}
		err = fmt.Errorf("remote operation failed: %w", err)
		if detail != "" {
			err = fmt.Errorf("%w: %s", err, detail)
		}
		out = ""
	}
	return out, err
}

// RunCommand executes cmd on the remote machine as the configured SSH user and
// returns the trimmed output. The command string is passed verbatim as the SSH
// remote argument — the caller must ensure safe quoting and validation of any
// interpolated values.
func (s SSHRemote) RunCommand(cmd string) (string, error) {
	out, err := s.run(cmd, "")
	return strings.TrimSpace(out), err
}

// RunCommandContext bounds remote admin commands by both the request lifetime
// and the configured SSH timeout.
func (s SSHRemote) RunCommandContext(ctx context.Context, cmd string) (string, error) {
	s.Context = ctx
	return s.RunCommand(cmd)
}

func (s SSHRemote) UserExists(username string) (bool, error) {
	if err := validateUsername(username); err != nil {
		return false, err
	}
	out, err := s.run("python3 -c "+ShellQuote("import pwd,sys\ntry: pwd.getpwnam(sys.argv[1]); print('yes')\nexcept KeyError: print('no')")+" "+username, "")
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(out) {
	case "yes":
		return true, nil
	case "no":
		return false, nil
	}
	return false, fmt.Errorf("invalid account lookup response")
}

func (s SSHRemote) PasswordMatches(username, password string) (bool, error) {
	if err := validateUsername(username); err != nil {
		return false, err
	}
	if err := rejectControls("password", password); err != nil {
		return false, err
	}
	if username == s.User {
		return false, fmt.Errorf("operator account cannot be provisioned")
	}
	// PAM runs with a fixed service and checks both authentication and account expiry.
	_, err := s.run("sudo -n python3 -c "+ShellQuote(pamScript)+" "+username, password, password)
	if err == nil {
		return true, nil
	}
	var exited *exec.ExitError
	if errors.As(err, &exited) && exited.ExitCode() == 42 {
		return false, nil
	}
	return false, err
}

// ShellQuote encodes one literal argument for a POSIX shell.
func ShellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func (s SSHRemote) CreateUser(username, password string) error {
	if err := validateUsername(username); err != nil {
		return err
	}
	if err := rejectControls("password", password); err != nil {
		return err
	}
	if username == s.User {
		return fmt.Errorf("operator account cannot be provisioned")
	}
	// Leave time for package installation, account setup, rollback,
	// and connection establishment. Other operations retain their normal limit.
	if s.Timeout < 8*time.Minute {
		s.Timeout = 8 * time.Minute
	}
	script := initializeZshScript + `import os,shutil,signal,subprocess,sys
# SSH hangups and shutdowns should unwind through the account rollback path.
def interrupted(signum, frame):
    raise RuntimeError("provisioning interrupted")
signal.signal(signal.SIGHUP, interrupted)
signal.signal(signal.SIGTERM, interrupted)
def run_installer(argv, **kwargs):
    p=subprocess.Popen(argv,start_new_session=True, **kwargs)
    try:
        if p.wait(timeout=120):
            raise RuntimeError("package_manager_failed" if argv[0]=="apt-get" else "installer_failed")
    except BaseException:
        try: os.killpg(p.pid,signal.SIGTERM)
        except ProcessLookupError: pass
        try: p.wait(timeout=10)
        except subprocess.TimeoutExpired: pass
        finally:
            # Kill descendants even if the group leader already exited.
            try: os.killpg(p.pid,signal.SIGKILL)
            except ProcessLookupError: pass
        p.wait(timeout=5)
        raise
u=sys.argv[1]; pw=sys.stdin.read()
# This provisioning path uses Debian/Ubuntu's package names. The connection
# probe checks apt-get before an account is created, so a failed package
# prerequisite cannot leave an account with a missing login shell.
if shutil.which("apt-get") is None:
    raise RuntimeError("apt-get is required to provision a new account")
run_installer(["apt-get","install","-y","--no-install-recommends","zsh","htop","curl","git"],
    env=dict(os.environ,DEBIAN_FRONTEND="noninteractive"))
subprocess.run(["useradd","--create-home","--shell","/usr/bin/zsh",u],check=True,timeout=30)
try:
    subprocess.run(["chpasswd"],input=u+":"+pw+"\n",text=True,check=True,timeout=15)
    initialize_zsh(u)
except BaseException:
    subprocess.run(["userdel","-r",u],check=True,timeout=30)
    raise
`
	_, err := s.run("sudo -n python3 -c "+ShellQuote(script)+" "+username, password, password)
	return err
}

func (s SSHRemote) DeleteUser(username string) error {
	if err := validateUsername(username); err != nil {
		return err
	}
	if err := s.StopDaemon(username); err != nil {
		return err
	}
	_, err := s.run("sudo -n userdel -r "+username, "")
	return err
}

func (s SSHRemote) WriteFiles(username string, files CredentialFiles) error {
	if err := validateUsername(username); err != nil {
		return err
	}
	for _, field := range []struct{ label, v string }{
		{"gitconfig", files.Gitconfig},
		{"ssh key", files.GitSSHKey}, {"known hosts", files.GitKnownHosts},
		{"gitlab token", files.GitLabToken},
		{"model env", files.ModelEnv},
		{"multica config", files.MulticaConfig},
	} {
		if strings.ContainsRune(field.v, 0) {
			return fmt.Errorf("%s contains NUL", field.label)
		}
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(files.MulticaConfig), &cfg); err != nil {
		return err
	}
	if s.WorkspaceID != "" {
		cfg["workspace_id"] = s.WorkspaceID
		cfg["health_port"] = s.HealthPort
	}
	data, _ := json.Marshal(cfg)
	files.MulticaConfig = string(data)
	payload := files.Gitconfig + "\x00" + files.GitLabToken + "\x00" + files.ModelEnv + "\x00" + files.MulticaConfig + "\x00" + files.GitSSHKey + "\x00" + files.GitKnownHosts
	script := "sudo -n runuser -u " + username + " -- python3 -c " + ShellQuote(writeFilesScript) + " " + ShellQuote("computer-"+s.DaemonID)

	_, err := s.run(script, payload, files.GitLabToken, files.MulticaConfig, files.ModelEnv, files.GitSSHKey)
	return err
}

func (s SSHRemote) InstallDaemon(username string) error {
	if err := validateUsername(username); err != nil {
		return err
	}
	if s.DaemonID != "" {
		if err := validateComputerID(s.DaemonID); err != nil {
			return err
		}
		// This per-user unit also supports accounts whose home/group differs from their name.
		unit := strings.ReplaceAll(DaemonUnit, "%i", username)
		unit = strings.ReplaceAll(unit, "/usr/local/bin/multica", "/opt/multica-computers/"+s.DaemonID+"/multica")
		unit = strings.ReplaceAll(unit, " daemon start --foreground", " --profile computer-"+s.DaemonID+" daemon start --foreground --no-auto-update --daemon-id "+s.DaemonID)
		script := "import sys,pwd,os\nu=sys.argv[1]; e=pwd.getpwnam(u)\nif not e.pw_dir.startswith('/') or any(c in e.pw_dir for c in '%\\n\\r\\t\"'): raise RuntimeError('unsupported home path')\nb=sys.stdin.read().replace('Group='+u+'\\n','').replace('/home/'+u,e.pw_dir)\np='/etc/systemd/system/multica-daemon@'+u+'.service'\nf=os.open(p,os.O_WRONLY|os.O_CREAT|os.O_TRUNC|os.O_NOFOLLOW,0o644)\nos.write(f,b.encode()); os.close(f)"
		if _, err := s.run("sudo -n python3 -c "+ShellQuote(script)+" "+username, unit); err != nil {
			return err
		}
		for _, step := range []string{"systemctl daemon-reload", "systemctl enable multica-daemon@" + username + ".service", "systemctl restart multica-daemon@" + username + ".service"} {
			if _, err := s.run("sudo -n "+step, ""); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("daemon identity is required")
}

// execute applies the same lifetime and timeout to real and injected runners.
func (s SSHRemote) execute(argv []string, stdin string) (string, error) {
	parent := s.Context
	if parent == nil {
		parent = context.Background()
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	runner := s.RunCmd
	if runner == nil {
		runner = execRunner{}
	}
	out, err := runner.Run(ctx, argv, stdin)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return out, err
}

type execRunner struct{}

func (e execRunner) Run(ctx context.Context, argv []string, stdin string) (string, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.WaitDelay = time.Second
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return out.String(), err
}

func (s SSHRemote) StopDaemon(username string) error {
	if err := validateUsername(username); err != nil {
		return err
	}
	script := `import subprocess,sys
unit="multica-daemon@"+sys.argv[1]+".service"
p=subprocess.run(["systemctl","show","--property=LoadState","--value",unit],capture_output=True,text=True,check=True,timeout=10)
if p.stdout.strip()!="not-found": subprocess.run(["systemctl","disable","--now",unit],check=True,timeout=30)
`
	_, err := s.run("sudo -n python3 -c "+ShellQuote(script)+" "+username, "")
	return err
}

// Prepare installs a deployment-supplied, pinned CLI artifact; no remote curl
// scripts or client-supplied executable paths are accepted.
func (s SSHRemote) Prepare() error {
	if err := validateComputerID(s.DaemonID); err != nil {
		return err
	}
	b, err := os.ReadFile(s.BinaryPath)
	if err != nil {
		return fmt.Errorf("configured CLI artifact unavailable")
	}
	if len(b) == 0 || len(b) > 150*1024*1024 {
		return fmt.Errorf("invalid CLI artifact")
	}
	script := `import os,sys,base64,tempfile
base="/opt/multica-computers/"+sys.argv[1]
os.makedirs(base,mode=0o755,exist_ok=True)
body=base64.b64decode(sys.stdin.buffer.read(),validate=True)
fd,path=tempfile.mkstemp(prefix=".multica-",dir=base)
try:
    with os.fdopen(fd,"wb") as f: f.write(body); f.flush(); os.fsync(f.fileno()); os.fchmod(f.fileno(),0o755)
    os.replace(path,base+"/multica")
finally:
    if os.path.exists(path): os.unlink(path)

`
	_, err = s.run("sudo -n python3 -c "+ShellQuote(script)+" "+s.DaemonID, base64.StdEncoding.EncodeToString(b))
	return err
}

// RemoveDaemon removes only this managed service and executable. Home is kept.
func (s SSHRemote) RemoveDaemon(username string) error {
	if err := validateUsername(username); err != nil {
		return err
	}
	if err := validateComputerID(s.DaemonID); err != nil {
		return err
	}
	if err := s.StopDaemon(username); err != nil {
		return err
	}
	script := `import os,sys,subprocess
unit="/etc/systemd/system/multica-daemon@"+sys.argv[1]+".service"
binary="/opt/multica-computers/"+sys.argv[2]+"/multica"
for path in (unit,binary):
    try: os.unlink(path)
    except FileNotFoundError: pass
subprocess.run(["systemctl","daemon-reload"],check=True,timeout=15)
`
	_, err := s.run("sudo -n python3 -c "+ShellQuote(script)+" "+username+" "+s.DaemonID, "")
	return err
}
