#!/bin/bash
# RUYI-669 exp2b: corrected cross-fs matrix rerun
# Fixes vs exp2: xfs bank chown, hardlink grep tail, mmap length, copy_file_range API,
#                xattr pkg installed, redirect_dir=off boundary probe, loop dev cleanup
set -u
W=/home/agent/workspace
R=$W/exp2
cd "$R"
MLOG=$R/matrix2.txt
RAW=$R/raw2.txt
: > "$MLOG"; : > "$RAW"

chk() { # $1=name, rest=command
  local name=$1; shift
  local out rc=0
  if out=$("$@" 2>&1); then
    echo "CHECK $name PASS ${out:0:160}" >> "$MLOG"
  else
    rc=$?
    echo "CHECK $name FAIL rc=$rc ${out:0:220}" >> "$MLOG"
  fi
}

{
  echo "== exp2b environment =="
  echo "kernel=$(uname -r)"
  echo "overlay_params:"
  ls /sys/module/overlay/parameters/ 2>/dev/null | while read -r p; do
    echo "  $p=$(cat /sys/module/overlay/parameters/$p 2>/dev/null)"
  done
  [ -d /sys/module/overlay/parameters ] || echo "  (params dir not found)"
  echo "attr=$(setfattr --version 2>/dev/null)"
} > "$R/env2.txt" 2>&1

BASE=$R/banks
# --- xfs banks: mount fresh, chown to rpmadmin so upperdir creation works ---
sudo umount "$BASE/mnt_xfs" 2>/dev/null; sudo umount "$BASE/mnt_xfslow" 2>/dev/null
sudo losetup -D 2>/dev/null
sudo mkfs.xfs -q -f "$BASE/xfs.img" >/dev/null 2>&1
sudo mkfs.xfs -q -f "$BASE/xfslow.img" >/dev/null 2>&1
sudo mount -o loop "$BASE/xfs.img" "$BASE/mnt_xfs"
sudo mount -o loop "$BASE/xfslow.img" "$BASE/mnt_xfslow"
sudo chown rpmadmin:rpmadmin "$BASE/mnt_xfs" "$BASE/mnt_xfslow"
# writability probe (fail fast if still unwritable)
touch "$BASE/mnt_xfs/.wprobe" "$BASE/mnt_xfslow/.wprobe" 2>/dev/null || { echo "BANKS UNWRITABLE - ABORT"; exit 1; }
rm -f "$BASE/mnt_xfs/.wprobe" "$BASE/mnt_xfslow/.wprobe"
rm -rf "$BASE/mnt_tmpfs"; mkdir -p "$BASE/mnt_tmpfs"
sudo mount -t tmpfs -o size=1g tmpfs "$BASE/mnt_tmpfs"
mkdir -p "$BASE/ext4low"
echo "banks ready: $(df -T "$BASE/mnt_xfs" "$BASE/mnt_tmpfs" | tail -2 | tr '\n' ' ')" >> "$R/env2.txt"

build_lower() {
  local L=$1
  rm -rf "$L"; mkdir -p "$L/sub"
  echo "lower-file-A" > "$L/fa.txt"
  echo "lower-file-B" > "$L/fb.txt"
  head -c 65536 /dev/zero | tr '\0' 'x' > "$L/big.bin"
  mkdir "$L/dir2"; echo "d" > "$L/dir2/x.txt"
  # pre-seeded user xattr on lower file (copy-up survival probe)
  setfattr -n user.preseed -v lowerval "$L/fa.txt"
}

