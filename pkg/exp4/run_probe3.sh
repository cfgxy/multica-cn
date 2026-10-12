#!/bin/bash
# RUYI-669 exp4 A-followup-3: cross-fs upper retest with banks mounted BEFORE holder ns
set -u
W=/home/agent/workspace
R=$W/exp4
A3=$R/a3
rm -rf "$A3"; mkdir -p "$A3"
RAW=$A3/raw.txt
RES=$A3/results.txt
: > "$RAW"; : > "$RES"
SUDO=""; [ "$(id -u)" != 0 ] && SUDO="sudo"

# node gate: stock ubuntu-24.04 blocks unprivileged userns via AppArmor; flip and record
BEFORE=$($SUDO sysctl -n kernel.apparmor_restrict_unprivileged_userns 2>/dev/null)
$SUDO sysctl -w kernel.apparmor_restrict_unprivileged_userns=0 >/dev/null 2>&1
AFTER=$($SUDO sysctl -n kernel.apparmor_restrict_unprivileged_userns 2>/dev/null)
{ echo "Q-1 sysctl before=$BEFORE after=$AFTER"; unshare --user --map-root-user true 2>&1 && echo "unshare_ok" || echo "unshare_STILL_BLOCKED"; } >> "$RAW"

t() { local name=$1 rc=$2 note=${3:-}
  if [ "$rc" = 0 ]; then echo "T $name PASS $note" >> "$RES"; else echo "T $name FAIL(rc=$rc) $note" >> "$RES"; fi; }
p() { echo "----- $* -----" >> "$RAW"; }

BASE=$A3/base
mkdir -p "$BASE/lower/proj" "$BASE/merged" "$BASE/upper" "$BASE/work" "$A3/mnt_x" "$A3/mnt_t" "$BASE/m_x" "$BASE/m_t"
echo hello-lower > "$BASE/lower/proj/lower.txt"

# ---------- banks FIRST ----------
p "banks mounted BEFORE holder creation"
XIMG=$A3/x.img
truncate -s 2G "$XIMG"; $SUDO mkfs.xfs -q -f "$XIMG" >/dev/null 2>&1
$SUDO mount -o loop "$XIMG" "$A3/mnt_x" 2>>"$RAW" && echo x_bank_mounted >> "$RAW"
$SUDO mount -t tmpfs -o size=1g tmpfs "$A3/mnt_t" 2>>"$RAW" && echo t_bank_mounted >> "$RAW"
$SUDO chown "$(id -un):$(id -gn)" "$A3/mnt_x" "$A3/mnt_t"
mkdir -p "$A3/mnt_x/up" "$A3/mnt_x/wk" "$A3/mnt_t/up" "$A3/mnt_t/wk"

# ---------- holder AFTER banks ----------
unshare --user --map-root-user --mount --propagation private bash -c "
  mount -t overlay overlay -o lowerdir=$BASE/lower,upperdir=$BASE/upper,workdir=$BASE/work $BASE/merged 2>&1 && echo MOUNT_OK
  exec sleep 900
" > "$A3/holder.log" 2>&1 &
HPID=$!
sleep 1
grep -q MOUNT_OK "$A3/holder.log"; t Q0_holder_mount $?

p "Q1 bank visibility inside ns (stat -f fstype)"
$SUDO nsenter -t $HPID -U -m -- bash -c "
  stat -f -c 'x_bank_fstype=%T' $A3/mnt_x/. 2>&1
  stat -f -c 't_bank_fstype=%T' $A3/mnt_t/. 2>&1
  stat -f -c 'lower_fstype=%T' $BASE/lower/. 2>&1
" >> "$RAW" 2>&1

p "Q2 userns overlay: ext4 lower + xfs-loop upper"
OUT=$($SUDO nsenter -t $HPID -U -m -- bash -c "
  mount -t overlay overlay -o lowerdir=$BASE/lower,upperdir=$A3/mnt_x/up,workdir=$A3/mnt_x/wk $BASE/m_x 2>&1 && echo XFS_UPPER_MOUNT_OK
  echo probe > $BASE/m_x/proj/cp.txt 2>/dev/null && echo COPYUP_OK
  cat $A3/mnt_x/up/proj/cp.txt 2>/dev/null
" 2>&1); RC=$?
echo "Q2 rc=$RC out: $OUT" >> "$RAW"
p "Q2 dmesg tail"
$SUDO dmesg 2>/dev/null | grep -i overlay | tail -4 >> "$RAW"
echo "$OUT" | grep -q XFS_UPPER_MOUNT_OK; t Q2_userns_crossfs_xfs_upper_mount $?
if echo "$OUT" | grep -q COPYUP_OK; then t Q2b_crossfs_copyup_works 0; else t Q2b_crossfs_copyup_works 1; fi

p "Q3 userns overlay: ext4 lower + tmpfs upper (different fs, no loop)"
OUT=$($SUDO nsenter -t $HPID -U -m -- bash -c "
  mount -t overlay overlay -o lowerdir=$BASE/lower,upperdir=$A3/mnt_t/up,workdir=$A3/mnt_t/wk $BASE/m_t 2>&1 && echo TMPFS_UPPER_MOUNT_OK
" 2>&1); RC=$?
echo "Q3 rc=$RC out: $OUT" >> "$RAW"
$SUDO dmesg 2>/dev/null | grep -i overlay | tail -3 >> "$RAW"
echo "$OUT" | grep -q TMPFS_UPPER_MOUNT_OK; t Q3_userns_crossfs_tmpfs_upper_mount $?

p "Q4 inode semantics in userns same-fs (xino readback)"
$SUDO nsenter -t $HPID -U -m -- bash -c "
  echo ino-probe > $BASE/merged/proj/ino.txt
  stat -c 'lower_ino=%i' $BASE/lower/proj/lower.txt
  stat -c 'view_ino=%i'  $BASE/merged/proj/lower.txt
" >> "$RAW" 2>&1
p "Q4 dmesg xino lines"
$SUDO dmesg 2>/dev/null | grep -iE "overlay.*(xino|file handle)" | tail -3 >> "$RAW"

p "Q5 loop hygiene"
$SUDO losetup -a >> "$RAW" 2>&1
echo "losetup_active=$($SUDO losetup -a | grep -c x.img)" >> "$RAW"

# teardown
kill $HPID 2>/dev/null; wait $HPID 2>/dev/null
$SUDO umount "$A3/mnt_x" "$A3/mnt_t" 2>/dev/null
$SUDO rm -f "$XIMG"

echo "== exp4-A3 summary ==" >> "$RES"
echo "PASS=$(grep -c ' PASS' "$RES") FAIL=$(grep -c ' FAIL' "$RES")" >> "$RES"
echo "===== RESULTS ====="
cat "$RES"
