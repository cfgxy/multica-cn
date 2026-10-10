package computer

// PAM responses are allocated by libc because libpam owns their lifetime.
const pamScript = `import ctypes as c, ctypes.util, sys, pwd, grp, subprocess, os, stat
# Fail closed instead of falling back to PAM's "other" service.
try:
    fd=os.open("/etc/pam.d/multica-provision",os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK)
    with os.fdopen(fd,"rb") as f:
        st=os.fstat(f.fileno())
        if not stat.S_ISREG(st.st_mode) or st.st_uid!=0 or st.st_mode & 0o022: sys.exit(43)
        if f.read(4096)!=b"auth required pam_unix.so\naccount required pam_unix.so\n": sys.exit(43)
except OSError: sys.exit(43)
u=sys.argv[1]; e=pwd.getpwnam(u)
groups={g.gr_name for g in grp.getgrall() if u in g.gr_mem or g.gr_gid==e.pw_gid}
if e.pw_shell.endswith(("/nologin","/false")): sys.exit(43)
if e.pw_uid<1000 or groups.intersection({"sudo","wheel","admin","docker","lxd","disk"}): sys.exit(43)
# sudo -l may return zero even when the user has no privileges (Ubuntu).
# Accept only its explicit C-locale denial; errors and privilege listings fail closed.
env=dict(os.environ,LC_ALL="C",LANG="C")
check=subprocess.run(["sudo","-n","-l","-U",u],capture_output=True,text=True,timeout=10,env=env)
expected="User "+u+" is not allowed to run sudo on "+os.uname().nodename+"."
if check.returncode not in (0,1) or check.stderr.strip() or check.stdout.strip()!=expected: sys.exit(43)
pw=sys.stdin.buffer.read(4097)
if not pw or len(pw)>4096 or b"\0" in pw: sys.exit(43)
lib=c.CDLL(ctypes.util.find_library("pam")); libc=c.CDLL(ctypes.util.find_library("c"))
class Msg(c.Structure): _fields_=[("style",c.c_int),("msg",c.c_char_p)]
class Resp(c.Structure): _fields_=[("resp",c.c_void_p),("ret",c.c_int)]
CB=c.CFUNCTYPE(c.c_int,c.c_int,c.POINTER(c.POINTER(Msg)),c.POINTER(c.POINTER(Resp)),c.c_void_p)
libc.calloc.argtypes=[c.c_size_t,c.c_size_t]; libc.calloc.restype=c.c_void_p
libc.strdup.argtypes=[c.c_char_p]; libc.strdup.restype=c.c_void_p
@CB
def conv(n,msg,out,data):
    if n<1 or n>32: return 19
    ptr=libc.calloc(n,c.sizeof(Resp))
    if not ptr: return 5
    a=c.cast(ptr,c.POINTER(Resp))
    for i in range(n):
        style=msg[i].contents.style
        if style==1: a[i].resp=libc.strdup(pw)
        elif style==2: a[i].resp=libc.strdup(u.encode())
        elif style not in (3,4): return 19
    out[0]=a
    return 0
class Conv(c.Structure): _fields_=[("conv",CB),("data",c.c_void_p)]
handle=c.c_void_p(); conversation=Conv(conv,None)
lib.pam_start.argtypes=[c.c_char_p,c.c_char_p,c.POINTER(Conv),c.POINTER(c.c_void_p)]
lib.pam_authenticate.argtypes=[c.c_void_p,c.c_int]; lib.pam_acct_mgmt.argtypes=[c.c_void_p,c.c_int]; lib.pam_end.argtypes=[c.c_void_p,c.c_int]
r=lib.pam_start(b"multica-provision",u.encode(),c.byref(conversation),c.byref(handle))
if r: sys.exit(43)
r=lib.pam_authenticate(handle,0)
if r==0: r=lib.pam_acct_mgmt(handle,0)
lib.pam_end(handle,r)
sys.exit(0 if r==0 else (42 if r in (7,9,10,11,12,13) else 43))
`