battery() {
  local V=$1 L=$2 U=$3
  chk create_write_read_delete bash -c "echo hello > $V/new.txt && grep -q hello $V/new.txt && rm $V/new.txt && [ ! -e $V/new.txt ]"
  chk copy_up_modify bash -c "echo appended >> $V/fa.txt && grep -q appended $V/fa.txt && [ \"\$(cat $L/fa.txt)\" = 'lower-file-A' ] && grep -q appended $U/fa.txt"
  chk whiteout_delete bash -c "rm $V/fb.txt && [ ! -e $V/fb.txt ] && [ -f $L/fb.txt ]"
  chk rename_lower_file bash -c "mv $V/fa.txt $V/fa2.txt && [ -f $V/fa2.txt ] && [ -f $L/fa.txt ]"
  chk rename_lower_dir bash -c "mv $V/dir2 $V/dir3 && [ -f $V/dir3/x.txt ] && [ -f $L/dir2/x.txt ]"
  chk hardlink_copyup bash -c "ln $V/big.bin $V/big.hl && echo zz >> $V/big.hl && tail -c 8 $V/big.bin | grep -q zz && [ \$(stat -c %s $L/big.bin) = 65536 ]"
  chk symlink_ops bash -c "ln -sf fa2.txt $V/link1 && [ \"\$(readlink $V/link1)\" = fa2.txt ]"
  chk mmap_shared_write python3 - "$V" <<'PY'
import sys, os, mmap
v = sys.argv[1]
p = os.path.join(v, "big.bin")
fd = os.open(p, os.O_RDWR)
m = mmap.mmap(fd, 0)          # whole file
m[60000:60005] = b"MMAPW"
m.flush(); m.close(); os.close(fd)
body = open(p, "rb").read()[60000:60005]
lo = open(os.path.join(sys.argv[2] if len(sys.argv) > 2 else "", "big.bin"), "rb").read()[60000:60005] if len(sys.argv) > 2 else b"?"
sys.exit(0 if body == b"MMAPW" else 3)
PY
  chk xattr_user_view bash -c "setfattr -n user.test -v v1 $V/fa2.txt && [ \"\$(getfattr --only-values -n user.test $V/fa2.txt 2>/dev/null)\" = v1 ]"
  chk xattr_survives_copyup bash -c "[ \"\$(getfattr --only-values -n user.preseed $V/fa2.txt 2>/dev/null)\" = lowerval ] && grep -q appended $U/fa.txt && [ \"\$(getfattr --only-values -n user.preseed $U/fa.txt 2>/dev/null)\" = lowerval ]"
  chk xattr_trusted bash -c "sudo setfattr -n trusted.test -v t1 $V/fa2.txt && [ \"\$(sudo getfattr --only-values -n trusted.test $V/fa2.txt 2>/dev/null)\" = t1 ]"
  chk fsync_write_basic bash -c "python3 -c \"
import os,sys
v='$V'
f=open(v+'/dur.txt','w'); f.write('durable'); f.flush(); os.fsync(f.fileno()); f.close()\"
grep -q durable $V/dur.txt"
  chk fifo_create_use bash -c "mkfifo $V/p1 && (echo pipedata > $V/p1 &) && head -c 8 $V/p1 | grep -q pipedata && rm $V/p1"
  chk o_direct bash -c "dd if=/dev/zero of=$V/od.bin bs=4096 count=16 oflag=direct 2>&1 && rm $V/od.bin"
  chk reflink_within_view bash -c "cp --reflink=auto $V/big.bin $V/big.refl && rm $V/big.refl"
  chk copy_file_range_lowerto_view python3 - "$V" <<'PY'
import sys, os, traceback
v = sys.argv[1]
try:
    sfd = os.open(os.path.join(v, "big.hl"), os.O_RDONLY)
    dfd = os.open(os.path.join(v, "big.cfr"), os.O_WRONLY | os.O_CREAT, 0o644)
    n = os.copy_file_range(sfd, 0, dfd, 0, 65536)
    os.close(sfd); os.close(dfd)
    print("cfr lower->view bytes:", n)
    sys.exit(0 if n == 65539 else 3)
except Exception:
    traceback.print_exc()
    sys.exit(1)
PY
  chk copy_file_range_upper_to_upper python3 - "$V" <<'PY'
import sys, os, traceback
v = sys.argv[1]
try:
    sfd = os.open(os.path.join(v, "big.cfr"), os.O_RDONLY)
    dfd = os.open(os.path.join(v, "big.cfr2"), os.O_WRONLY | os.O_CREAT, 0o644)
    n = os.copy_file_range(sfd, 0, dfd, 0, 65539)
    os.close(sfd); os.close(dfd)
    print("cfr upper->upper bytes:", n)
    sys.exit(0 if n == 65539 else 3)
except Exception:
    traceback.print_exc()
    sys.exit(1)
PY
  chk append_concurrent bash -c "( for i in 1 2 3 4 5; do echo \$i >> $V/dur.txt; sleep 0.05; done ) & sleep 0.05; ls -R $V >/dev/null; wait"
  chk compile_exec bash -c "printf 'int main(void){return 42;}\n' > $V/t.c && cc -O0 -o $V/t $V/t.c && $V/t; [ \$? = 42 ] && rm -f $V/t $V/t.c"
  echo "ino_record view_big=$(stat -c %i $V/big.bin 2>/dev/null) upper_big=$(stat -c %i $U/big.bin 2>/dev/null) lower_big=$(stat -c %i $L/big.bin 2>/dev/null)" >> "$MLOG"
}

