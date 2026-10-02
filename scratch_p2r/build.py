#!/usr/bin/env python3
"""RUYI-359 Phase 2R builder: 29 current 9xx files -> 17 domain files,
gap-free from 900. Runtime-local scaffolding; not committed.

Rules:
- identity groups (new stem == source stem, single source): file untouched
- merged/rename groups: Phase 2 header replaced by a numbers-only header
- up bodies in listed order; down bodies in reverse order
- CREATE/DROP INDEX CONCURRENTLY stripped from real statements
- 957 down is regenerated (RUYI-360 fix) instead of copied
"""
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
MIG = os.path.join(ROOT, "server", "migrations")
REGEN_957_DOWN = os.path.join(ROOT, "scratch_p2r", "957.down.regenerated.sql")

GROUPS = [
    ("900_agent_webhooks", ["900_agent_webhooks"]),
    ("901_project_instructions", ["901_project_instructions"]),
    ("902_user_admin_state", ["902_user_admin_state"]),
    ("903_execution_profile", ["904_execution_profile"]),
    ("904_runtime_profile_add_deerflow_zcode", ["923_runtime_profile_add_deerflow_zcode"]),
    ("905_agent_session_context_gate", ["907_agent_session_context_gate"]),
    ("906_task_usage", ["908_task_usage_context_tokens", "915_task_usage_run_stats", "924_task_message_is_error"]),
    ("907_marketplace", ["910_marketplace_listing", "914_marketplace_prompt"]),
    ("908_prompt", ["916_prompt_version", "925_prompt_quality_rollup", "929_prompt_quiz", "958_prompt_proposal", "960_prompt_structure_baseline"]),
    ("909_oauth_client", ["939_oauth_client"]),
    ("910_proposal", ["943_proposal"]),
    ("911_knowledge", ["945_knowledge", "950_knowledge_daemon_execution", "953_knowledge_dir_daemon_idx", "954_project_resource_local_dir_daemon_idx"]),
    ("912_issue_run_suppressed", ["949_issue_run_suppressed"]),
    ("913_skill", ["941_skill_version", "957_legislation_e1_removal", "970_runtime_skill_discovery"]),
    ("914_retrospective", ["962_retrospective"]),
    ("915_channel_chat_run_intent", ["965_channel_chat_run_intent"]),
    ("916_agent_task_queue", ["972_agent_task_cancel_requested"]),
]

CONCURRENT_RE = re.compile(
    r"^(CREATE INDEX CONCURRENTLY |CREATE UNIQUE INDEX CONCURRENTLY |DROP INDEX CONCURRENTLY )",
    re.M,
)


def strip_concurrently(text):
    return CONCURRENT_RE.sub(lambda m: m.group(1).replace(" CONCURRENTLY", ""), text)


def strip_p2_header(text):
    """Drop the Phase 2 banner: everything before the first '-- >>>' marker
    line. Files without any marker pass through unchanged (the bash draft
    emptied them here - fixed)."""
    lines = text.splitlines(keepends=True)
    for i, ln in enumerate(lines):
        if ln.startswith("-- >>>"):
            body = "".join(lines[i:])
            break
    else:
        body = text
    return body.lstrip("\n")


def num(stem):
    return stem.split("_", 1)[0]


def member_body(src, direction):
    path = os.path.join(MIG, f"{src}.{direction}.sql")
    if src == "957_legislation_e1_removal" and direction == "down":
        with open(REGEN_957_DOWN, encoding="utf-8") as f:
            regen = f.read()
        # drop the regen file's own marker line; the emit separator already
        # carries provenance
        return regen.split("\n", 1)[1].lstrip("\n")
    with open(path, encoding="utf-8") as f:
        return strip_concurrently(strip_p2_header(f.read()))


