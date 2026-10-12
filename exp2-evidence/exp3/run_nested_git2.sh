#!/bin/bash
# RUYI-669 exp3 v2: nested git repos under overlay views + worktree compatibility
# Fixes vs v1: seeded bare repos (empty bare breaks submodule add), defaultBranch=main,
#              mount_kern_view creates mountpoint, guarded template build
set -u
W=/home/agent/workspace
R=$W/exp3
mkdir -p "$R"
cd "$R"
RES=$R/results2.txt
RAW=$R/raw2.txt
: > "$RES"; : > "$RAW"

log() { echo "[$(date +%H:%M:%S)] $*" >> "$R/progress2.log"; }
t() {
  local name=$1; shift
  local out rc=0
  out=$("$@" 2>&1) || rc=$?
  { echo "----- $name (rc=$rc) -----"; [ -n "$out" ] && echo "$out"; } >> "$RAW"
  if [ $rc = 0 ]; then echo "T $name PASS" >> "$RES"; else echo "T $name FAIL(rc=$rc)" >> "$RES"; fi
}
tout() {
  local name=$1; shift
  local out
  out=$("$@" 2>&1); local rc=$?
  { echo "----- $name (rc=$rc) -----"; echo "$out"; } >> "$RAW"
  echo "T $name RECORD(rc=$rc, see raw)" >> "$RES"
}
capture() { local f=$1; shift; "$@" > "$f" 2>&1; }

{
  echo "== exp3 v2 environment =="
  echo "kernel=$(uname -r)"
  echo "fuse_overlayfs=$(fuse-overlayfs --version 2>&1 | head -1)"
  echo "git=$(git --version)"
  echo "init_default_branch=$(git config --get init.defaultBranch || echo unset)"
} > "$R/env2.txt" 2>&1
log "start"

BASE=$R/base
rm -rf "$BASE" "$R/fuseview" "$R/kernview" "$R/samelower" "$R/lower-alias" "$R/wt-external" "$R/samewt-external" \
       "$R/fuse-upper" "$R/fuse-work" "$R/kern-upper" "$R/kern-work" "$R/same-upper" "$R/same-work"
mkdir -p "$BASE"
BARE="$BASE/bares"

seed_bare() { # seed a bare repo with one commit on main
  local b=$1
  git init -q --bare "$b"
  local w=/tmp/seedw.$$
  rm -rf "$w"; git init -q -b main "$w"
  echo base > "$w/base.txt"
  git -C "$w" add -A
  git -C "$w" -c user.email=t@t.local -c user.name=t commit -qm base
  git -C "$w" push -q "$b" main
  git -C "$b" symbolic-ref HEAD refs/heads/main
  rm -rf "$w"
}
seed_bare "$BARE/sub-bare.git"
seed_bare "$BARE/bare-abs.git"

build_template() { # $1 = proj dir, $2 = external worktree path
  local P=$1 EXT=$2
  git init -q -b main "$P" || { log "FAIL init $P"; return 1; }
  git -C "$P" config user.email t@t.local
  git -C "$P" config user.name tester
  echo "# snap project" > "$P/README.md"
  mkdir -p "$P/code"
  printf 'int main(void){return 0;}\n' > "$P/code/app.c"
  git init -q -b main "$P/vendor-nested" || { log "FAIL nested init"; return 1; }
  git -C "$P/vendor-nested" config user.email t@t.local
  git -C "$P/vendor-nested" config user.name tester
  echo "int lib(void){return 1;}" > "$P/vendor-nested/lib.c"
  git -C "$P/vendor-nested" add -A
  git -C "$P/vendor-nested" commit -qm "nested init" || { log "FAIL nested commit"; return 1; }
  git -C "$P/vendor-nested" worktree add -q "$P/vendor-wt" -b vwmain || { log "FAIL nested wt"; return 1; }
  git -C "$P" -c protocol.file.allow=always submodule add -q "$BARE/sub-bare.git" sub-rel || { log "FAIL sub-rel add"; return 1; }
  git -C "$P" -c protocol.file.allow=always submodule add -q "$BARE/bare-abs.git" sub-abs || { log "FAIL sub-abs add"; return 1; }
  git -C "$P" config -f .gitmodules submodule.sub-rel.url ../bares/sub-bare.git
  git -C "$P" add -A
  git -C "$P" commit -qm "parent init with nested+submodules" || { log "FAIL parent commit"; return 1; }
  git -C "$P" worktree add -q "$EXT" -b extb || { log "FAIL ext wt"; return 1; }
  log "template ok: $P (commits=$(git -C $P log --oneline | wc -l))"
}