// Executed as the target uid, never root. Refuse symlink traversal, write
// atomically, and merge only managed configuration into existing files.
const privateFileScript = `import os,sys,pwd,json,secrets,stat
home=pwd.getpwuid(os.getuid()).pw_dir
os.umask(0o077)
root=os.open(home,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
def directory(parent,name):
    try: os.mkdir(name,0o700,dir_fd=parent)
    except FileExistsError: pass
    fd=os.open(name,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW,dir_fd=parent)
    if os.fstat(fd).st_uid!=os.getuid(): raise RuntimeError("directory ownership")
    return fd
def read(fd,name):
    try: f=os.open(name,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK,dir_fd=fd)
    except FileNotFoundError: return b""
    with os.fdopen(f,"rb") as stream:
        if not stat.S_ISREG(os.fstat(stream.fileno()).st_mode): raise RuntimeError("not a regular file")
        body=stream.read(1048577)
        if len(body)>1048576: raise RuntimeError("configuration too large")
        return body
def write(fd,name,body):
    tmp=".multica-"+secrets.token_hex(16)
    f=os.open(tmp,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600,dir_fd=fd)
    try:
        with os.fdopen(f,"wb") as stream:
            stream.write(body); stream.flush(); os.fsync(stream.fileno())
        os.rename(tmp,name,src_dir_fd=fd,dst_dir_fd=fd); os.fsync(fd)
    finally:
        try: os.unlink(tmp,dir_fd=fd)
        except FileNotFoundError: pass
`

const writeFilesScript = privateFileScript + `parts=sys.stdin.buffer.read(262145).split(b"\0")
if len(parts)!=6: raise RuntimeError("invalid payload")
config=directory(root,".config"); managed=directory(config,"multica-provision"); multica=directory(root,".multica")
` + managedProfileScript + `write(managed,"gitconfig",parts[0]); write(managed,"gitlab.token",parts[1]); write(managed,"model.env",parts[2])
write(managed,"git.key",parts[4]); write(managed,"known_hosts",parts[5])
old=read(root,".gitconfig")
include=b'\n[include]\n\tpath = ~/.config/multica-provision/gitconfig\n'
if include not in old: write(root,".gitconfig",old+include)
updates=json.loads(parts[3])
` + mergeDaemonConfigScript

const managedProfileScript = `profile=sys.argv[1] if len(sys.argv)>1 else ""
if profile:
    if not profile.startswith("computer-") or any(c not in "abcdefghijklmnopqrstuvwxyz0123456789-" for c in profile): raise RuntimeError("invalid profile")
    profiles=directory(multica,"profiles"); multica=directory(profiles,profile)
    workspaces=directory(multica,"workspaces")
    os.fchmod(workspaces,0o700)
`

const mergeDaemonConfigScript = `previous=read(multica,"config.json")
merged=json.loads(previous) if previous else {}
if not isinstance(merged,dict): raise RuntimeError("invalid existing config")
merged.update(updates)
if profile: merged["workspaces_root"]=os.path.join(home,".multica","profiles",profile,"workspaces")
write(multica,"config.json",json.dumps(merged).encode()+b"\n")
`

// Only the managed CLI profile is touched; Git/model credentials are independent.
const writeDaemonConfigScript = privateFileScript + `updates=json.loads(sys.stdin.buffer.read(65537))
multica=directory(root,".multica")
` + managedProfileScript + mergeDaemonConfigScript

// Initialize only new accounts; preserve any startup file supplied by /etc/skel.
const initializeZshScript = `import os,pwd

def initialize_zsh(u):
    account=pwd.getpwnam(u)
    home=os.open(account.pw_dir,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
    try:
        if os.fstat(home).st_uid!=account.pw_uid: raise RuntimeError("home ownership")
        try:
            fd=os.open(".zshrc",os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600,dir_fd=home)
        except FileExistsError:
            return
        with os.fdopen(fd,"wb") as stream:
            os.fchown(stream.fileno(),account.pw_uid,account.pw_gid)
            stream.write(b"""# Multica defaults for a new Linux account. Customize this file as needed.
typeset -U path PATH
path=("$HOME/.local/bin" "$HOME/.kimi-code/bin" "$HOME/.grok/bin" $path)
export PATH
HISTFILE="$HOME/.zsh_history"
HISTSIZE=1000
SAVEHIST=1000
setopt HIST_IGNORE_DUPS
bindkey -e
autoload -Uz compinit
compinit
""")
            stream.flush()
            os.fsync(stream.fileno())
        os.fsync(home)
    finally:
        os.close(home)
`
