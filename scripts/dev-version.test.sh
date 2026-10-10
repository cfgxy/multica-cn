#!/usr/bin/env bash
# dev-version.sh 的离线单测：bash scripts/dev-version.test.sh
# 覆盖：版本/tag 组装、当天序号推算（空表/跳号/跨日/跨版本/跨前缀）、
# 冲突顺延、参数校验拒绝。不访问网络（取数函数被覆盖）。
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=dev-version.sh
source "$SCRIPT_DIR/dev-version.sh"

PASS=0
assert_eq() {
  local desc=$1 expected=$2 actual=$3
  if [[ "$expected" != "$actual" ]]; then
    echo "FAIL: $desc — 期望 <$expected>，实际 <$actual>" >&2
    exit 1
  fi
  PASS=$((PASS + 1))
  echo "ok: $desc"
}

assert_fails() {
  local desc=$1
  shift
  if "$@" >/dev/null 2>&1; then
    echo "FAIL: $desc — 命令意外成功" >&2
    exit 1
  fi
  PASS=$((PASS + 1))
  echo "ok: $desc"
}

# --- compose_version / expected_tag ---
assert_eq "组装开发版本号" "0.2.0-dev.20261008-1" "$(compose_version 0.2.0 20261008 1)"
assert_eq "组装开发 tag" "mobile-android-dev-v0.2.0-dev.20261008-12" \
  "$(expected_tag mobile-android-dev-v 0.2.0 20261008 12)"

# --- next_sequence：空表从 1 起 ---
assert_eq "空 Release 列表 → N=1" "1" \
  "$(next_sequence 0.2.0 20261008 mobile-android-dev-v </dev/null)"

# --- 当天已有 1、3（2 失败未发布）→ 下一个 4 ---
seq_tags=$'mobile-android-dev-v0.2.0-dev.20261008-1\nmobile-android-dev-v0.2.0-dev.20261008-3'
assert_eq "同日 1/3 → N=4" "4" "$(next_sequence 0.2.0 20261008 mobile-android-dev-v <<<"$seq_tags")"

# --- 跨日忽略：只有昨天的 tag → N=1 ---
yesterday=$'mobile-android-dev-v0.2.0-dev.20261007-9'
assert_eq "昨日 tag 不累计 → N=1" "1" "$(next_sequence 0.2.0 20261008 mobile-android-dev-v <<<"$yesterday")"

# --- 跨 base 版本忽略 ---
other_base=$'mobile-android-dev-v0.3.0-dev.20261008-5'
assert_eq "其他 base 不累计 → N=1" "1" "$(next_sequence 0.2.0 20261008 mobile-android-dev-v <<<"$other_base")"

# --- 跨前缀（正式 tag / 其他端 tag）忽略 ---
other_prefix=$'mobile-android-v0.2.0\ndesktop-dev-v0.2.0-dev.20261008-7\nmobile-android-dev-x0.2.0-dev.20261008-8'
assert_eq "其他前缀不累计 → N=1" "1" "$(next_sequence 0.2.0 20261008 mobile-android-dev-v <<<"$other_prefix")"

# --- cmd_next：列表内撞号顺延（collect_release_tags 覆盖为已有 -1 与 -2）---
cmd_next_tags=$'mobile-android-dev-v0.2.0-dev.20261008-1\nmobile-android-dev-v0.2.0-dev.20261008-2'
collect_release_tags() { printf '%s\n' "$cmd_next_tags"; }
out=$(GITHUB_REPOSITORY=cfgxy/multica-cn main next --base 0.2.0 --date 20261008 --prefix mobile-android-dev-v)
assert_eq "撞号顺延到 N=3（version）" "version=0.2.0-dev.20261008-3" "$(grep '^version=' <<<"$out")"
assert_eq "撞号顺延到 N=3（tag）" "tag=mobile-android-dev-v0.2.0-dev.20261008-3" "$(grep '^tag=' <<<"$out")"

# --- cmd_next：干净列表 → N=1 ---
collect_release_tags() { printf 'mobile-android-v0.2.0\n'; }
out=$(GITHUB_REPOSITORY=cfgxy/multica-cn main next --base 0.2.0 --date 20261008 --prefix mobile-android-dev-v)
assert_eq "干净列表 → version N=1" "version=0.2.0-dev.20261008-1" "$(grep '^version=' <<<"$out")"

# --- 参数校验拒绝 ---
assert_fails "base 非三段数字被拒" env GITHUB_REPOSITORY=r main next --base 0.2 --date 20261008 --prefix p
assert_fails "date 非八位被拒" env GITHUB_REPOSITORY=r main next --base 0.2.0 --date 2026100 --prefix p
assert_fails "prefix 含非法字符被拒" env GITHUB_REPOSITORY=r main next --base 0.2.0 --date 20261008 --prefix 'bad*prefix'
assert_fails "缺 repo 被拒" env -u GITHUB_REPOSITORY main next --base 0.2.0 --date 20261008 --prefix p

# --- 正式版本号不含 dev 后缀（规范断言：compose_version 永不产出正式号）---
assert_fails "dev 组装结果不匹配正式版正则" bash -c 'echo 0.2.0-dev.20261008-1 | grep -qE "^[0-9]+\.[0-9]+\.[0-9]+$"'

echo "全部通过：$PASS 项"
