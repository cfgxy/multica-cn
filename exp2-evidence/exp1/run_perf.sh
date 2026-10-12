#!/bin/bash
# RUYI-669 exp1: fuse-overlayfs performance baseline under coding/build workloads
# Arms: native (ext4 direct) / fuse-overlayfs / kernel overlayfs (same-fs)
set -u
W=/home/agent/workspace
R=$W/exp1
mkdir -p "$R"
cd "$R"
OUT=$R/results.csv
echo "arm,rep,workload,seconds" > "$OUT"
EV=$R/env.txt

log() { echo "[$(date +%H:%M:%S)] $*" >> "$R/progress.log"; }

# ---------- 0. environment spec ----------
{
  echo "== exp1 environment =="
  echo "kernel=$(uname -r)"
  echo "cpu_nproc=$(nproc)"
  grep -m1 'model name' /proc/cpuinfo
  free -m | awk 'NR==2{print "ram_mb="$2}'
  echo "rootfs: $(df -T / | tail -1)"
  echo "disk_rotational=$(cat /sys/block/sda/queue/rotational 2>/dev/null)"
  echo "disk_driver=$(readlink /sys/block/sda/device/driver 2>/dev/null)"
  echo "disk_scheduler=$(cat /sys/block/sda/queue/scheduler 2>/dev/null)"
  echo "fuse_overlayfs=$(fuse-overlayfs --version 2>&1 | head -1)"
  echo "fusermount3=$(fusermount3 --version 2>&1)"
  echo "git=$(git --version)"
  echo "gcc=$(cc --version | head -1)"
} > "$EV" 2>&1
log "env recorded"

# ---------- 1. pristine template project (not timed) ----------
TPL=$R/tpl
rm -rf "$TPL"
mkdir -p "$TPL/src" "$TPL/data"
python3 - <<'PY'
import os, random
random.seed(42)
root = os.environ.get("TPL_SRC", "/home/agent/workspace/exp1/tpl/src")
N = 1500
for i in range(N):
    d = os.path.join(root, "pkg%02d" % (i % 30))
    os.makedirs(d, exist_ok=True)
    incs = random.sample(range(N), 8)
    lines = ["#include <stdio.h>"] + ["#include \"pkg%02d/f%d.h\"" % (j % 30, j) for j in incs]
    lines.append("int f%d(int x) { return x + %d; }" % (i, i))
    pad = "\n".join("static int pad_%d = %d;" % (i * 100 + k, k) for k in range(30))
    with open(os.path.join(d, "f%d.c" % i), "w") as f:
        f.write("\n".join(lines) + "\n" + pad + "\n")
    with open(os.path.join(d, "f%d.h" % i), "w") as f:
        f.write("#ifndef F%d_H\n#define F%d_H\nint f%d(int);\n#endif\n" % (i, i, i))
with open(os.path.join(root, "main.c"), "w") as f:
    f.write("int main(void){return 41+f0(1)-41;}\n")
PY
cat > "$TPL/Makefile" <<'MK'
SRCS := $(shell find src -name '*.c')
OBJS := $(SRCS:.c=.o)
app: $(OBJS)
	cc -O0 -Isrc -o $@ $^
%.o: %.c
	cc -O1 -Isrc -c -o $@ $<
MK
printf '*.o\napp\n' > "$TPL/.gitignore"
dd if=/dev/urandom of="$TPL/data/blob.bin" bs=1M count=8 2>/dev/null
git -C "$TPL" init -q -b main
git -C "$TPL" config user.email t@t.local
git -C "$TPL" config user.name tester
git -C "$TPL" add -A
git -C "$TPL" commit -qm "init template"
git -C "$TPL" checkout -qb edit_branch
# branch commit touching 200 files (for checkout workload)
python3 - <<'PY'
import os
root = "/home/agent/workspace/exp1/tpl/src"
n = 0
for i in range(0, 1500, 7):
    p = os.path.join(root, "pkg%02d" % (i % 30), "f%d.c" % i)
    if os.path.exists(p):
        with open(p, "a") as f:
            f.write("/* edit_branch touch %d */\n" % i)
        n += 1
PY
git -C "$TPL" add -A
git -C "$TPL" commit -qm "edit branch: touch every 7th file"
git -C "$TPL" checkout -q main
log "template built: $(du -sh $TPL | cut -f1), files=$(find $TPL -type f | wc -l)"
echo "template_du=$(du -sh $TPL | cut -f1)" >> "$EV"

