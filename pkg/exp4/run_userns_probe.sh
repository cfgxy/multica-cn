#!/bin/bash
# RUYI-669 exp4 section A+B: userns unprivileged kernel overlay feasibility + loop attach in userns
set -u
W=/home/agent/workspace
R=$W/exp4
A=$R/a
mkdir -p "$A"
RAW=$A/raw.txt
RES=$A/results.txt
: > "$RAW"; : > "$RES"
SUDO=""; [ "$(id -u)" != 0 ] && SUDO="sudo"

t() { # t NAME RC [note]
  local name=$1 rc=$2 note=${3:-}
  if [ "$rc" = 0 ]; then echo "T $name PASS $note" >> "$RES"; else echo "T $name FAIL(rc=$rc) $note" >> "$RES"; fi
}
p() { echo "----- $* -----" >> "$RAW"; }

{
  echo "== exp4-A environment =="
  echo "kernel=$(uname -r)"
  echo "uid=$(id -u) user=$(id -un)"
  $SUDO sysctl kernel.unprivileged_userns_clone kernel.apparmor_restrict_unprivileged_userns user.max_user_namespaces 2>&1
  if grep -q overlay /proc/filesystems; then echo "overlay_in_proc_filesystems=yes"; else echo "overlay_in_proc_filesystems=NO"; fi
  echo "fuse_overlayfs=$(fuse-overlayfs --version 2>&1 | head -1)"
  echo "unshare=$(unshare --version 2>&1 | head -1)"
  echo "mkfs_xfs=$(mkfs.xfs -V 2>&1 | head -1)"
  echo "apparmor_enabled=$(cat /sys/module/apparmor/parameters/enabled 2>/dev/null || echo unknown)"
} > "$A/env.txt" 2>&1
cat "$A/env.txt" >> "$RAW"

# A0: overlay module load readback + userns gate probe (+ flip if AppArmor-blocked)
$SUDO mount -t tmpfs tmpfs /mnt 2>/dev/null && $SUDO umount /mnt
if grep -q overlay /proc/filesystems; then echo "overlay_after_autoload=yes" >> "$A/env.txt"; else echo "overlay_after_autoload=NO" >> "$A/env.txt"; fi
UNSHARE_OUT=$(unshare --user --map-root-user true 2>&1); UNSHARE_RC=$?
echo "unshare_probe rc=$UNSHARE_RC out=$UNSHARE_OUT" >> "$A/env.txt" 2>&1
if [ $UNSHARE_RC != 0 ]; then
  echo "A0 userns BLOCKED by default; flipping apparmor_restrict_unprivileged_userns for the rest of the run" >> "$RAW"
  $SUDO sysctl -w kernel.apparmor_restrict_unprivileged_userns=0 >> "$RAW" 2>&1
  UNSHARE_OUT=$(unshare --user --map-root-user true 2>&1); UNSHARE_RC=$?
  echo "A0 after flip: unshare rc=$UNSHARE_RC out=$UNSHARE_OUT" >> "$RAW" 2>&1
  t A0_userns_blocked_by_default_then_flipped $UNSHARE_RC
else
  echo "A0 userns allowed by default" >> "$RAW"
  t A0_userns_allowed_by_default 0
fi

BASE=$A/base
rm -rf "$BASE"
mkdir -p "$BASE/lower/proj" "$BASE/upper" "$BASE/work" "$BASE/merged"
echo hello-lower > "$BASE/lower/proj/lower.txt"

# ---------- A1: unshare userns+mountns, mount overlay ext4/ext4, holder stays alive ----------
p "A1 unshare + overlay mount ext4/ext4 (holder pattern)"
unshare --user --map-root-user --mount --propagation private bash -c "
  mount -t overlay overlay -o lowerdir=$BASE/lower,upperdir=$BASE/upper,workdir=$BASE/work $BASE/merged 2>&1 && echo MOUNT_OK
  exec sleep 1800
