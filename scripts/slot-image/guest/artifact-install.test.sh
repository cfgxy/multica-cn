#!/usr/bin/env bash
# artifact-install.test.sh — startup unpack/load mechanism (plan §3.7):
# mount dir → sha256 verify (fail-fast, nothing touched) → atomic unpack
# (temp dir + rename) → idempotent re-run. Decompression failure is an
# ENV-FAULT: the deploy dir must never hold a half-deployed state.
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
install_sh="$root_dir/scripts/slot-image/guest/artifact-install.sh"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

PASS=0; FAIL=0
fail() { printf 'FAIL: %s\n' "$1" >&2; FAIL=$((FAIL + 1)); }
ok()   { PASS=$((PASS + 1)); }

make_zip() { # <zip-path> <content-dir>
  (cd "$2" && zip -qr "$1" .)
}
sha_of_file() { sha256sum "$1" | awk '{print $1}'; }
tree_sha() { # stable hash of a tree's content (paths + file hashes)
  (cd "$1" && find . -type f ! -path './.incoming*' -print0 | sort -z | \
    xargs -0 sha256sum | sha256sum | awk '{print $1}')
}

# --- 1. happy path: verify + unpack + rename ---------------------------------
mount="$tmp_dir/m1"; deploy="$tmp_dir/deploy1"
mkdir -p "$mount/src" "$deploy"
echo '<html>hello</html>' >"$mount/src/index.html"
make_zip "$mount/web-dist.zip" "$mount/src"
good_sha="$(sha_of_file "$mount/web-dist.zip")"
cat >"$mount/manifest.json" <<EOF
{"artifacts":[{"name":"web-dist.zip","sha256":"$good_sha","target":"web"}]}
EOF
out="$(bash "$install_sh" "$mount" "$mount/manifest.json" "$deploy")"; rc=$?
if [ "$rc" = 0 ] && [ -f "$deploy/web/index.html" ] \
   && grep -q "installed: web-dist.zip -> web" <<<"$out"; then ok; else
  fail "happy path rc=$rc out=$out"; fi
if [ -z "$(find "$deploy" -maxdepth 1 -name '.incoming*' -o -maxdepth 1 -name '.trash*')" ]; then ok; else
  fail "happy path left temp dirs in deploy"; fi

# --- 2. sha mismatch: fail-fast, deploy untouched ----------------------------
deploy="$tmp_dir/deploy2"; mount="$tmp_dir/m2"
mkdir -p "$mount/src" "$deploy"
echo one >"$mount/src/f.txt"; make_zip "$mount/pkg.zip" "$mount/src"
sha="$(sha_of_file "$mount/pkg.zip")"
printf '{"artifacts":[{"name":"pkg.zip","sha256":"%s","target":"app"}]}' \
  "$(printf '%s' "$sha" | tr '0-9a-f' '1-9af0b-cdef' )" >"$mount/manifest.json"
out="$(bash "$install_sh" "$mount" "$mount/manifest.json" "$deploy" 2>&1)" || rc=$?
rc=0; out="$(bash "$install_sh" "$mount" "$mount/manifest.json" "$deploy" 2>&1)" || rc=$?
if [ "$rc" != 0 ] && [ -z "$(ls -A "$deploy")" ] \
   && grep -q 'sha256 mismatch' <<<"$out"; then ok; else
  fail "sha mismatch: rc=$rc deploy='$(ls -A "$deploy")' out=$out"; fi

# --- 3. missing artifact file: fail-fast -------------------------------------
deploy="$tmp_dir/deploy3"; mount="$tmp_dir/m3"
mkdir -p "$mount" "$deploy"
printf '{"artifacts":[{"name":"absent.zip","sha256":"%s","target":"app"}]}' \
  "$(printf 'a%.0s' {1..64})" >"$mount/manifest.json"
rc=0; out="$(bash "$install_sh" "$mount" "$mount/manifest.json" "$deploy" 2>&1)" || rc=$?
if [ "$rc" != 0 ] && [ -z "$(ls -A "$deploy")" ] && grep -q 'missing' <<<"$out"; then ok; else
  fail "missing artifact: rc=$rc out=$out"; fi

# --- 4. idempotent: same inputs, same tree -----------------------------------
deploy="$tmp_dir/deploy4"; mount="$tmp_dir/m4"
mkdir -p "$mount/src" "$deploy"
printf 'v1\n' >"$mount/src/app.js"; make_zip "$mount/a.zip" "$mount/src"
cat >"$mount/manifest.json" <<EOF
{"artifacts":[{"name":"a.zip","sha256":"$(sha_of_file "$mount/a.zip")","target":"app"}]}
EOF
bash "$install_sh" "$mount" "$mount/manifest.json" "$deploy" >/dev/null
s1="$(tree_sha "$deploy")"
bash "$install_sh" "$mount" "$mount/manifest.json" "$deploy" >/dev/null
s2="$(tree_sha "$deploy")"
if [ -n "$s1" ] && [ "$s1" = "$s2" ]; then ok; else fail "idempotent: $s1 != $s2"; fi