build_template "$BASE/lower/proj" "$R/wt-external" || { echo "TEMPLATE BUILD FAILED"; cat "$R/progress2.log"; exit 1; }
build_template "$BASE/samelower/proj" "$R/samewt-external" || { echo "TEMPLATE BUILD FAILED"; exit 1; }

mount_fuse_view() {
  rm -rf "$R/fuse-upper" "$R/fuse-work"; mkdir -p "$R/fuse-upper" "$R/fuse-work" "$2"
  fuse-overlayfs -o "lowerdir=$1,upperdir=$R/fuse-upper,workdir=$R/fuse-work" "$2" 2>>"$RAW"
  local i=0; while [ ! -d "$2/proj" ] && [ $i -lt 100 ]; do sleep 0.05; i=$((i+1)); done
}
mount_kern_view() {
  rm -rf "$R/$3-upper" "$R/$3-work"; mkdir -p "$R/$3-upper" "$R/$3-work" "$2"
  sudo mount -t overlay overlay -o "lowerdir=$1,upperdir=$R/$3-upper,workdir=$R/$3-work" "$2" 2>>"$RAW"
}

# =========================================================
echo "== A. fuse-overlayfs view at DIFFERENT path ==" >> "$RES"
mount_fuse_view "$BASE/lower" "$R/fuseview"
FV=$R/fuseview/proj
ALIAS=$BASE/lower/proj
capture "$R/pristine_parent_count.txt" bash -c "git -C $ALIAS log --oneline | wc -l"
tout A01_git_status_parent git -C "$FV" status --porcelain=v1
tout A01b_git_status_long git -C "$FV" status
tout A02_worktree_list_in_view git -C "$FV" worktree list
t A03_nested_commit_in_view bash -c "cd $FV/vendor-nested && echo changed >> lib.c && git add lib.c && git commit -qm view-commit"
t A04_pristine_nested_head_unchanged bash -c "! git -C $ALIAS/vendor-nested rev-parse HEAD | grep -q \$(git -C $FV/vendor-nested rev-parse HEAD)"
tout A05_vendor_wt_gitdir_pointer bash -c "cat $FV/vendor-wt/.git; echo ---; cat $ALIAS/vendor-nested/.git/worktrees/vwmain/gitdir"
t A06_vendor_wt_silent_wrong_target bash -c "cd $FV/vendor-wt && echo probe-in-view > probe.txt && git status --porcelain | grep -q probe.txt"
tout A06b_vendor_wt_status_from_view bash -c "cd $FV/vendor-wt && git status --porcelain; echo gitdir=\$(git rev-parse --git-dir); ls probe.txt"
t A07_worktree_add_in_view bash -c "cd $FV && git worktree add $R/fuseview/proj2-wt -b wtb && [ -d $R/fuseview/proj2-wt ]"
tout A08_worktree_list_after_add git -C "$FV" worktree list
t A09_commit_from_new_wt bash -c "cd $R/fuseview/proj2-wt && echo x >> README.md && git add README.md && git commit -qm wt-commit"
t A10_new_wt_branch_visible_from_view_main_repo bash -c "git -C $FV log --oneline -1 wtb | grep -q wt-commit"
t A11_worktree_remove_in_view bash -c "cd $FV && git worktree remove $R/fuseview/proj2-wt"
t A12_prune_keeps_pristine_intact bash -c "cd $FV && git worktree prune -v; [ \$(git -C $ALIAS worktree list | wc -l) = 2 ]"
tout A13_submodule_status git -C "$FV" submodule status
tout A14_submodule_rel_url_reinit git -C "$FV" -c protocol.file.allow=always submodule update --init --force sub-rel
tout A14b_submodule_rel_deinit_then_init bash -c "cd $FV && git -c protocol.file.allow=always submodule deinit -f sub-rel && git -c protocol.file.allow=always submodule update --init sub-rel"
tout A15_submodule_abs_update git -C "$FV" -c protocol.file.allow=always submodule update --init --force sub-abs
t A16_git_log_show_history bash -c "git -C $FV log --stat | head -40 && git -C $FV show HEAD --stat | head -10"
t A17_git_gc_fsck bash -c "git -C $FV gc -q && git -C $FV fsck --strict"
t A18_parent_commit_in_view bash -c "cd $FV && echo r >> README.md && git add README.md && git commit -qm parent-view-commit"
t A19_pristine_parent_untouched bash -c "[ \$(git -C $ALIAS log --oneline | wc -l) = \$(cat $R/pristine_parent_count.txt) ]"
t A20_branch_switch_in_view bash -c "cd $FV && git checkout -qb featx && git checkout -q main"
tout A21_clean_dry_run git -C "$FV" clean -nxd
tout A22_index_lock_probe bash -c "cd $FV && (git add -A >/dev/null 2>&1 &) ; sleep 0.15 ; git add -A; echo rc=\$?"
fusermount3 -u "$R/fuseview" 2>/dev/null