" > "$A/holder.log" 2>&1 &
HPID=$!
sleep 1
VIEW_OK=0
if $SUDO nsenter -t $HPID -U -m -- test -f "$BASE/merged/proj/lower.txt" 2>>"$RAW"; then VIEW_OK=1; fi
MOUNT_RC=1; grep -q MOUNT_OK "$A/holder.log" && MOUNT_RC=0
{ echo "A1 holder.log: $(tr '\n' ' ' < "$A/holder.log")"; echo "A1 view-lookup ok=$VIEW_OK"; } >> "$RAW"
t A1_userns_overlay_mount_ext4_ext4 $MOUNT_RC "holder_pid=$HPID view_lookup=$VIEW_OK"

p "A1 mountinfo inside ns"
$SUDO nsenter -t $HPID -U -m -- bash -c "grep overlay /proc/self/mountinfo; grep overlay /proc/mounts" >> "$RAW" 2>&1

# ---------- A2: degraded-option mount attempts inside userns ----------
p "A2 option rejection: index/redirect_dir/metacopy inside userns"
for OPT in index=on redirect_dir=on metacopy=on; do
  OUT=$($SUDO nsenter -t $HPID -U -m -- bash -c "
    mkdir -p $BASE/up_$OPT $BASE/wk_$OPT $BASE/m_$OPT
    mount -t overlay overlay -o lowerdir=$BASE/lower,upperdir=$BASE/up_$OPT,workdir=$BASE/wk_$OPT,$OPT $BASE/m_$OPT 2>&1
  " 2>&1); RC=$?
  echo "A2 [$OPT] rc=$RC out=$OUT" >> "$RAW"
  if [ $RC != 0 ]; then
    t "A2_option_rejected_${OPT%=*}" 0 "kernel_err=${OUT:0:150}"
  else
    $SUDO nsenter -t $HPID -U -m -- umount "$BASE/m_$OPT" 2>/dev/null
    t "A2_option_rejected_${OPT%=*}" 1 "option ACCEPTED by kernel"
  fi
done
p "A2 overlay module params host defaults (order: index redirect_dir metacopy xino_auto)"
cat /sys/module/overlay/parameters/index /sys/module/overlay/parameters/redirect_dir /sys/module/overlay/parameters/metacopy /sys/module/overlay/parameters/xino_auto 2>/dev/null >> "$RAW"

# ---------- A3: xattr semantics (user. prefix forced?) ----------
p "A3 xattr: user.overlay.opaque semantics inside userns"
OUT=$($SUDO nsenter -t $HPID -U -m -- bash -c "
  mkdir -p $BASE/lower/ovtest
  echo secret > $BASE/lower/ovtest/hidden.txt
  mkdir -p $BASE/m_ov
  mount -t overlay overlay -o lowerdir=$BASE/lower,upperdir=$BASE/upper,workdir=$BASE/work $BASE/m_ov 2>&1
  python3 - <<'PYA'
import os
up = '$BASE/upper/ovtest'
os.makedirs(up, exist_ok=True)
try:
    os.setxattr(up, b'user.overlay.opaque', b'y')
    print('SET_USER_OK')
except OSError as e:
    print('SET_USER_ERR', e)
print('ls_after_opaque: [' + ','.join(os.listdir('$BASE/m_ov/ovtest')) + ']')
try:
    os.setxattr(up, b'trusted.overlay.opaque', b'y')
    print('TRUSTED_SET_UNEXPECTED')
except PermissionError as e:
    print('TRUSTED_EPERM_CONFIRMED', e)
except OSError as e:
    print('TRUSTED_ERR', e)
print('xattrs_on_upper:', os.listxattr(up))
try:
    print('readback:', os.getxattr(up, b'user.overlay.opaque'))
except OSError as e:
    print('READBACK_ERR', e)
PYA
" 2>&1)
echo "A3 out: $OUT" >> "$RAW"
A3OK=0
echo "$OUT" | grep -q SET_USER_OK && echo "$OUT" | grep -q "ls_after_opaque: \[\]" && echo "$OUT" | grep -q TRUSTED_EPERM_CONFIRMED && A3OK=1
[ $A3OK = 1 ]; t A3_userxattr_forced_trusted_denied $? "opaque_hides_lower+trusted_eperm=$A3OK"

# ---------- A4: redirect_dir behavioral probe (syscall level, mv has copy fallback) ----------
p "A4 rename lower-only dir inside view: rename(2) + mv fallback"
OUT=$($SUDO nsenter -t $HPID -U -m -- bash -c "
  mkdir -p $BASE/lower/rdir && echo rdata > $BASE/lower/rdir/file.txt
  python3 - <<'PYB'
import os
try:
    os.rename('$BASE/m_ov/rdir', '$BASE/m_ov/rdir_sys')
    print('RENAME_SYSCALL_OK')
except OSError as e:
    print('RENAME_SYSCALL_ERR', e)
try:
    v = os.getxattr('$BASE/upper/rdir_mv', b'user.overlay.redirect')
    print('REDIRECT_XATTR_PRESENT', v)
except OSError as e:
    print('NO_REDIRECT_XATTR', e)
print('merged_ls:', sorted(os.listdir('$BASE/m_ov')))
PYB
  mv $BASE/m_ov/rdir $BASE/m_ov/rdir_mv 2>&1 && echo MV_FALLBACK_OK || echo MV_FALLBACK_FAIL
" 2>&1)
echo "A4 out: $OUT" >> "$RAW"
echo "$OUT" | grep -q "RENAME_SYSCALL_ERR"; RC_EXDEV=$?
echo "$OUT" | grep -q NO_REDIRECT_XATTR; RC_NORED=1; [ $? = 0 ] && RC_NORED=0
A4OK=0
{ [ $RC_EXDEV = 0 ] || echo "$OUT" | grep -q RENAME_SYSCALL_OK; } && [ $RC_NORED = 0 ] && A4OK=1
[ $A4OK = 1 ]; t A4_redirect_dir_behavior_recorded $? "exdev_rc=$RC_EXDEV no_redirect_xattr_rc=$RC_NORED out=${OUT:0:200}"

# ---------- A5: cross-fs layers inside userns (ext4 lower + xfs-image upper, root pre-mounts bank) ----------
p "A5 userns overlay with upper on loop-mounted xfs bank (root pre-mount)"
XFSIMG=$A/xfsbank.img
mkdir -p "$A/mnt_xfs"
$SUDO umount "$A/mnt_xfs" 2>/dev/null
truncate -s 2G "$XFSIMG"
if $SUDO mkfs.xfs -q -f "$XFSIMG" >/dev/null 2>&1; then echo "mkfs_xfs_ok" >> "$RAW"; else echo "mkfs_xfs_FAIL" >> "$RAW"; fi
if $SUDO mount -o loop "$XFSIMG" "$A/mnt_xfs" 2>>"$RAW"; then echo "xfs_bank_mounted" >> "$RAW"; fi
$SUDO chown "$(id -un):$(id -gn)" "$A/mnt_xfs"
mkdir -p "$A/mnt_xfs/xupper" "$A/mnt_xfs/xwork" "$BASE/m_xfs"
OUT=$($SUDO nsenter -t $HPID -U -m -- bash -c "
  mount -t overlay overlay -o lowerdir=$BASE/lower,upperdir=$A/mnt_xfs/xupper,workdir=$A/mnt_xfs/xwork $BASE/m_xfs 2>&1 && echo CROSSFS_MOUNT_OK
  ls $BASE/m_xfs/proj/ 2>&1
  echo write-probe > $BASE/m_xfs/proj/copyup.txt && echo COPYUP_OK
  cat $A/mnt_xfs/xupper/proj/copyup.txt 2>&1
" 2>&1)
echo "A5 out: $OUT" >> "$RAW"
echo "$OUT" | grep -q CROSSFS_MOUNT_OK; t A5_userns_crossfs_ext4_lower_xfs_upper $? "$(echo "$OUT" | head -1 | cut -c1-150)"
p "A5 inode comparison under userns cross-fs (xino)"
$SUDO nsenter -t $HPID -U -m -- bash -c "
  stat -c 'lower_ino=%i' $BASE/lower/proj/lower.txt
  stat -c 'view_ino=%i'  $BASE/m_xfs/proj/lower.txt
  echo x >> $BASE/m_xfs/proj/copyup.txt
  stat -c 'upper_ino=%i' $A/mnt_xfs/xupper/proj/copyup.txt
" >> "$RAW" 2>&1

# ---------- A6: visibility model ----------
p "A6 visibility from OUTSIDE the userns"
OUT=$(ls -A "$BASE/merged" 2>&1); echo "A6 host ls merged: [$OUT]" >> "$RAW"
[ -z "$OUT" ]; t A6a_ns_external_cannot_see_view $?
N=$(grep -c "^overlay " /proc/mounts); echo "A6 host /proc/mounts overlay mounts=$N" >> "$RAW"
[ "$N" = "0" ]; t A6b_host_mount_table_no_overlay $?
p "A6 root nsenter CAN enter and inspect (daemon entry path)"
OUT=$($SUDO nsenter -t $HPID -U -m -- ls -A "$BASE/merged" 2>&1); echo "A6 nsenter ls: [$OUT]" >> "$RAW"
echo "$OUT" | grep -q proj; t A6c_root_nsenter_enters_view $?

# ---------- A7: lifecycle ----------
p "A7 lifecycle: write via view -> upper persists; kill holder -> mount evaporates"
$SUDO nsenter -t $HPID -U -m -- bash -c "echo snap-data > $BASE/merged/proj/snapfile.txt" 2>>"$RAW"
sleep 0.2
if cat "$BASE/upper/proj/snapfile.txt" >> "$RAW" 2>&1; then echo "upper_persist_ok" >> "$RAW"; fi
kill $HPID 2>/dev/null; wait $HPID 2>/dev/null
sleep 1
OUT=$($SUDO nsenter -t $HPID -U -m -- true 2>&1); RC=$?
echo "A7 nsenter-after-kill rc=$RC err=$OUT (expect fail)" >> "$RAW"
[ $RC != 0 ]; t A7a_mount_gone_after_ns_exit $?
UM=0; $SUDO umount "$BASE/merged" 2>/dev/null && UM=1
echo "A7 host umount attempt applied=$UM (0 = nothing mounted on host: no umount race)" >> "$RAW"
rm -rf "$BASE/upper" "$BASE/m_ov" "$BASE/m_xfs" "$BASE/m_"* && echo "upper rm-rf ok (no umount needed)" >> "$RAW"
t A7b_upper_cleanup_without_umount $?

# ---------- B1: loop attach inside userns ----------
p "B1 loop attach inside userns (expect EPERM)"
BIMG=$A/b1.img
truncate -s 64M "$BIMG"
unshare --user --map-root-user --mount bash -c "
  losetup --find --show $BIMG 2>&1 || echo LOOP_ATTACH_DENIED
  mount -o loop $BIMG /tmp/loopprobe 2>&1 | head -2
  mkfs.xfs -q -f $BIMG 2>&1 && echo MKFS_IN_USERNS_OK
" > "$A/b1.log" 2>&1
cat "$A/b1.log" >> "$RAW"
grep -q LOOP_ATTACH_DENIED "$A/b1.log"; t B1a_loop_attach_denied_in_userns $?
grep -q MKFS_IN_USERNS_OK "$A/b1.log"; t B1b_mkfs_image_prep_unprivileged $?
p "B1 loop devices on host after test (losetup -a)"
$SUDO losetup -a >> "$RAW" 2>&1
echo "losetup_active_count=$($SUDO losetup -a | wc -l)" >> "$RAW"

echo "== exp4-A summary ==" >> "$RES"
echo "PASS=$(grep -c ' PASS' "$RES") FAIL=$(grep -c ' FAIL' "$RES")" >> "$RES"
echo "===== RESULTS ====="
cat "$RES"
