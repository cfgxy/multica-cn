#!/bin/bash
# RUYI-669 exp4 section D: userns SAME-PATH overlay + nested git linked worktree smoke
# Confirms exp3-C conclusions hold under userns degraded constraints (userxattr, no redirect/index/metacopy)
set -u
W=/home/agent/workspace
R=$W/exp4
D=$R/d
rm -rf "$D"; mkdir -p "$D"
RAW=$D/raw.txt
RES=$D/results.txt
: > "$RAW"; : > "$RES"
SUDO=""; [ "$(id -u)" != 0 ] && SUDO="sudo"

t() {
  local name=$1 rc=$2 note=${3:-}
  if [ "$rc" = 0 ]; then echo "T $name PASS $note" >> "$RES"; else echo "T $name FAIL(rc=$rc) $note" >> "$RES"; fi
}
p() { echo "----- $* -----" >> "$RAW"; }

# ---------- 1. pristine: main repo + nested repo + linked worktree (absolute gitdir pointer) ----------
BASE=$D/pristine
mkdir -p "$BASE/proj"
git -C "$BASE/proj" init -q -b main
git -C "$BASE/proj" config user.email t@t.local
git -C "$BASE/proj" config user.name tester
echo base-file > "$BASE/proj/base.txt"
mkdir -p "$BASE/proj/app"
echo "int main(){return 0;}" > "$BASE/proj/app/main.c"
git -C "$BASE/proj" add -A && git -C "$BASE/proj" commit -qm "pristine base"
# nested repo vendored inside proj
mkdir -p "$BASE/proj/vendor-nested"
git -C "$BASE/proj/vendor-nested" init -q -b vmain
git -C "$BASE/proj/vendor-nested" config user.email t@t.local
git -C "$BASE/proj/vendor-nested" config user.name tester
echo "int vend(int x){return x;}" > "$BASE/proj/vendor-nested/vend.c"
git -C "$BASE/proj/vendor-nested" add -A && git -C "$BASE/proj/vendor-nested" commit -qm "vend base"
# linked worktree of the nested repo (creates .git POINTER FILE with absolute gitdir)
git -C "$BASE/proj/vendor-nested" worktree add -b wtmain "$BASE/proj/vendor-wt" -q 2>>"$RAW"
echo "pristine commits: main=$(git -C "$BASE/proj" rev-parse HEAD) vmain=$(git -C "$BASE/proj/vendor-nested" rev-parse vmain)" >> "$RAW"
p "pointer file content (absolute gitdir proof)"
cat "$BASE/proj/vendor-wt/.git" >> "$RAW"
echo >> "$RAW"

# ---------- 2. userns SAME-PATH overlay mount ----------
p "D mount: overlay at pristine path inside userns (bind alias pins pristine first)"
unshare --user --map-root-user --mount --propagation private bash -c "
  set -e
  mkdir -p $D/alias $D/upper $D/work
  mount --bind $BASE/proj $D/alias
  mount -t overlay overlay -o lowerdir=$BASE/proj,upperdir=$D/upper,workdir=$D/work $BASE/proj 2>&1 && echo SAMEPATH_MOUNT_OK
  test -f $BASE/proj/base.txt && echo VIEW_READS_LOWER_OK
  exec sleep 600
" > "$D/holder.log" 2>&1 &
HPID=$!
sleep 1
MOUNT_RC=1; grep -q SAMEPATH_MOUNT_OK "$D/holder.log" && MOUNT_RC=0
echo "holder.log: $(tr '\n' ' ' < "$D/holder.log")" >> "$RAW"
t D00_userns_samepath_mount $MOUNT_RC "holder=$HPID"

NSRUN() { $SUDO nsenter -t $HPID -U -m -- bash -c "$1"; }

# ---------- 3. tests (inside view; pristine via bind alias is OUTSIDE overlay) ----------
OUT=$(NSRUN "git -C $BASE/proj status --porcelain=v1 2>&1 && echo D1_OK"); echo "D01: $OUT" >> "$RAW"
echo "$OUT" | grep -q D1_OK; t D01_git_status_in_view $?

OUT=$(NSRUN "cd $BASE/proj/vendor-wt && echo sp-probe > sp_probe.txt && git status --porcelain 2>&1 | grep sp_probe && echo D2_OK"); echo "D02: $OUT" >> "$RAW"
echo "$OUT" | grep -q D2_OK; t D02_vendor_wt_targets_view $?