def merge_header(new, sources, direction):
    nums = ", ".join(num(s) for s in sources)
    if direction == "up":
        return (
            f"-- RUYI-359 Phase 2R domain consolidation: merges the former migrations\n"
            f"-- {nums} into one atomic migration (renumbered to {new}) on the gap-free\n"
            f"-- 900+ ladder. Statement bodies are unchanged except CREATE/DROP INDEX lost\n"
            f"-- the CONCURRENTLY keyword: every target is created earlier in this same\n"
            f"-- file or by an earlier migration, and the whole file runs as one implicit\n"
            f"-- transaction (914 precedent); existing environments converge via the\n"
            f"-- ledger rewrite and never re-run these files. Original-stem -> new-stem\n"
            f"-- mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.\n"
        )
    return (
        f"-- Down for {new}: reverse concatenation of the former migrations\n"
        f"-- {nums} (original order, descending), renumbered by the RUYI-359 Phase 2R\n"
        f"-- domain consolidation. Mapping rules:\n"
        f"-- server/cmd/migrate/9xx-consolidation.md.\n"
    )


def rename_header(new, oldnum, direction):
    if direction == "up":
        return (
            f"-- Renumbered from the former migration {oldnum} by the RUYI-359 Phase 2R\n"
            f"-- domain consolidation (gap-free 900+ ladder); content otherwise unchanged.\n"
            f"-- Mapping and ledger-rewrite rules: server/cmd/migrate/9xx-consolidation.md.\n"
        )
    return (
        f"-- Down for {new}, renumbered from the former migration {oldnum} by the\n"
        f"-- RUYI-359 Phase 2R domain consolidation; content otherwise unchanged.\n"
        f"-- Mapping rules: server/cmd/migrate/9xx-consolidation.md.\n"
    )


def current_stems():
    stems = set()
    for f in os.listdir(MIG):
        m = re.match(r"^(9\d\d_.+)\.up\.sql$", f)
        if m:
            stems.add(m.group(1))
    return stems


def main():
    keep = {new for new, _ in GROUPS}
    sources_flat = [s for _, srcs in GROUPS for s in srcs]
    disk = current_stems()

    # gate: groups must cover exactly the current on-disk 9xx stems
    if set(sources_flat) != disk:
        missing = sorted(disk - set(sources_flat))
        extra = sorted(set(sources_flat) - disk)
        sys.exit(f"FATAL group/disk mismatch: disk-only={missing} group-only={extra}")
    if sorted(int(num(k)) for k in keep) != list(range(900, 900 + len(keep))):
        sys.exit("FATAL new stems are not a gap-free 900+ ladder")

    removed = []
    for new, sources in GROUPS:
        if len(sources) == 1 and sources[0] == new:
            print(f"kept   {new} (identity, untouched)")
            continue
        for direction in ("up", "down"):
            parts = []
            if len(sources) > 1:
                parts.append(merge_header(new, sources, direction))
            else:
                parts.append(rename_header(new, num(sources[0]), direction))
            parts.append("\n")
            order = sources if direction == "up" else list(reversed(sources))
            for i, src in enumerate(order):
                if i > 0:
                    parts.append(
                        f"\n-- >>> from former migration {num(src)} "
                        f"(RUYI-359 Phase 2R)\n\n"
                    )
                parts.append(member_body(src, direction))
            with open(os.path.join(MIG, f"{new}.{direction}.sql"), "w", encoding="utf-8") as f:
                f.write("".join(parts))
        print(f"built  {new} (from {len(sources)} source(s))")

    for stem in sorted(disk - keep):
        for direction in ("up", "down"):
            p = os.path.join(MIG, f"{stem}.{direction}.sql")
            if os.path.exists(p):
                os.remove(p)
                removed.append(f"{stem}.{direction}.sql")
    for r in removed:
        print(f"removed {r}")

    final = current_stems()
    if final != keep:
        sys.exit(f"FATAL final ladder mismatch: {sorted(final ^ keep)}")
    print(f"---- final ladder: {len(final)} stems, "
          f"max {max(int(num(s)) for s in final)} ----")


if __name__ == "__main__":
    main()
