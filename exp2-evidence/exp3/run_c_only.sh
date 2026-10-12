#!/bin/bash
# RUYI-669 exp3 section C standalone: kernel overlayfs SAME-PATH view
set -u
W=/home/agent/workspace
R=$W/exp3
BASE=$R/base
SL=$BASE/samelower          # FIX: same path that holds the pristine template
RES=$R/resultsC.txt
RAW=$R/rawC.txt
: > "$RES"; : > "$RAW"

t() {
  local name=$1; shift
  local out rc=0
  out=$("$@" 2>&1) || rc=$?
  { echo "----- $name (rc=$rc) -----"; [ -n "$out" ] && echo "$out"; } >> "$RAW"
  if [ $rc = 0 ]; then echo "T $name PASS" >> "$RES"; else echo "T $name FAIL(rc=$rc)" >> "$RES"; fi
}

echo "== C section (standalone rerun), $(date) ==" >> "$RES"

# reset pristine: samelower/proj must have exactly 1 commit and no probes
git -C "$SL/proj" worktree prune >/dev/null 2>&1 || true
rm -rf "$R/lower-alias" "$R/same-upper" "$R/same-work"
sudo mkdir -p "$R/lower-alias"

# bind alias BEFORE the overlay mount (pin the pristine tree)
sudo mount --bind "$SL/proj" "$R/lower-alias" || { echo "BIND FAILED" >> "$RES"; cat "$RES"; exit 1; }

mkdir -p "$R/same-upper" "$R/same-work"
sudo mount -t overlay overlay -o "lowerdir=$SL,upperdir=$R/same-upper,workdir=$R/same-work" "$SL" 2>>"$RAW" \
  || { echo "OVERLAY MOUNT AT $SL FAILED" >> "$RES"; sudo umount "$R/lower-alias" 2>/dev/null; cat "$RES"; exit 1; }
mountpoint -q "$SL" && echo "MOUNT OK: overlay at $SL" >> "$RES" || { echo "MOUNT VERIFY FAILED" >> "$RES"; exit 1; }

SP=$SL/proj
t C01_git_status_same_path git -C "$SP" status --porcelain=v1
t C02_vendor_wt_targets_view bash -c "cd $SP/vendor-wt && echo sp-probe > sp_probe.txt && git status --porcelain | grep -q sp_probe.txt"
t C03_pristine_alias_not_touched bash -c "[ ! -e $R/lower-alias/vendor-wt/sp_probe.txt ]"
t C04_external_wt_resolves_into_view bash -c "cd $R/samewt-external && echo ext-probe > ext_probe.txt && git status --porcelain | grep -q ext_probe.txt"
t C05_ext_commit_lands_in_view_not_pristine bash -c "cd $R/samewt-external && git add ext_probe.txt && git commit -qm ext-wt-commit && git -C $SP log --oneline -1 | grep -q ext-wt-commit && ! git -C $R/lower-alias log --oneline -1 | grep -q ext-wt-commit"
t C06_pristine_alias_commits_unchanged bash -c "[ \$(git -C $R/lower-alias log --oneline | wc -l) = 1 ]"

sudo umount "$SL" 2>/dev/null
sudo umount "$R/lower-alias" 2>/dev/null
echo "== summary ==" >> "$RES"
echo "PASS=$(grep -c ' PASS$' "$RES")" >> "$RES"
echo "FAIL=$(grep -c ' FAIL' "$RES")" >> "$RES"
cat "$RES"