# ---------- 2. arm preparation ----------
prepare_arm() { # $1 = arm name
  local a=$1
  fusermount3 -u "$R/view_fuse" 2>/dev/null || true
  sudo umount "$R/view_kern" 2>/dev/null || true
  rm -rf "$R/lower_$a" "$R/upper_$a" "$R/upperwork_$a" "$R/view_$a" "$R/ws_$a"
  cp -a "$TPL" "$R/lower_$a"
  mkdir -p "$R/upper_$a" "$R/view_$a"
}
mount_fuse() {
  mkdir -p "$R/upperwork_fuse"
  fuse-overlayfs -o lowerdir="$R/lower_fuse",upperdir="$R/upper_fuse",workdir="$R/upperwork_fuse" "$R/view_fuse"
  local i=0
  while [ ! -d "$R/view_fuse/src" ] && [ $i -lt 100 ]; do sleep 0.05; i=$((i+1)); done
}
mount_kern() {
  mkdir -p "$R/upperwork_kern"
  sudo mount -t overlay overlay -o "lowerdir=$R/lower_kern,upperdir=$R/upper_kern,workdir=$R/upperwork_kern" "$R/view_kern"
}

drop_caches() { sync; sudo sh -c 'echo 3 > /proc/sys/vm/drop_caches' 2>/dev/null || true; }

# timed workload runner: WS=workspace dir, ARM, REP globals
run_workloads() {
  local ws="$1" arm="$2" rep="$3"
  local t0 t1
  # w1 full build (cold cache)
  drop_caches
  t0=$(date +%s.%N)
  make -C "$ws" -j4 >/dev/null 2>&1
  t1=$(date +%s.%N)
  echo "$arm,$rep,build_full,$(awk "BEGIN{printf \"%.3f\", $t1-$t0}")" >> "$OUT"
  # w2 incremental build (touch 5 files)
  local i=0
  for i in 0 7 14 21 28; do touch "$ws/src/pkg0$i/f$((i*100+3)).c" 2>/dev/null || touch "$(find "$ws/src" -name 'f*.c' | sed -n "$((i+1))p")"; done
  t0=$(date +%s.%N)
  make -C "$ws" -j4 >/dev/null 2>&1
  t1=$(date +%s.%N)
  echo "$arm,$rep,build_incr,$(awk "BEGIN{printf \"%.3f\", $t1-$t0}")" >> "$OUT"
  # w3 git status clean x5 (median via min)
  ( cd "$ws" && git status --porcelain >/dev/null 2>&1 ) # warm
  local best=9999
  for i in 1 2 3 4 5; do
    t0=$(date +%s.%N); ( cd "$ws" && git status --porcelain >/dev/null 2>&1 ); t1=$(date +%s.%N)
    local d=$(awk "BEGIN{printf \"%.4f\", $t1-$t0}")
    best=$(awk "BEGIN{print ($d<$best)?$d:$best}")
  done
  echo "$arm,$rep,git_status_clean_min5,$best" >> "$OUT"
  # w4 git status after modifying 100 tracked files (mtime-only dirtiness)
  ( cd "$ws" && find src -name '*.c' | head -100 | xargs touch )
  best=9999
  for i in 1 2 3 4 5; do
    t0=$(date +%s.%N); ( cd "$ws" && git status --porcelain >/dev/null 2>&1 ); t1=$(date +%s.%N)
    local d=$(awk "BEGIN{printf \"%.4f\", $t1-$t0}")
    best=$(awk "BEGIN{print ($d<$best)?$d:$best}")
  done
  echo "$arm,$rep,git_status_m100_min5,$best" >> "$OUT"
  # w5 branch round-trip checkout x3 (main<->edit_branch)
  ( cd "$ws" && git checkout -q edit_branch 2>/dev/null && git checkout -q main 2>/dev/null )
  t0=$(date +%s.%N)
  for i in 1 2 3; do ( cd "$ws" && git checkout -q edit_branch && git checkout -q main ); done
  t1=$(date +%s.%N)
  echo "$arm,$rep,git_checkout_rt_x3,$(awk "BEGIN{printf \"%.3f\", $t1-$t0}")" >> "$OUT"
  # w6 small-file creation storm 5000 files (python)
  t0=$(date +%s.%N)
  python3 - "$ws/storm" <<'PY'
import os, sys
d = sys.argv[1]
os.makedirs(d, exist_ok=True)
for i in range(5000):
    with open(os.path.join(d, "f%05d.js" % i), "w") as f:
        f.write("module.exports=%d;\n" % i)
PY
  t1=$(date +%s.%N)
  echo "$arm,$rep,create_5000_files,$(awk "BEGIN{printf \"%.3f\", $t1-$t0}")" >> "$OUT"
  # w7 traversal: find -type f stat all
  best=9999
  for i in 1 2 3; do
    t0=$(date +%s.%N); find "$ws" -type f >/dev/null 2>&1; t1=$(date +%s.%N)
    local d=$(awk "BEGIN{printf \"%.4f\", $t1-$t0}")
    best=$(awk "BEGIN{print ($d<$best)?$d:$best}")
  done
  echo "$arm,$rep,find_traverse_min3,$best" >> "$OUT"
  # w8 content grep read-heavy
  drop_caches
  t0=$(date +%s.%N); grep -r "pad_1 " "$ws/src" >/dev/null 2>&1; t1=$(date +%s.%N)
  echo "$arm,$rep,grep_read_cold,$(awk "BEGIN{printf \"%.3f\", $t1-$t0}")" >> "$OUT"
  # w9 editor save simulation: rewrite 300 tracked files
  t0=$(date +%s.%N)
  python3 - "$ws" <<'PY'
import os, sys
ws = sys.argv[1]
n = 0
for i in range(0, 1500, 5):
    p = os.path.join(ws, "src", "pkg%02d" % (i % 30), "f%d.c" % i)
    if os.path.exists(p):
        with open(p) as f: body = f.read()
        with open(p, "w") as f: f.write(body)
        n += 1
PY
  t1=$(date +%s.%N)
  echo "$arm,$rep,editor_save_300,$(awk "BEGIN{printf \"%.3f\", $t1-$t0}")" >> "$OUT"
  # w10 git add -A + commit (write-heavy metadata)
  t0=$(date +%s.%N)
  ( cd "$ws" && git add -A >/dev/null 2>&1 && git commit -qm "bench rep$rep" >/dev/null 2>&1 )
  t1=$(date +%s.%N)
  echo "$arm,$rep,git_add_commit,$(awk "BEGIN{printf \"%.3f\", $t1-$t0}")" >> "$OUT"
}