# --- 5. re-run with new artifact replaces content ----------------------------
printf 'v2\n' >"$mount/src/app.js"; make_zip "$mount/a.zip" "$mount/src"
cat >"$mount/manifest.json" <<EOF
{"artifacts":[{"name":"a.zip","sha256":"$(sha_of_file "$mount/a.zip")","target":"app"}]}
EOF
bash "$install_sh" "$mount" "$mount/manifest.json" "$deploy" >/dev/null
if [ "$(cat "$deploy/app/app.js")" = "v2" ]; then ok; else fail "replace: content not updated"; fi

# --- 6. multi-artifact: one bad entry installs nothing -----------------------
deploy="$tmp_dir/deploy6"; mount="$tmp_dir/m6"
mkdir -p "$mount/s1" "$mount/s2" "$deploy"
echo A >"$mount/s1/a.txt"; echo B >"$mount/s2/b.txt"
make_zip "$mount/one.zip" "$mount/s1"; make_zip "$mount/two.zip" "$mount/s2"
bad="$(printf '%s' "$(sha_of_file "$mount/two.zip")" | tr '0-9a-f' '1-9af0b-cdef')"
cat >"$mount/manifest.json" <<EOF
{"artifacts":[{"name":"one.zip","sha256":"$(sha_of_file "$mount/one.zip")","target":"one"},
              {"name":"two.zip","sha256":"$bad","target":"two"}]}
EOF
rc=0; out="$(bash "$install_sh" "$mount" "$mount/manifest.json" "$deploy" 2>&1)" || rc=$?
if [ "$rc" != 0 ] && [ -z "$(ls -A "$deploy")" ]; then ok; else
  fail "multi verify-first: rc=$rc deploy='$(ls -A "$deploy")'"; fi

# --- 7. nested target path is created ----------------------------------------
deploy="$tmp_dir/deploy7"; mount="$tmp_dir/m7"
mkdir -p "$mount/src" "$deploy"
echo x >"$mount/src/i.js"; make_zip "$mount/w.zip" "$mount/src"
cat >"$mount/manifest.json" <<EOF
{"artifacts":[{"name":"w.zip","sha256":"$(sha_of_file "$mount/w.zip")","target":"srv/web/dist"}]}
EOF
rc=0; bash "$install_sh" "$mount" "$mount/manifest.json" "$deploy" >/dev/null 2>&1 || rc=$?
if [ "$rc" = 0 ] && [ -f "$deploy/srv/web/dist/i.js" ]; then ok; else
  fail "nested target rc=$rc"; fi

# --- 8. corrupt zip: fail-fast -----------------------------------------------
deploy="$tmp_dir/deploy8"; mount="$tmp_dir/m8"
mkdir -p "$mount" "$deploy"
head -c 2048 /dev/urandom >"$mount/bad.zip"
cat >"$mount/manifest.json" <<EOF
{"artifacts":[{"name":"bad.zip","sha256":"$(sha_of_file "$mount/bad.zip")","target":"app"}]}
EOF
# sha matches so verification passes; unpack itself must fail without half state
rc=0; out="$(bash "$install_sh" "$mount" "$mount/manifest.json" "$deploy" 2>&1)" || rc=$?
if [ "$rc" != 0 ] && [ -z "$(ls -A "$deploy")" ]; then ok; else
  fail "corrupt zip: rc=$rc deploy='$(ls -A "$deploy")'"; fi

# --- 9. size_bytes mismatch: fail-fast ---------------------------------------
deploy="$tmp_dir/deploy9"; mount="$tmp_dir/m9"
mkdir -p "$mount/src" "$deploy"
echo data >"$mount/src/d.txt"; make_zip "$mount/d.zip" "$mount/src"
cat >"$mount/manifest.json" <<EOF
{"artifacts":[{"name":"d.zip","sha256":"$(sha_of_file "$mount/d.zip")","size_bytes":99999,"target":"app"}]}
EOF
rc=0; out="$(bash "$install_sh" "$mount" "$mount/manifest.json" "$deploy" 2>&1)" || rc=$?
if [ "$rc" != 0 ] && grep -q 'size mismatch' <<<"$out"; then ok; else
  fail "size mismatch: rc=$rc out=$out"; fi

# --- 10. failed load leaves no deploy shell when deploy dir was absent --------
deploy="$tmp_dir/deploy10"; mount="$tmp_dir/m10"
mkdir -p "$mount/src"
echo one >"$mount/src/f.txt"; make_zip "$mount/pkg.zip" "$mount/src"
printf '{"artifacts":[{"name":"pkg.zip","sha256":"%s","target":"app"}]}' \
  "$(printf '%s' "$(sha_of_file "$mount/pkg.zip")" | tr '0-9a-f' '1-9af0b-cdef')" >"$mount/manifest.json"
rc=0; out="$(bash "$install_sh" "$mount" "$mount/manifest.json" "$deploy" 2>&1)" || rc=$?
shell_state="absent"; [ -e "$deploy" ] && shell_state="present"
if [ "$rc" != 0 ] && [ "$shell_state" = "absent" ] \
   && grep -q 'sha256 mismatch' <<<"$out"; then ok; else
  fail "no deploy shell: rc=$rc shell=$shell_state out=$out"; fi

printf '\nartifact-install.test.sh: %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ]
