#!/bin/bash
# RUYI-669 exp4 section A follow-up: cross-fs diagnosis, lifecycle redo, index readback, mkfs size fix
set -u
W=/home/agent/workspace
R=$W/exp4
A2=$R/a2
rm -rf "$A2"; mkdir -p "$A2"
RAW=$A2/raw.txt
RES=$A2/results.txt
: > "$RAW"; : > "$RES"
SUDO=""; [ "$(id -u)" != 0 ] && SUDO="sudo"
dmesg_overlay() { $SUDO dmesg 2>/dev/null | grep -i overlay | tail -6; }

t() {
  local name=$1 rc=$2 note=${3:-}
  if [ "$rc" = 0 ]; then echo "T $name PASS $note" >> "$RES"; else echo "T $name FAIL(rc=$rc) $note" >> "$RES"; fi
}
p() { echo "----- $* -----" >> "$RAW"; }

BASE=$A2/base
mkdir -p "$BASE/lower/proj" "$BASE/merged" "$BASE/upper" "$BASE/work"
echo hello-lower > "$BASE/lower/proj/lower.txt"

# holder (dedicated upper/work, nothing else shares them)
unshare --user --map-root-user --mount --propagation private bash -c "
  mount -t overlay overlay -o lowerdir=$BASE/lower,upperdir=$BASE/upper,workdir=$BASE/work $BASE/merged 2>&1 && echo MOUNT_OK
  exec sleep 1200
" > "$A2/holder.log" 2>&1 &
HPID=$!
sleep 1
grep -q MOUNT_OK "$A2/holder.log"; t P0_holder_mount $?