# ---------- 3. run arms ----------
log "=== native arm ==="
for rep in 1 2 3; do
  prepare_arm native
  run_workloads "$R/lower_native" native "$rep"
  log "native rep$rep done"
done

log "=== fuse-overlayfs arm ==="
for rep in 1 2 3; do
  prepare_arm fuse
  t0=$(date +%s.%N); mount_fuse; t1=$(date +%s.%N)
  echo "fuse,$rep,mount_to_ready,$(awk "BEGIN{printf \"%.3f\", $t1-$t0}")" >> "$OUT"
  run_workloads "$R/view_fuse" fuse "$rep"
  # upper materialization size
  du -sh --exclude=upperwork* "$R/upper_fuse" 2>/dev/null | cut -f1 >> "$R/upper_size_fuse.txt"
  find "$R/upper_fuse" -type f 2>/dev/null | wc -l >> "$R/upper_files_fuse.txt"
  t0=$(date +%s.%N); fusermount3 -u "$R/view_fuse" 2>/dev/null; t1=$(date +%s.%N)
  echo "fuse,$rep,unmount,$(awk "BEGIN{printf \"%.3f\", $t1-$t0}")" >> "$OUT"
  log "fuse rep$rep done"
done

log "=== kernel overlayfs arm (same-fs ext4) ==="
for rep in 1 2 3; do
  prepare_arm kern
  t0=$(date +%s.%N); mount_kern; t1=$(date +%s.%N)
  echo "kern,$rep,mount_to_ready,$(awk "BEGIN{printf \"%.3f\", $t1-$t0}")" >> "$OUT"
  run_workloads "$R/view_kern" kern "$rep"
  du -sh "$R/upper_kern" 2>/dev/null | cut -f1 >> "$R/upper_size_kern.txt"
  t0=$(date +%s.%N); sudo umount "$R/view_kern" 2>/dev/null; t1=$(date +%s.%N)
  echo "kern,$rep,unmount,$(awk "BEGIN{printf \"%.3f\", $t1-$t0}")" >> "$OUT"
  log "kern rep$rep done"
done

# ---------- 4. summary ----------
{
  echo "== exp1 summary (median of 3 reps) =="
  python3 - "$OUT" <<'PY'
import sys, csv, statistics, collections
rows = collections.defaultdict(lambda: collections.defaultdict(list))
with open(sys.argv[1]) as f:
    for r in csv.DictReader(f):
        rows[r["workload"]][r["arm"]].append(float(r["seconds"]))
wls = sorted(rows)
arms = ["native", "fuse", "kern"]
print(f"{'workload':26s}" + "".join(f"{a:>10s}" for a in arms) + f"{'fuse/native':>12s}")
for w in wls:
    meds = {}
    for a in arms:
        v = rows[w].get(a, [])
        meds[a] = statistics.median(v) if v else float("nan")
    ratio = meds["fuse"] / meds["native"] if meds["native"] else float("nan")
    print(f"{w:26s}" + "".join(f"{meds[a]:10.3f}" for a in arms) + f"{ratio:11.2f}x")
PY
} > "$R/summary.txt" 2>&1
log "exp1 complete"
cat "$R/summary.txt"