# =========================================================
echo "== B. kernel overlayfs view at DIFFERENT path (same-fs ext4) ==" >> "$RES"
rm -rf "$BASE/lower2"; cp -a "$BASE/lower" "$BASE/lower2"
mount_kern_view "$BASE/lower2" "$R/kernview" kern
KV=$R/kernview/proj
B2=$BASE/lower2/proj
tout B01_git_status_parent git -C "$KV" status --porcelain=v1
tout B02_worktree_list_in_view git -C "$KV" worktree list
t B03_nested_commit_in_view bash -c "cd $KV/vendor-nested && echo k >> lib.c && git add lib.c && git commit -qm kern-view-commit"
t B04_pristine_lower2_untouched bash -c "[ \$(git -C $B2/vendor-nested rev-parse HEAD) = \$(git -C $BASE/lower/proj/vendor-nested rev-parse HEAD) ]"
t B05_vendor_wt_silent_wrong_target bash -c "cd $KV/vendor-wt && echo kprobe > kprobe.txt && git status --porcelain | grep -q kprobe.txt"
t B06_worktree_add_in_view bash -c "cd $KV && git worktree add $R/kernview/proj2-wt -b kb && git worktree list | grep -q proj2-wt"
t B07_gc_fsck bash -c "git -C $KV gc -q && git -C $KV fsck --strict"
sudo umount "$R/kernview" 2>/dev/null

# =========================================================
echo "== C. kernel overlayfs SAME-PATH view (mount over the lower dir itself) ==" >> "$RES"
SL=$R/samelower
sudo mkdir -p "$R/lower-alias"
sudo mount --bind "$SL/proj" "$R/lower-alias"
mount_kern_view "$SL" "$SL" same
SP=$SL/proj
t C01_git_status_same_path git -C "$SP" status --porcelain=v1
t C02_vendor_wt_targets_view bash -c "cd $SP/vendor-wt && echo sp-probe > sp_probe.txt && git status --porcelain | grep -q sp_probe.txt"
t C03_pristine_alias_not_touched bash -c "[ ! -e $R/lower-alias/vendor-wt/sp_probe.txt ]"
t C04_external_wt_resolves_into_view bash -c "cd $R/samewt-external && echo ext-probe > ext_probe.txt && git status --porcelain | grep -q ext_probe.txt"
t C05_ext_commit_lands_in_view_not_pristine bash -c "cd $R/samewt-external && git add ext_probe.txt && git commit -qm ext-wt-commit && git -C $SP log --oneline -1 | grep -q ext-wt-commit && ! git -C $R/lower-alias log --oneline -1 | grep -q ext-wt-commit"
t C06_pristine_alias_commits_unchanged bash -c "[ \$(git -C $R/lower-alias log --oneline | wc -l) = 1 ]"
sudo umount "$SL" 2>/dev/null
sudo umount "$R/lower-alias" 2>/dev/null

# =========================================================
echo "== D. teardown checks ==" >> "$RES"
t D01_pristine_lower_fsck bash -c "git -C $BASE/lower/proj fsck --strict && [ \$(git -C $BASE/lower/proj log --oneline | wc -l) = 1 ]"
tout D02_remaining_mounts bash -c "mount | grep -E 'overlay|fuse' | grep -v -E 'cgroup|/tmp/hl' || echo none"

log "exp3 v2 complete"
echo "== summary ==" >> "$RES"
echo "PASS=$(grep -c ' PASS$' "$RES")" >> "$RES"
echo "FAIL=$(grep -c ' FAIL' "$RES")" >> "$RES"
cat "$RES"
