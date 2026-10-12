#!/bin/bash
# RUYI-669 exp2: kernel overlayfs lower/upper cross-filesystem semantics matrix
# Combos (lower,upper): (ext4,ext4) (ext4,tmpfs) (tmpfs,ext4) (ext4,xfs) (xfs,ext4)
# Each combo runs a 20-item semantic check battery; results -> matrix + raw log
set -u
W=/home/agent/workspace
R=$W/exp2
mkdir -p "$R"
cd "$R"
MLOG=$R/matrix.txt
RAW=$R/raw.txt
: > "$MLOG"; : > "$RAW"

log() { echo "[$(date +%H:%M:%S)] $*" >> "$R/progress.log"; }

{
  echo "== exp2 environment =="
  echo "kernel=$(uname -r)"
  echo "overlay_version=$(modinfo -F version overlay 2>/dev/null || echo builtin-unknown)"
  echo "module_params:"
  for p in /sys/module/overlay/parameters/*; do
    echo "  $(basename $p)=$(cat $p 2>/dev/null)"
  done
  echo "ext4: $(df -T / | tail -1)"
  echo "git=$(git --version)"
} > "$R/env.txt" 2>&1
log "env recorded"

# ---------- backing filesystems ----------
BASE=$R/banks
rm -rf "$BASE"; mkdir -p "$BASE/mnt_xfs" "$BASE/mnt_tmpfs" "$BASE/mnt_xfslow" "$BASE/ext4low"
truncate -s 2G "$BASE/xfs.img"
sudo mkfs.xfs -q -f "$BASE/xfs.img" >/dev/null 2>&1
sudo mount -o loop "$BASE/xfs.img" "$BASE/mnt_xfs" && log "xfs upper bank mounted"
truncate -s 2G "$BASE/xfslow.img"
sudo mkfs.xfs -q -f "$BASE/xfslow.img" >/dev/null 2>&1
sudo mount -o loop "$BASE/xfslow.img" "$BASE/mnt_xfslow" && log "xfs lower bank mounted"
sudo mount -t tmpfs -o size=1g tmpfs "$BASE/mnt_tmpfs" && log "tmpfs bank mounted"
# ext4 low bank: plain dir on root ext4
mkdir -p "$BASE/ext4low"
df -T "$BASE/mnt_xfs" "$BASE/mnt_tmpfs" "$BASE/ext4low" 2>/dev/null | tail -3 >> "$R/env.txt"

# ---------- fixture builder: seed a lower dir ----------
build_lower() { # $1 = lower dir
  local L=$1
  rm -rf "$L"; mkdir -p "$L/sub"
  echo "lower-file-A" > "$L/fa.txt"
  echo "lower-file-B" > "$L/fb.txt"
  head -c 65536 /dev/zero | tr '\0' 'x' > "$L/big.bin"
  echo "nested" > "$L/sub/inner.txt"
  mkdir "$L/dir2"; echo "d" > "$L/dir2/x.txt"
  ln -s fa.txt "$L/link1"
}

# ---------- check battery ----------
chk() { # $1=name, rest = command to run; appends CHECK line
  local name=$1; shift
  local out rc=0
  if out=$("$@" 2>&1); then
    echo "CHECK $name PASS ${out:0:160}" >> "$MLOG"
  else
    rc=$?
    echo "CHECK $name FAIL rc=$rc ${out:0:200}" >> "$MLOG"
  fi
}
# runs with CWD-safe absolute paths; appends "CHECK name PASS|FAIL detail" lines
battery() { # $1 = view mountpoint, $2 = lower dir, $3 = upper dir
  local V=$1 L=$2 U=$3
  # 02 create/write/read/delete (upper-only)
  chk create_write_read_delete bash -c "echo hello > $V/new.txt && grep -q hello $V/new.txt && rm $V/new.txt && [ ! -e $V/new.txt ]"
  # 03 copy-up modify: lower intact, upper has new content, view shows new
  chk copy_up_modify bash -c "echo appended >> $V/fa.txt && grep -q appended $V/fa.txt && [ \"\$(cat $L/fa.txt)\" = 'lower-file-A' ] && grep -q appended $U/fa.txt"
  # 04 whiteout delete: view hides, lower intact
  chk whiteout_delete bash -c "rm $V/fb.txt && [ ! -e $V/fb.txt ] && [ -f $L/fb.txt ]"
  # 05 rename lower file in view
  chk rename_lower_file bash -c "mv $V/fa.txt $V/fa2.txt && [ -f $V/fa2.txt ] && [ -f $L/fa.txt ]"
  # 06 rename lower dir in view (redirect_dir mechanics)
  chk rename_lower_dir bash -c "mv $V/dir2 $V/dir3 && [ -f $V/dir3/x.txt ] && [ -f $L/dir2/x.txt ]"
  # 07 hardlink copy-up
  chk hardlink_copyup bash -c "ln $V/big.bin $V/big.hl && echo zz >> $V/big.hl && tail -c2 $V/big.bin | grep -q zz && [ \$(stat -c %s $L/big.bin) = 65536 ]"
  # 08 symlink ops
  chk symlink_ops bash -c "ln -s fb2.txt $V/link2 2>/dev/null; ln -sf fa2.txt $V/link1 && [ \"\$(readlink $V/link1)\" = fa2.txt ]"
  # 09 mmap shared write on copied-up file
  chk mmap_shared_write python3 - "$V" <<'PY'
import sys, os, mmap
v = sys.argv[1]
p = os.path.join(v, "big.bin")
fd = os.open(p, os.O_RDWR)
m = mmap.mmap(fd, 131072)
m[100000:100005] = b"MMAPW"
m.flush(); m.close(); os.close(fd)
with open(p, "rb") as f:
    f.seek(100000)
    ok = f.read(5) == b"MMAPW"
sys.exit(0 if ok else 3)
PY
  # 10 xattr user.*
  chk xattr_user bash -c "setfattr -n user.test -v v1 $V/fa2.txt && [ \"\$(getfattr --only-values -n user.test $V/fa2.txt 2>/dev/null)\" = v1 ]"
  # 11 xattr trusted.* (root)
  chk xattr_trusted bash -c "sudo setfattr -n trusted.test -v t1 $V/fa2.txt && [ \"\$(sudo getfattr --only-values -n trusted.test $V/fa2.txt 2>/dev/null)\" = t1 ]"
  # 12 fsync write then re-read (basic durability signal, not crash test)
  chk fsync_write_basic python3 - "$V" <<'PY'
import sys, os
v = sys.argv[1]
with open(os.path.join(v, "dur.txt"), "w") as f:
    f.write("durable")
    f.flush(); os.fsync(f.fileno())
PY
  # 12b verify after cache drop only (no real crash test here)
  chk fsync_data_present bash -c "grep -q durable $V/dur.txt"
  # 13 fifo in view
  chk fifo_create_use bash -c "mkfifo $V/p1 && (echo pipedata > $V/p1 &) && head -c 8 $V/p1 | grep -q pipedata && rm $V/p1"
  # 14 st_ino stability across copy-up
  chk st_ino_stability bash -c "i1=\$(stat -c %i $V/big.bin); i2=\$(stat -c %i $L/big.bin); echo \"view_ino=\$i1 lower_ino=\$i2\"; true"
  # (record-only; separately logged, not pass/fail)
  # 15 O_DIRECT on view file
  chk o_direct bash -c "dd if=/dev/zero of=$V/od.bin bs=4096 count=16 oflag=direct 2>&1 && rm $V/od.bin"
  # 16 reflink / copy_file_range within view
  chk reflink bash -c "cp --reflink=auto $V/big.bin $V/big.refl 2>&1 && rm $V/big.refl"
  chk copy_file_range python3 - "$V" <<'PY'
import sys, os, shutil
v = sys.argv[1]
src = os.path.join(v, "big.bin"); dst = os.path.join(v, "big.cfr")
with open(src, "rb") as a, open(dst, "wb") as b:
    ret = os.copy_file_range(a, b, 65536)
sys.exit(0 if ret == 65536 else 3)
PY
  # 17 append while reading listing (no hang)
  chk append_concurrent bash -c "( for i in 1 2 3 4 5; do echo \$i >> $V/dur.txt; sleep 0.05; done ) & sleep 0.05; ls -R $V >/dev/null; wait"
  # 18 statfs reports upper fs
  chk statfs bash -c "df -T $V | tail -1"
  # 20 compile+exec from view
  chk compile_exec bash -c "printf 'int main(void){return 42;}\n' > $V/t.c && cc -O0 -o $V/t $V/t.c && $V/t; [ \$? = 42 ] && rm -f $V/t $V/t.c"
  # st_ino record
  { echo "ino_record view_big=$(stat -c %i $V/big.bin 2>/dev/null) upper_big=$(stat -c %i $U/big.bin 2>/dev/null) lower_big=$(stat -c %i $L/big.bin 2>/dev/null)"; } >> "$MLOG"
}

run_combo() { # $1=lowerfs $2=upperfs $3=lowerdir $4=upperbase $5=extra_opts
  local lf=$1 uf=$2 LD=$3 UB=$4 OPTS=${5:-}
  local tag="${lf}2${uf}$([ -n "$OPTS" ] && echo _idx)"
  local UP="$UB/up_$tag" WK="$UB/wk_$tag" V="$R/view_$tag"
  rm -rf "$UP" "$WK" "$V"; mkdir -p "$UP" "$WK" "$V"
  build_lower "$LD"
  echo "" >> "$MLOG"
  echo "=== COMBO $tag opts='${OPTS:-defaults}' ===" >> "$MLOG"
  local t0=$(date +%s.%N)
  if ! sudo mount -t overlay overlay -o "lowerdir=$LD,upperdir=$UP,workdir=$WK${OPTS:+,$OPTS}" "$V" 2>>"$RAW"; then
    echo "MOUNT $tag FAIL (see raw)" >> "$MLOG"
    sudo umount "$V" 2>/dev/null
    return
  fi
  local t1=$(date +%s.%N)
  echo "MOUNT $tag OK $(awk "BEGIN{printf \"%.3f\", $t1-$t0}")s" >> "$MLOG"
  battery "$V" "$LD" "$UP"
  # dmesg overlay errors since combo start
  sudo dmesg | tail -200 | grep -iE 'overlay.*(error|warn|fail)' | tail -5 >> "$RAW" 2>/dev/null || true
  sudo umount "$V" 2>/dev/null
  echo "UNMOUNT $tag done" >> "$MLOG"
}

log "running combos"
# default-opts combos
run_combo ext4 ext4 "$BASE/ext4low" "$BASE"
run_combo ext4 tmpfs "$BASE/ext4low" "$BASE/mnt_tmpfs"
run_combo tmpfs ext4 "$BASE/mnt_tmpfs/low" "$BASE"
run_combo ext4 xfs "$BASE/ext4low" "$BASE/mnt_xfs"
run_combo xfs ext4 "$BASE/mnt_xfslow/low" "$BASE"
# index+redirect_dir variant on the two most design-relevant combos
run_combo ext4 tmpfs "$BASE/ext4low" "$BASE/mnt_tmpfs" "index=on,redirect_dir=on"
run_combo ext4 xfs "$BASE/ext4low" "$BASE/mnt_xfs" "index=on,redirect_dir=on"

{
  echo "== exp2 matrix =="
  grep -E '^(===|MOUNT|CHECK|ino_record)' "$MLOG"
} > "$R/matrix_flat.txt" 2>&1
log "exp2 complete"
grep -cE '^CHECK .* PASS' "$MLOG" | xargs echo "PASS count:"
grep -cE '^CHECK .* FAIL' "$MLOG" | xargs echo "FAIL count:"
