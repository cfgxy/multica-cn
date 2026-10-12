package computer

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ProbeCheck is one pass/fail requirement for provisioning on a computer.
type ProbeCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// ProbeFacts is read-only inventory shown to instance admins.
type ProbeFacts struct {
	Hostname string `json:"hostname,omitempty"`
	OS       string `json:"os,omitempty"`
	Kernel   string `json:"kernel,omitempty"`
	CPUs     int    `json:"cpus,omitempty"`
	MemoryMB int    `json:"memory_mb,omitempty"`
}

// ProbeResult reports whether a computer can host managed accounts.
type ProbeResult struct {
	OK     bool         `json:"ok"`
	Facts  ProbeFacts   `json:"facts"`
	Checks []ProbeCheck `json:"checks"`
}

// probeScript only reads state: it creates no account and writes no file.
const probeScript = `import json,os,shutil,subprocess
PAM=r"""import os,stat,sys
p="/etc/pam.d/multica-provision"
try: fd=os.open(p,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK)
except OSError: print("missing"); sys.exit(0)
with os.fdopen(fd,"rb") as f:
    st=os.fstat(f.fileno())
    if not stat.S_ISREG(st.st_mode) or st.st_uid!=0 or st.st_mode & 0o022: print("unsafe"); sys.exit(0)
    print("ok" if f.read(4096)==b"auth required pam_unix.so\naccount required pam_unix.so\n" else "content")
"""
import sys
def run(argv):
    try:
        p=subprocess.run(argv,capture_output=True,text=True,timeout=20,env=dict(os.environ,LC_ALL="C",LANG="C"))
        return p.returncode,p.stdout.strip(),p.stderr.strip()
    except BaseException as e:
        return 127,"",type(e).__name__
checks=[]
def add(name,ok,detail=""):
    checks.append({"name":name,"ok":bool(ok),"detail":detail[:200]})
add("ssh",True)
add("python3",True,".".join(str(n) for n in sys.version_info[:3]))
rc,out,err=run(["sudo","-n","true"])
add("sudo",rc==0,"" if rc==0 else (err or "passwordless sudo unavailable"))
if rc==0:
    prc,pout,perr=run(["sudo","-n","python3","-c",PAM])
    add("pam",prc==0 and pout=="ok","" if pout=="ok" else {"missing":"/etc/pam.d/multica-provision is absent","unsafe":"/etc/pam.d/multica-provision is not a root-owned regular file","content":"/etc/pam.d/multica-provision content is unexpected"}.get(pout,perr or "pam check failed"))
else:
    add("pam",False,"skipped: sudo unavailable")
for tool in ("systemctl","useradd","userdel","runuser","chpasswd","apt-get"):
    add(tool,shutil.which(tool) is not None,"" if shutil.which(tool) else "not found in PATH")
u=os.uname()
facts={"hostname":u.nodename,"kernel":u.release,"cpus":os.cpu_count() or 0}
try:
    with open("/etc/os-release") as f:
        for line in f:
            if line.startswith("PRETTY_NAME="): facts["os"]=line.split("=",1)[1].strip().strip('"')
except OSError: pass
try:
    with open("/proc/meminfo") as f:
        for line in f:
            if line.startswith("MemTotal:"): facts["memory_mb"]=int(line.split()[1])//1024
except OSError: pass
print(json.dumps({"ok":all(c["ok"] for c in checks),"facts":facts,"checks":checks}))
`

// runDiagnostic keeps command output on failure so an admin can see why a
// computer was rejected. It must never be used with a secret on stdin.
func (s SSHRemote) runDiagnostic(remote string) (string, error) {
	argv, err := s.argv(remote)
	if err != nil {
		return "", err
	}
	return s.execute(argv, "")
}

// Probe checks one computer without changing it. A transport failure is
// returned as a failed ssh check so admins see the reason, not a bare error.
func (s SSHRemote) Probe() (ProbeResult, error) {
	remote := "python3 -c " + ShellQuote(probeScript)
	// Reject a malformed connection up front: that is a bad request, not an
	// unreachable computer.
	if _, err := s.argv(remote); err != nil {
		return ProbeResult{}, err
	}
	out, err := s.runDiagnostic(remote)
	if err != nil {
		detail := strings.TrimSpace(out)
		if detail == "" {
			detail = "ssh command failed"
		}
		if len(detail) > 400 {
			detail = detail[:400]
		}
		return ProbeResult{Checks: []ProbeCheck{{Name: "ssh", OK: false, Detail: detail}}}, nil
	}
	var res ProbeResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &res); err != nil {
		return ProbeResult{}, fmt.Errorf("invalid probe response")
	}
	return res, nil
}
