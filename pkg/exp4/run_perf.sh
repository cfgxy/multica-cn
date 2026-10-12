#!/bin/bash
# RUYI-669 exp4 section C: four-arm performance benchmark
# Arms: native (ext4 direct) / kern (privileged kernel overlay, ext4 upper)
#       / kernxfs (privileged kernel overlay, upper on loop-mounted xfs image)
#       / userns (unprivileged kernel overlay inside user+mount ns, ext4 upper)
# Workload template identical to exp1 (RUYI-669 first spike), 3 reps, median.
set -u
W=/home/agent/workspace
R=$W/exp4
P=$R/perf
mkdir -p "$P"
cd "$P"
OUT=$P/results.csv
echo "arm,rep,workload,seconds" > "$OUT"
EV=$P/env.txt
SUDO=""; [ "$(id -u)" != 0 ] && SUDO="sudo"

log() { echo "[$(date +%H:%M:%S)] $*" >> "$P/progress.log"; }

{
  echo "== exp4-C environment =="
  echo "kernel=$(uname -r)"
  echo "cpu_nproc=$(nproc)"
  grep -m1 'model name' /proc/cpuinfo
  free -m | awk 'NR==2{print "ram_mb="$2}'
  echo "rootfs: $(df -T / | tail -1)"
  echo "disk_rotational=$(cat /sys/block/sda/queue/rotational 2>/dev/null)"
  echo "disk_driver=$(readlink /sys/block/sda/device/driver 2>/dev/null)"
  echo "disk_scheduler=$(cat /sys/block/sda/queue/scheduler 2>/dev/null)"
  echo "fuse_overlayfs=$(fuse-overlayfs --version 2>&1 | head -1)"
  echo "git=$(git --version)"
  echo "gcc=$(cc --version | head -1)"
  echo "mkfs_xfs=$(mkfs.xfs -V 2>&1 | head -1)"
  echo "uid=$(id -un)"
  $SUDO sysctl kernel.unprivileged_userns_clone kernel.apparmor_restrict_unprivileged_userns 2>&1
} > "$EV" 2>&1
log "env recorded"

# ---------- 1. pristine template project (identical to exp1) ----------
TPL=$P/tpl
rm -rf "$TPL"
mkdir -p "$TPL/src" "$TPL/data"
TPL_SRC="$TPL/src" python3 - <<'PY'
import os, random
random.seed(42)
root = os.environ["TPL_SRC"]
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
TPL_SRC="$TPL/src" python3 - <<'PY'
import os
root = os.environ["TPL_SRC"]
for i in range(0, 1500, 7):
    p = os.path.join(root, "pkg%02d" % (i % 30), "f%d.c" % i)
    if os.path.exists(p):
        with open(p, "a") as f:
            f.write("/* edit_branch touch %d */\n" % i)
PY
git -C "$TPL" add -A
git -C "$TPL" commit -qm "edit branch: touch every 7th file"
git -C "$TPL" checkout -q main
log "template built: $(du -sh $TPL | cut -f1), files=$(find $TPL -type f | wc -l)"
echo "template_du=$(du -sh $TPL | cut -f1)" >> "$EV"

# ---------- 2. arm preparation ----------
prepare_arm() { # $1 = arm name
  local a=$1
  $SUDO fusermount3 -u "$P/view_fuse" 2>/dev/null || true
  $SUDO umount "$P/view_kern" 2>/dev/null || true
  rm -rf "$P/lower_$a" "$P/upper_$a" "$P/upperwork_$a" "$P/view_$a" "$P/ws_$a" "$P/xfs.img_$a"
  cp -a "$TPL" "$P/lower_$a"
  mkdir -p "$P/upper_$a" "$P/view_$a"
}

drop_caches() { sync; $SUDO sh -c 'echo 3 > /proc/sys/vm/drop_caches' 2>/dev/null || true; }

# executor: HOST runs plain bash -c; USERNS runs via nsenter into holder ns
EXEC_NS_PID=0
runsh() { # $1 = bash -c script string
  if [ "$EXEC_NS_PID" != 0 ]; then
    $SUDO nsenter -t "$EXEC_NS_PID" -U -m -- bash -c "$1"
  else
    bash -c "$1"
  fi
}