# ---------- P1: cross-fs upper diagnosis ----------
p "P1 bank1: xfs loop (root pre-mount)"
XIMG=$A2/x.img; mkdir -p "$A2/mnt_x"
$SUDO umount "$A2/mnt_x" 2>/dev/null
truncate -s 2G "$XIMG"; $SUDO mkfs.xfs -q -f "$XIMG" >/dev/null 2>&1
$SUDO mount -o loop "$XIMG" "$A2/mnt_x" 2>>"$RAW" && echo x_bank_mounted >> "$RAW"
$SUDO chown "$(id -un):$(id -gn)" "$A2/mnt_x"
mkdir -p "$A2/mnt_x/up" "$A2/mnt_x/wk" "$BASE/m_x"
p "P1a mount inside userns: ext4 lower + xfs upper"
OUT=$($SUDO nsenter -t $HPID -U -m -- bash -c "
  stat -f -c 'bank_fstype=%T' $A2/mnt_x 2>&1
  mount -t overlay overlay -o lowerdir=$BASE/lower,upperdir=$A2/mnt_x/up,workdir=$A2/mnt_x/wk $BASE/m_x 2>&1 && echo XFS_UPPER_MOUNT_OK
" 2>&1); RC=$?
echo "P1a rc=$RC out: $OUT" >> "$RAW"
p "P1a dmesg overlay tail after xfs-upper attempt"
dmesg_overlay >> "$RAW"
echo "$OUT" | grep -q XFS_UPPER_MOUNT_OK; t P1a_userns_xfs_upper_mount $?

p "P1b control: upper on tmpfs (host mounts bank)"
mkdir -p "$A2/mnt_t"; $SUDO mount -t tmpfs -o size=1g tmpfs "$A2/mnt_t" 2>>"$RAW" && echo t_bank_mounted >> "$RAW"
$SUDO chown "$(id -un):$(id -gn)" "$A2/mnt_t"
mkdir -p "$A2/mnt_t/up" "$A2/mnt_t/wk" "$BASE/m_t"
OUT=$($SUDO nsenter -t $HPID -U -m -- bash -c "
  mount -t overlay overlay -o lowerdir=$BASE/lower,upperdir=$A2/mnt_t/up,workdir=$A2/mnt_t/wk $BASE/m_t 2>&1 && echo TMPFS_UPPER_MOUNT_OK
" 2>&1); RC=$?
echo "P1b rc=$RC out: $OUT" >> "$RAW"
p "P1b dmesg overlay tail after tmpfs-upper attempt"
dmesg_overlay >> "$RAW"
echo "$OUT" | grep -q TMPFS_UPPER_MOUNT_OK; t P1b_userns_tmpfs_upper_mount $?

p "P1c control: upper on same-ext4 bind path (same sb)"
mkdir -p "$BASE/bind" "$BASE/bind/up" "$BASE/bind/wk" "$BASE/m_b"
$SUDO mount --bind "$BASE/lower" "$BASE/bind" 2>>"$RAW" || true
OUT=$($SUDO nsenter -t $HPID -U -m -- bash -c "
  mount -t overlay overlay -o lowerdir=$BASE/lower,upperdir=$BASE/bind/up,workdir=$BASE/bind/wk $BASE/m_b 2>&1 && echo BIND_UPPER_MOUNT_OK
" 2>&1); RC=$?
echo "P1c rc=$RC out: $OUT" >> "$RAW"
echo "$OUT" | grep -q BIND_UPPER_MOUNT_OK; t P1c_userns_bind_samefs_upper_mount $?

# ---------- P3: index=on deep readback ----------
p "P3 index=on mount inside userns: mountinfo + origin xattr readback"
mkdir -p "$BASE/iup" "$BASE/iwk" "$BASE/m_i"
OUT=$($SUDO nsenter -t $HPID -U -m -- bash -c "
  mount -t overlay overlay -o lowerdir=$BASE/lower,upperdir=$BASE/iup,workdir=$BASE/iwk,index=on $BASE/m_i 2>&1 && echo INDEX_MOUNT_OK
  grep overlay /proc/self/mountinfo | grep -c 'm_i'
  echo new > $BASE/m_i/proj/newfile.txt && echo COPYUP_DONE
  python3 -c \"import os; print('upper_xattrs:', os.listxattr('$BASE/iup/proj/newfile.txt'))\"
" 2>&1); RC=$?
echo "P3 rc=$RC out: $OUT" >> "$RAW"
p "P3 dmesg overlay tail after index=on"
dmesg_overlay >> "$RAW"
echo "$OUT" | grep -q INDEX_MOUNT_OK; t P3a_index_mount_accepted $?
echo "$OUT" | grep -q "upper_xattrs:.*origin"; t P3b_index_origin_xattr_present $? "origin_xattr_observability"

# mountinfo options of main merged view
p "P3 mountinfo line of merged view (options text)"
$SUDO nsenter -t $HPID -U -m -- grep " /merged overlay" /proc/self/mountinfo >> "$RAW" 2>&1
$SUDO nsenter -t $HPID -U -m -- cat /proc/self/mountinfo >> "$A2/mountinfo.txt" 2>&1

# ---------- P2: lifecycle redo (write->upper->kill->gone) ----------
p "P2 lifecycle: write via view, upper persists, kill holder, mount evaporates"
$SUDO nsenter -t $HPID -U -m -- bash -c "echo snap-data > $BASE/merged/proj/snapfile.txt" 2>>"$RAW"
sleep 0.2
if [ "$(cat $BASE/upper/proj/snapfile.txt 2>/dev/null)" = "snap-data" ]; then t P2a_upper_persists_after_write 0; else t P2a_upper_persists_after_write 1; fi
kill $HPID 2>/dev/null; wait $HPID 2>/dev/null
sleep 1
$SUDO nsenter -t $HPID -U -m -- true 2>/dev/null; RC=$?
[ $RC != 0 ]; t P2b_mount_unreachable_after_ns_exit $?
rm -rf "$BASE/upper" && echo "upper rm-rf ok" >> "$RAW"
t P2c_upper_cleanup_without_umount $?

# ---------- P4: mkfs.xfs inside userns with adequate size ----------
p "P4 mkfs.xfs 512M inside userns (pure file IO, no caps expected)"
unshare --user --map-root-user --mount bash -c "
  truncate -s 512M $A2/p4.img && mkfs.xfs -q -f $A2/p4.img >/dev/null 2>&1 && echo MKFS_IN_USERNS_OK || echo MKFS_IN_USERNS_FAIL
" > "$A2/p4.log" 2>&1
cat "$A2/p4.log" >> "$RAW"
grep -q MKFS_IN_USERNS_OK "$A2/p4.log"; t P4_mkfs_image_prep_unprivileged $?

# ---------- cleanup banks ----------
$SUDO umount "$A2/mnt_x" "$A2/mnt_t" "$BASE/bind" 2>/dev/null
p "loop devices after run"
$SUDO losetup -a >> "$RAW" 2>&1

echo "== exp4-A2 summary ==" >> "$RES"
echo "PASS=$(grep -c ' PASS' "$RES") FAIL=$(grep -c ' FAIL' "$RES")" >> "$RES"
echo "===== RESULTS ====="
cat "$RES"
