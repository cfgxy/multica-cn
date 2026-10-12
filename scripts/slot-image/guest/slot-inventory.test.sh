#!/usr/bin/env bash
# slot-inventory.test.sh — in-VM orphans-equivalent inventory (image-baked).
# Contract: given the slot env file, report (a) listeners still holding the
# slot's registered ports, (b) multica-shaped processes tied to this slot's
# workspace/ports, (c) git carriers remaining under the workspace root
# (report only — the guard owns disposition). --json mirrors human output.
# Exit 0 clean · 1 residue. Everything runs against fakes; no real processes.
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
inv="$root_dir/scripts/slot-image/guest/slot-inventory.sh"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

fake_bin="$tmp_dir/bin"; mkdir -p "$fake_bin"
export PATH="$fake_bin:$PATH"

PASS=0; FAIL=0
fail() { printf 'FAIL: %s\n' "$1" >&2; FAIL=$((FAIL + 1)); }
ok()   { PASS=$((PASS + 1)); }

write_env() { # <file> [extra k=v...]
  local f=$1; shift
  {
    printf 'SLOT_NAME=pdev1\n'
    printf 'API_PORT=21801\n'
    printf 'WEB_PORT=13801\n'
    printf 'DESKTOP_PORT=57801\n'
    printf 'MCP_PORT=13001\n'
    printf 'WORKSPACE_ROOT=%s/ws\n' "$(dirname "$f")"
    for kv in "$@"; do printf '%s\n' "$kv"; done
  } >"$f"
  mkdir -p "$(dirname "$f")/ws"
}

set_ss() { # <ss-output>
  cat >"$fake_bin/ss" <<EOF
#!/usr/bin/env bash
[ "\${1:-}" = "-tlnp" ] || exit 0
cat <<'SSOUT'
$1
SSOUT
EOF
  chmod +x "$fake_bin/ss"
}
set_ps() { # <ps-output>
  cat >"$fake_bin/ps" <<EOF
#!/usr/bin/env bash
[ "\${1:-}" = "-eo" ] || exit 0
shift 2
cat <<'PSOUT'
$1
PSOUT
EOF
  chmod +x "$fake_bin/ps"
}

CLEAN_SS='State  Recv-Q Send-Q Local Address:Port Peer Address:Process'
CLEAN_PS='    1 /sbin/init'

# --- 1. nothing running: clean ------------------------------------------------
envf="$tmp_dir/a.env"; write_env "$envf"; set_ss "$CLEAN_SS"; set_ps "$CLEAN_PS"
out="$(bash "$inv" --env "$envf")"; rc=$?
if [ "$rc" = 0 ] && grep -q 'inventory: clean' <<<"$out"; then ok; else
  fail "clean: rc=$rc out=$out"; fi

# --- 2. listener on a registered port: residue --------------------------------
envf="$tmp_dir/b.env"; write_env "$envf"
set_ss "$CLEAN_SS
LISTEN 0 511 0.0.0.0:21801 0.0.0.0:* users:((\"cmd\",pid=4242,fd=9))"; set_ps "$CLEAN_PS"
rc=0; out="$(bash "$inv" --env "$envf")" || rc=$?
if [ "$rc" = 1 ] && grep -q 'port 21801' <<<"$out"; then ok; else
  fail "registered-port listener: rc=$rc out=$out"; fi

# --- 3. multica-shaped process tied to slot workspace: residue ----------------
envf="$tmp_dir/c.env"; write_env "$envf"; set_ss "$CLEAN_SS"
set_ps "  424 /home/agent/workspace/multica-api/bin/api --port 39999
    7 systemd"
rc=0; out="$(bash "$inv" --env "$envf")" || rc=$?
if [ "$rc" = 1 ] && grep -q '424' <<<"$out"; then ok; else
  fail "workspace process: rc=$rc out=$out"; fi

# --- 4. unrelated process on unregistered port: clean -------------------------
envf="$tmp_dir/d.env"; write_env "$envf"; set_ps "$CLEAN_PS"
set_ss "$CLEAN_SS
LISTEN 0 128 0.0.0.0:5432 0.0.0.0:* users:((\"postgres\",pid=99,fd=4))"
out="$(bash "$inv" --env "$envf")"; rc=$?
if [ "$rc" = 0 ]; then ok; else fail "unrelated listener: rc=$rc out=$out"; fi

# --- 5. git carriers under workspace root: reported, not blocking -------------
envf="$tmp_dir/e.env"; write_env "$envf"; set_ss "$CLEAN_SS"; set_ps "$CLEAN_PS"
mkdir -p "$tmp_dir/e.d/ws/repo"; git -C "$tmp_dir/e.d/ws/repo" init -qb main 2>/dev/null
sed -i "s|WORKSPACE_ROOT=.*|WORKSPACE_ROOT=$tmp_dir/e.d/ws|" "$envf"
out="$(bash "$inv" --env "$envf")"; rc=$?
if [ "$rc" = 0 ] && grep -q 'git carrier' <<<"$out"; then ok; else
  fail "carrier report: rc=$rc out=$out"; fi

# --- 6. --json output mirrors the verdict -------------------------------------
envf="$tmp_dir/f.env"; write_env "$envf"
set_ss "$CLEAN_SS
LISTEN 0 511 0.0.0.0:13801 0.0.0.0:* users:((\"node\",pid=77,fd=3))"; set_ps "$CLEAN_PS"
rc=0; out="$(bash "$inv" --env "$envf" --json)" || rc=$?
if [ "$rc" = 1 ] && command -v jq >/dev/null \
   && [ "$(jq -r .clean <<<"$out")" = "false" ] \
   && [ "$(jq -r '.port_residues | length' <<<"$out")" = "1" ]; then ok; else
  fail "json: rc=$rc out=$out"; fi

# --- 7. missing env file: usage error, not "clean" ----------------------------
rc=0; out="$(bash "$inv" --env "$tmp_dir/absent.env" 2>&1)" || rc=$?
if [ "$rc" = 2 ]; then ok; else fail "missing env: rc=$rc out=$out"; fi

printf '\nslot-inventory.test.sh: %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ]