# ---------- workloads (semantics identical to exp1; timing on host clock) ----------
run_workloads() { # $1 = ws dir, $2 = arm, $3 = rep
  local ws="$1" arm="$2" rep="$3" t0 t1 d best i
  # w1 full build (cold cache)
  drop_caches
  t0=$(date +%s.%N)
  runsh "make -C '$ws' -j4 >/dev/null 2>&1"
  t1=$(date +%s.%N)
  echo "$arm,$rep,build_full,$(awk "BEGIN{printf \"%.3f\", $t1-$t0}")" >> "$OUT"
  # w2 incremental build (touch 5 files)
  for i in 0 7 14 21 28; do
    runsh "touch '$ws/src/pkg0$i/f$((i*100+3)).c' 2>/dev/null || touch \"\$(find '$ws/src' -name 'f*.c' | sed -n '$((i+1))p')\""
  done
  t0=$(date +%s.%N)
  runsh "make -C '$ws' -j4 >/dev/null 2>&1"
  t1=$(date +%s.%N)
  echo "$arm,$rep,build_incr,$(awk "BEGIN{printf \"%.3f\", $t1-$t0}")" >> "$OUT"
  # w3 git status clean x5 best
  runsh "cd '$ws' && git status --porcelain >/dev/null 2>&1"
  best=9999
  for i in 1 2 3 4 5; do
    t0=$(date +%s.%N); runsh "cd '$ws' && git status --porcelain >/dev/null 2>&1"; t1=$(date +%s.%N)
    d=$(awk "BEGIN{printf \"%.4f\", $t1-$t0}")
    best=$(awk "BEGIN{print ($d<$best)?$d:$best}")
  done
  echo "$arm,$rep,git_status_clean_min5,$best" >> "$OUT"
  # w4 git status with 100 mtime-dirty files
  runsh "cd '$ws' && find src -name '*.c' | head -100 | xargs touch"
  best=9999
  for i in 1 2 3 4 5; do
    t0=$(date +%s.%N); runsh "cd '$ws' && git status --porcelain >/dev/null 2>&1"; t1=$(date +%s.%N)
    d=$(awk "BEGIN{printf \"%.4f\", $t1-$t0}")
    best=$(awk "BEGIN{print ($d<$best)?$d:$best}")
  done
  echo "$arm,$rep,git_status_m100_min5,$best" >> "$OUT"
  # w5 branch round-trip checkout x3
  runsh "cd '$ws' && git checkout -q edit_branch 2>/dev/null && git checkout -q main 2>/dev/null"
  t0=$(date +%s.%N)
  runsh "cd '$ws' && for i in 1 2 3; do git checkout -q edit_branch && git checkout -q main; done"
  t1=$(date +%s.%N)
  echo "$arm,$rep,git_checkout_rt_x3,$(awk "BEGIN{printf \"%.3f\", $t1-$t0}")" >> "$OUT"
  # w6 small-file creation storm 5000 files
  t0=$(date +%s.%N)
  runsh "python3 - '$ws/storm' <<'PYEOF'
import os, sys
d = sys.argv[1]
os.makedirs(d, exist_ok=True)
for i in range(5000):
    with open(os.path.join(d, \"f%05d.js\" % i), \"w\") as f:
        f.write(\"module.exports=%d;\\n\" % i)
PYEOF"
  t1=$(date +%s.%N)
  echo "$arm,$rep,create_5000_files,$(awk "BEGIN{printf \"%.3f\", $t1-$t0}")" >> "$OUT"
  # w7 traversal
  best=9999
  for i in 1 2 3; do
    t0=$(date +%s.%N); runsh "find '$ws' -type f >/dev/null 2>&1"; t1=$(date +%s.%N)
    d=$(awk "BEGIN{printf \"%.4f\", $t1-$t0}")
    best=$(awk "BEGIN{print ($d<$best)?$d:$best}")
  done
  echo "$arm,$rep,find_traverse_min3,$best" >> "$OUT"
  # w8 content grep cold
  drop_caches
  t0=$(date +%s.%N); runsh "grep -r 'pad_1 ' '$ws/src' >/dev/null 2>&1"; t1=$(date +%s.%N)
  echo "$arm,$rep,grep_read_cold,$(awk "BEGIN{printf \"%.3f\", $t1-$t0}")" >> "$OUT"
  # w9 editor save simulation 300 rewrites
  t0=$(date +%s.%N)
  runsh "python3 - '$ws' <<'PYEOF'
import os, sys
ws = sys.argv[1]
for i in range(0, 1500, 5):
    p = os.path.join(ws, \"src\", \"pkg%02d\" % (i % 30), \"f%d.c\" % i)
    if os.path.exists(p):
        with open(p) as f: body = f.read()
        with open(p, \"w\") as f: f.write(body)
PYEOF"
  t1=$(date +%s.%N)
  echo "$arm,$rep,editor_save_300,$(awk "BEGIN{printf \"%.3f\", $t1-$t0}")" >> "$OUT"
  # w10 git add + commit
  t0=$(date +%s.%N)
  runsh "cd '$ws' && git add -A >/dev/null 2>&1 && git commit -qm 'bench rep$rep' >/dev/null 2>&1"
  t1=$(date +%s.%N)
  echo "$arm,$rep,git_add_commit,$(awk "BEGIN{printf \"%.3f\", $t1-$t0}")" >> "$OUT"
}

USERNS_HOLDER=0
mount_userns() {
  mkdir -p "$P/upperwork_userns"
  local t0 t1
  t0=$(date +%s.%N)
  unshare --user --map-root-user --mount --propagation private bash -c "
    mount -t overlay overlay -o lowerdir=$P/lower_userns,upperdir=$P/upper_userns,workdir=$P/upperwork_userns $P/view_userns 2>&1 && echo NS_MOUNT_OK
    exec sleep 900
  " > "$P/holder_userns.log" 2>&1 &
  USERNS_HOLDER=$!
  local ok=0 i=0
  while [ $i -lt 100 ]; do
    if $SUDO nsenter -t "$USERNS_HOLDER" -U -m -- test -f "$P/view_userns/src/main.c" 2>/dev/null; then ok=1; break; fi
    sleep 0.05; i=$((i+1))
  done
  t1=$(date +%s.%N)
  EXEC_NS_PID=$USERNS_HOLDER
  echo "userns mount ok=$ok holder=$USERNS_HOLDER log=$(tr '\n' ' ' < "$P/holder_userns.log")" >> "$P/progress.log"
  awk "BEGIN{printf \"%.3f\", $t1-$t0}"
}

# ---------- 3. run arms ----------
log "=== native arm ==="
for rep in 1 2 3; do
  prepare_arm native
  run_workloads "$P/lower_native" native "$rep"
  log "native rep$rep done"
done

log "=== kern arm (privileged overlay, ext4 upper) ==="
for rep in 1 2 3; do
  prepare_arm kern
  mkdir -p "$P/upperwork_kern"
  local_t0=$(date +%s.%N)
  $SUDO mount -t overlay overlay -o "lowerdir=$P/lower_kern,upperdir=$P/upper_kern,workdir=$P/upperwork_kern" "$P/view_kern"
  local_t1=$(date +%s.%N)
  echo "kern,$rep,mount_to_ready,$(awk "BEGIN{printf \"%.3f\", $local_t1-$local_t0}")" >> "$OUT"
  run_workloads "$P/view_kern" kern "$rep"
  du -sh "$P/upper_kern" 2>/dev/null | cut -f1 >> "$P/upper_size_kern.txt"
  local_t0=$(date +%s.%N); $SUDO umount "$P/view_kern" 2>/dev/null; local_t1=$(date +%s.%N)
  echo "kern,$rep,unmount,$(awk "BEGIN{printf \"%.3f\", $local_t1-$local_t0}")" >> "$OUT"
  log "kern rep$rep done"
done

log "=== kernxfs arm (privileged overlay, upper on loop-mounted xfs image) ==="
for rep in 1 2 3; do
  prepare_arm kernxfs
  IMG="$P/xfs.img_kernxfs"
  MNT="$P/mnt_xfs"
  rm -rf "$MNT"; mkdir -p "$MNT"
  t0=$(date +%s.%N); $SUDO truncate -s 4G "$IMG"; t1=$(date +%s.%N)
  echo "kernxfs,$rep,image_truncate,$(awk "BEGIN{printf \"%.3f\", $t1-$t0}")" >> "$OUT"
  t0=$(date +%s.%N); $SUDO mkfs.xfs -q -f "$IMG" >/dev/null 2>&1; t1=$(date +%s.%N)
  echo "kernxfs,$rep,image_mkfs,$(awk "BEGIN{printf \"%.3f\", $t1-$t0}")" >> "$OUT"
  t0=$(date +%s.%N); $SUDO mount -o loop "$IMG" "$MNT"; t1=$(date +%s.%N)
  echo "kernxfs,$rep,loop_attach_mount,$(awk "BEGIN{printf \"%.3f\", $t1-$t0}")" >> "$OUT"
  rm -rf "$MNT/upper" "$MNT/work"; mkdir -p "$MNT/upper" "$MNT/work"
  t0=$(date +%s.%N)
  $SUDO mount -t overlay overlay -o "lowerdir=$P/lower_kernxfs,upperdir=$MNT/upper,workdir=$MNT/work" "$P/view_kernxfs"
  t1=$(date +%s.%N)
  echo "kernxfs,$rep,mount_to_ready,$(awk "BEGIN{printf \"%.3f\", $t1-$t0}")" >> "$OUT"
  run_workloads "$P/view_kernxfs" kernxfs "$rep"
  du -sh "$MNT/upper" 2>/dev/null | cut -f1 >> "$P/upper_size_kernxfs.txt"
  t0=$(date +%s.%N); $SUDO umount "$P/view_kernxfs" 2>/dev/null; t1=$(date +%s.%N)
  echo "kernxfs,$rep,unmount,$(awk "BEGIN{printf \"%.3f\", $t1-$t0}")" >> "$OUT"
  t0=$(date +%s.%N); $SUDO umount "$MNT" 2>/dev/null; t1=$(date +%s.%N)
  echo "kernxfs,$rep,loop_detach,$(awk "BEGIN{printf \"%.3f\", $t1-$t0}")" >> "$OUT"
  rm -f "$IMG"
  log "kernxfs rep$rep done"
done

log "=== userns arm (unprivileged overlay in user+mount ns, ext4 upper) ==="
for rep in 1 2 3; do
  prepare_arm userns
  MT=$(mount_userns)
  echo "userns,$rep,mount_to_ready,$MT" >> "$OUT"
  run_workloads "$P/view_userns" userns "$rep"
  du -sh "$P/upper_userns" 2>/dev/null | cut -f1 >> "$P/upper_size_userns.txt"
  t0=$(date +%s.%N)
  kill "$USERNS_HOLDER" 2>/dev/null; wait "$USERNS_HOLDER" 2>/dev/null
  t1=$(date +%s.%N)
  echo "userns,$rep,unmount_teardown,$(awk "BEGIN{printf \"%.3f\", $t1-$t0}")" >> "$OUT"
  EXEC_NS_PID=0; USERNS_HOLDER=0
  log "userns rep$rep done"
done

# ---------- 4. summary ----------
{
  echo "== exp4-C summary (median of 3 reps, seconds) =="
  python3 - "$OUT" <<'PY'
import sys, csv, statistics, collections
rows = collections.defaultdict(lambda: collections.defaultdict(list))
with open(sys.argv[1]) as f:
    for r in csv.DictReader(f):
        rows[r["workload"]][r["arm"]].append(float(r["seconds"]))
wls = sorted(rows)
arms = ["native", "kern", "userns", "kernxfs"]
print(f"{'workload':24s}" + "".join(f"{a:>10s}" for a in arms) + f"{'us/native':>10s}{'xfs/native':>11s}")
for w in wls:
    meds = {}
    for a in arms:
        v = rows[w].get(a, [])
        meds[a] = statistics.median(v) if v else float("nan")
    rn = meds["native"] or float("nan")
    print(f"{w:24s}" + "".join(f"{meds[a]:10.3f}" for a in arms)
          + f"{meds['userns']/rn:9.2f}x{meds['kernxfs']/rn:10.2f}x")
PY
} > "$P/summary.txt" 2>&1
# loop device hygiene
$SUDO losetup -a > "$P/losetup_after.txt" 2>&1
echo "losetup_active=$($SUDO losetup -a | wc -l)" >> "$P/summary.txt"
log "exp4-C complete"
cat "$P/summary.txt"
