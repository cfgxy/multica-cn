#!/usr/bin/env bash
# build-seed.test.sh — renders the autoinstall seed for multica-slot-base and
# pins its contract: pinned versions, guest scripts embedded byte-identical,
# NO build toolchain in the package list (方案 §4.1: 不含构建工具链), key +
# console rescue password, sudoers/fstrim/KVP facts, and a seed manifest that
# finalize-base.ps1 later folds into the image manifest.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
build_seed="$here/build-seed.sh"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

pubkey_file="$tmp_dir/acceptance.pub"
printf 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE1AAAAIT3stK3yTESTPUBLICKEYMATERIAL000000000 slot-acceptance-test\n' >"$pubkey_file"

PASS=0; FAIL=0
fail() { printf 'FAIL: %s\n' "$1" >&2; FAIL=$((FAIL + 1)); }
ok()   { PASS=$((PASS + 1)); }

seed="$tmp_dir/seed"
bash "$build_seed" --out "$seed" --pubkey-file "$pubkey_file" --image-version 2026.10.11-test
ud="$seed/user-data"
md="$seed/meta-data"
sm="$seed/seed-manifest.json"

[ -f "$ud" ] && [ -f "$md" ] && [ -f "$sm" ] && ok || fail "seed files missing"

# --- 1. cloud-config envelope + autoinstall basics --------------------------
head -1 "$ud" | grep -q '^#cloud-config$' && ok || fail "user-data missing #cloud-config header"
grep -q '^autoinstall:' "$ud" && ok || fail "autoinstall section missing"
grep -q '^  version: 1' "$ud" && ok || fail "autoinstall version missing"
grep -q '^  shutdown: poweroff' "$ud" && ok || fail "shutdown: poweroff missing (install VM must power off for finalize)"

# --- 2. identity: slotadmin + acceptance pubkey ------------------------------
grep -q 'username: slotadmin' "$ud" && ok || fail "slotadmin user missing"
grep -q 'hostname: multica-slot-base' "$ud" && ok || fail "hostname missing"
grep -q 'slot-acceptance-test' "$ud" && ok || fail "acceptance pubkey not embedded"

# --- 3. runtime packages present (PostgreSQL + KVP + debug tools) -----------
for pkg in linux-cloud-tools-virtual linux-tools-virtual postgresql postgresql-client jq curl unzip git lsof; do
  grep -qE "^    - $pkg\$" "$ud" && ok || fail "package $pkg missing from packages list"
done

# --- 4. node runtime via NodeSource, version pinned --------------------------
grep -q "setup_${NODE_MAJOR:-22}\.x" "$ud" && ok || fail "NodeSource setup_22.x missing"

# --- 5. NO build toolchain (negative assertions pin the 方案 boundary) -------
for bad in build-essential gcc make dkms linux-headers; do
  grep -qE "$bad" "$ud" && fail "build toolchain leaked into seed: $bad" || ok
done

# --- 6. sudoers + fstrim ------------------------------------------------------
grep -q "slotadmin ALL=(ALL) NOPASSWD:ALL" "$ud" && ok || fail "NOPASSWD sudoers missing"
grep -q 'systemctl enable fstrim.timer' "$ud" && ok || fail "fstrim.timer enable missing"

# --- 7. guest scripts embedded byte-identical (base64 late-commands) ---------
for g in guard-scan.sh artifact-install.sh slot-inventory.sh; do
  want="$(base64 -w0 "$here/guest/$g")"
  got="$(sed -n "s/.*echo '\([A-Za-z0-9+/=]*\)' | base64 -d > \/usr\/local\/lib\/multica\/$g .*/\1/p" "$ud")"
  if [ -n "$got" ] && [ "$got" = "$want" ]; then ok; else fail "guest script $g not embedded byte-identical"; fi
done

# --- 8. meta-data instance-id carries the image version ---------------------
grep -q 'instance-id: multica-slot-base-2026.10.11-test' "$md" && ok || fail "meta-data instance-id missing version"

# --- 9. seed manifest: versions + script hashes pinned ----------------------
jq -e '.image == "multica-slot-base"' "$sm" >/dev/null && ok || fail "manifest image field"
jq -e '.versions.ubuntu == "24.04.5"' "$sm" >/dev/null && ok || fail "manifest ubuntu version"
jq -e '.versions.node_major == 22' "$sm" >/dev/null && ok || fail "manifest node_major"
jq -e '.versions.postgres_major == 16' "$sm" >/dev/null && ok || fail "manifest postgres_major"
jq -e --arg iso "${UBUNTU_ISO_SHA256:-97f3d7ffb032c3eb3b23d2c8be9cc76e60c2c1f2c0146ba5ba9fe01cafae0fd8}" \
  '.versions.iso_sha256 == $iso' "$sm" >/dev/null && ok || fail "manifest iso sha256"
for g in guard-scan.sh artifact-install.sh slot-inventory.sh; do
  want="$(sha256sum "$here/guest/$g" | awk '{print $1}')"
  jq -e --arg n "$g" --arg h "$want" '.guest_scripts[] | select(.name == $n) | .sha256 == $h' "$sm" >/dev/null \
    && ok || fail "manifest hash mismatch for $g"
done

# --- 10. console rescue password: generated, hashed, file gated --------------
pw_file="$seed/.console-password"
[ -f "$pw_file" ] && ok || fail "console password file missing"
[ "$(stat -c %a "$pw_file")" = "600" ] && ok || fail "console password file not 600"
grep -q '\$6\$' "$ud" && ok || fail "password hash not in user-data"
if grep -qF "$(cat "$pw_file")" "$ud"; then fail "plaintext password leaked (hash only allowed)"; else ok; fi

printf '\nbuild-seed.test.sh: %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ]