OUT=$([ ! -e "$D/alias/vendor-wt/sp_probe.txt" ] && echo D3_OK); echo "D03: $OUT" >> "$RAW"
echo "$OUT" | grep -q D3_OK; t D03_pristine_alias_not_touched $?

OUT=$(NSRUN "cd $BASE/proj/vendor-wt && git add sp_probe.txt && git commit -qm userns-sp-commit && git rev-parse HEAD && git log --oneline -1 2>&1"); echo "D04: $OUT" >> "$RAW"
COMMIT=$(echo "$OUT" | grep -E '^[0-9a-f]{40}$' | head -1)
[ -n "$COMMIT" ]; t D04_commit_in_vendor_wt $? "commit=${COMMIT:0:9}"
p "D04 physical object placement: view upper vs pristine"
if [ -n "$COMMIT" ]; then
  FOUND_UP=0; FOUND_PRISTINE=0
  find "$D/upper" -type f -name "${COMMIT:0:2}" -path "*objects/${COMMIT:0:2}" 2>/dev/null | grep -q . && FOUND_UP=1
  find "$BASE/proj/.git/objects" -name "${COMMIT:0:2}" 2>/dev/null | grep -q . && FOUND_PRISTINE=1
  # objects are zlib-packed per short-name dir; verify by unpacking presence via git instead:
  UP_HAS=$($SUDO git -C "$D/upper/vendor-nested" cat-file -t $COMMIT 2>/dev/null || echo none)
  PR_HAS=$(git -C "$BASE/proj/vendor-nested" cat-file -t $COMMIT 2>/dev/null || echo none)
  echo "D04 upper_view_repo_catfile=$UP_HAS pristine_nested_catfile=$PR_HAS find_upper_hit=$FOUND_UP find_pristine_hit=$FOUND_PRISTINE" >> "$RAW"
  [ "$UP_HAS" != "none" ] && [ "$PR_HAS" = "none" ]; t D04b_commit_object_in_upper_not_pristine $?
else
  t D04b_commit_object_in_upper_not_pristine 1 "no commit produced"
fi

OUT=$(NSRUN "cd $BASE/proj/vendor-nested && git rev-parse vmain && cd $D/alias/vendor-nested && git rev-parse vmain" 2>&1); echo "D05: $OUT" >> "$RAW"
V1=$(echo "$OUT" | sed -n 1p); V2=$(echo "$OUT" | sed -n 2p)
[ -n "$V1" ] && [ "$V1" = "$V2" ]; t D05_pristine_ref_unchanged $? "view_vmain=$V1 alias_vmain=$V2"

OUT=$(NSRUN "cat $BASE/proj/vendor-wt/.git && cd $BASE/proj/vendor-wt && git rev-parse --git-dir 2>&1"); echo "D06: $OUT" >> "$RAW"
echo "$OUT" | grep -q "gitdir: $BASE/proj/vendor-nested"; t D06_pointer_resolves_into_view $?

# ---------- 4. teardown & hygiene ----------
p "teardown: kill holder, verify pristine intact"
kill $HPID 2>/dev/null; wait $HPID 2>/dev/null
sleep 1
FSCK=$(git -C "$BASE/proj/vendor-nested" fsck 2>&1 | grep -v dangling || true); RC=$?
echo "fsck: [$FSCK] rc=$RC" >> "$RAW"
[ -z "$FSCK" ]; t D07_pristine_fsck_clean $?
BASE_COUNT=$(git -C "$BASE/proj/vendor-nested" rev-list --all --count)
echo "pristine_nested_total_commits=$BASE_COUNT (expect 1)" >> "$RAW"
[ "$BASE_COUNT" = "1" ]; t D08_pristine_no_new_commits $?
rm -rf "$D/upper" "$D/alias" && echo "upper rm-rf ok" >> "$RAW"
t D09_upper_cleanup_without_umount $?

echo "== exp4-D summary ==" >> "$RES"
echo "PASS=$(grep -c ' PASS' "$RES") FAIL=$(grep -c ' FAIL' "$RES")" >> "$RES"
echo "===== RESULTS ====="
cat "$RES"
