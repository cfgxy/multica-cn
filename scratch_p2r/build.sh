#!/usr/bin/env bash
# RUYI-359 Phase 2R builder: 29 current 9xx files -> 17 domain files,
# gap-free from 900. Runtime-local scaffolding; not committed.
set -euo pipefail
cd "$(dirname "$0")/.."
MIG=server/migrations

# group entries: new_stem:space-separated source stems (original number order)
GROUPS=(
  "900_agent_webhooks:900_agent_webhooks"
  "901_project_instructions:901_project_instructions"
  "902_user_admin_state:902_user_admin_state"
  "903_execution_profile:904_execution_profile"
  "904_runtime_profile_add_deerflow_zcode:923_runtime_profile_add_deerflow_zcode"
  "905_agent_session_context_gate:907_agent_session_context_gate"
  "906_task_usage:908_task_usage_context_tokens 915_task_usage_run_stats 924_task_message_is_error"
  "907_marketplace:910_marketplace_listing 914_marketplace_prompt"
  "908_prompt:916_prompt_version 925_prompt_quality_rollup 929_prompt_quiz 958_prompt_proposal 960_prompt_structure_baseline"
  "909_oauth_client:939_oauth_client"
  "910_proposal:943_proposal"
  "911_knowledge:945_knowledge 950_knowledge_daemon_execution 953_knowledge_dir_daemon_idx 954_project_resource_local_dir_daemon_idx"
  "912_issue_run_suppressed:949_issue_run_suppressed"
  "913_skill:941_skill_version 957_legislation_e1_removal 970_runtime_skill_discovery"
  "914_retrospective:962_retrospective"
  "915_channel_chat_run_intent:965_channel_chat_run_intent"
  "916_agent_task_queue:972_agent_task_cancel_requested"
)

# strip the Phase 2 header: everything before the first "^-- >>>" marker,
# then leading blank lines. Files without a marker pass through unchanged.
strip_p2_header() {
  awk '/^-- >>>/{found=1} found{print}' "$1" | sed '/./,$!d'
}

# strip CONCURRENTLY from real CREATE/DROP INDEX statements (not comments)
strip_concurrently() {
  sed -E 's/^CREATE INDEX CONCURRENTLY /CREATE INDEX /; s/^CREATE UNIQUE INDEX CONCURRENTLY /CREATE UNIQUE INDEX /; s/^DROP INDEX CONCURRENTLY /DROP INDEX /'
}

merge_header_up() {
  local new=$1; shift
  local nums=()
  local s
  for s in "$@"; do nums+=("${s%%_*}"); done
  local list
  list=$(printf '%s, ' "${nums[@]}"); list=${list%, }
  cat <<EOF
-- RUYI-359 Phase 2R domain consolidation: merges the former migrations
-- ${list} into one atomic migration (renumbered to ${new}) on the gap-free
-- 900+ ladder. Statement bodies are unchanged except CREATE/DROP INDEX lost
-- the CONCURRENTLY keyword: every target is created earlier in this same
-- file or by an earlier migration, and the whole file runs as one implicit
-- transaction (914 precedent); existing environments converge via the
-- ledger rewrite and never re-run these files. Original-stem -> new-stem
-- mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.
EOF
}

rename_header_up() {
  local new=$1 oldnum=$2
  cat <<EOF
-- Renumbered from the former migration ${oldnum} by the RUYI-359 Phase 2R
-- domain consolidation (gap-free 900+ ladder); content otherwise unchanged.
-- Mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.
EOF
}

merge_header_down() {
  local new=$1; shift
  local nums=()
  local s
  for s in "$@"; do nums+=("${s%%_*}"); done
  local list
  list=$(printf '%s, ' "${nums[@]}"); list=${list%, }
  cat <<EOF
-- Down for ${new}: reverse concatenation of the former migrations
-- ${list} (original order, descending), renumbered by the RUYI-359 Phase 2R
-- domain consolidation. Mapping rules:
-- server/cmd/migrate/9xx-consolidation.md.
EOF
}

rename_header_down() {
  local new=$1 oldnum=$2
  cat <<EOF
-- Down for ${new}, renumbered from the former migration ${oldnum} by the
-- RUYI-359 Phase 2R domain consolidation; content otherwise unchanged.
-- Mapping rules: server/cmd/migrate/9xx-consolidation.md.
EOF
}

emit_group() {
  local entry=$1
  local new=${entry%%:*}
  local srcs=${entry#*:}
  local -a sources
  read -ra sources <<<"$srcs"
  local n=${#sources[@]}
  local dir

  for dir in up down; do
    local out="$MIG/${new}.${dir}.sql"
    : >"$out"
    if [ "$n" -gt 1 ]; then
      if [ "$dir" = up ]; then merge_header_up "$new" "${sources[@]}" >>"$out";
      else merge_header_down "$new" "${sources[@]}" >>"$out"; fi
    else
      local oldnum=${sources[0]%%_*}
      if [ "$dir" = up ]; then rename_header_up "$new" "$oldnum" >>"$out";
      else rename_header_down "$new" "$oldnum" >>"$out"; fi
    fi
    printf '\n' >>"$out"

    local -a order=()
    if [ "$dir" = up ]; then
      order=("${sources[@]}")
    else
      local i
      for ((i=n-1; i>=0; i--)); do order+=("${sources[i]}"); done
    fi

    local first=1
    local src
    for src in "${order[@]}"; do
      if [ "$first" != 1 ]; then
        printf '\n-- >>> from former migration %s (RUYI-359 Phase 2R)\n\n' "${src%%_*}" >>"$out"
      fi
      if [ "$src" = "957_legislation_e1_removal" ] && [ "$dir" = down ]; then
        cat scratch_p2r/957.down.regenerated.sql >>"$out"
      else
        strip_p2_header "$MIG/$src.$dir.sql" | strip_concurrently >>"$out"
      fi
      first=0
    done
  done
  echo "built $new (from ${n} source(s))"
}

for g in "${GROUPS[@]}"; do emit_group "$g"; done

# remove every source file whose stem is not a surviving new stem
declare -A KEEP
for g in "${GROUPS[@]}"; do
  KEEP[${g%%:*}]=1
done
for f in "$MIG"/9*.up.sql; do
  stem=$(basename "$f" .up.sql)
  if [ -z "${KEEP[$stem]:-}" ]; then
    rm -f "$MIG/$stem.up.sql" "$MIG/$stem.down.sql"
    echo "removed $stem"
  fi
done

echo "---- final ladder ----"
ls "$MIG" | grep -E '^9' | sed 's/\.\(up\|down\)\.sql//' | sort -u | cat -n