run_combo() { # $1=lowerfs $2=upperfs $3=lowerdir $4=upperbase $5=extra_opts
  local lf=$1 uf=$2 LD=$3 UB=$4 OPTS=${5:-}
  local tag="${lf}2${uf}$([ -n "$OPTS" ] && echo _$(echo "$OPTS" | tr ',=' '__' | cut -c1-20))"
  local UP="$UB/up_$tag" WK="$UB/wk_$tag" V="$R/view_$tag"
  rm -rf "$UP" "$WK" "$V"; mkdir -p "$UP" "$WK" "$V"
  build_lower "$LD"
  echo "" >> "$MLOG"
  echo "=== COMBO $tag opts='${OPTS:-defaults}' ===" >> "$MLOG"
  local t0=$(date +%s.%N)
  if ! sudo mount -t overlay overlay -o "lowerdir=$LD,upperdir=$UP,workdir=$WK${OPTS:+,$OPTS}" "$V" 2>>"$RAW"; then
    echo "MOUNT $tag FAIL (see raw2)" >> "$MLOG"
    sudo umount "$V" 2>/dev/null
    return
  fi
  local t1=$(date +%s.%N)
  echo "MOUNT $tag OK $(awk "BEGIN{printf \"%.3f\", $t1-$t0}")s" >> "$MLOG"
  battery "$V" "$LD" "$UP"
  sudo dmesg | tail -60 | grep -iE 'overlay.*(error|warn|fail)' | grep -v "xino" | tail -3 >> "$RAW" 2>/dev/null || true
  sudo umount "$V" 2>/dev/null
}

run_combo ext4 ext4 "$BASE/ext4low" "$BASE"
run_combo ext4 tmpfs "$BASE/ext4low" "$BASE/mnt_tmpfs"
run_combo tmpfs ext4 "$BASE/mnt_tmpfs/low" "$BASE"
run_combo ext4 xfs "$BASE/ext4low" "$BASE/mnt_xfs"
run_combo xfs ext4 "$BASE/mnt_xfslow/low" "$BASE"
run_combo ext4 tmpfs "$BASE/ext4low" "$BASE/mnt_tmpfs" "index=on,redirect_dir=on"
run_combo ext4 xfs "$BASE/ext4low" "$BASE/mnt_xfs" "index=on,redirect_dir=on"
run_combo ext4 ext4 "$BASE/ext4low" "$BASE" "redirect_dir=off"

# redirect_dir=off boundary: renamed lower dir should EXDEV; capture verbatim
{
  echo "=== redirect_dir=off boundary detail ==="
  V=$R/view_rdoff; UP=$BASE/up_rdoff; WK=$BASE/wk_rdoff; LD=$BASE/ext4low_rd
  rm -rf "$UP" "$WK" "$V" "$LD"; mkdir -p "$UP" "$WK" "$V" "$LD/dir2"
  echo x > "$LD/dir2/x.txt"; echo y > "$LD/y.txt"
  sudo mount -t overlay overlay -o "lowerdir=$LD,upperdir=$UP,workdir=$WK,redirect_dir=off" "$V" 2>>"$RAW"
  echo "-- mv lower dir (expect EXDEV):"
  mv "$V/dir2" "$V/dir3" 2>&1 || echo "rc=$? (EXDEV expected)"
  echo "-- mv upper-only dir (should work):"
  mkdir "$V/udir"; mv "$V/udir" "$V/udir2" && echo "upper-dir rename OK"
  sudo umount "$V" 2>/dev/null
} >> "$MLOG" 2>&1

# teardown loop devices
sudo umount "$BASE/mnt_xfs" "$BASE/mnt_xfslow" "$BASE/mnt_tmpfs" 2>/dev/null
sudo losetup -D 2>/dev/null

grep -E '^(===|MOUNT|CHECK|ino_record)' "$MLOG" > "$R/matrix2_flat.txt"
echo "PASS=$(grep -cE '^CHECK .* PASS' "$MLOG") FAIL=$(grep -cE '^CHECK .* FAIL' "$MLOG")"
cat "$R/matrix2_flat.txt"
