#!/usr/bin/env bash
# 开发测试版本号生成器（RUYI-573 版本号体系，全仓 workflow 共用）。
#
# 版本规范：
#   正式版 X.Y.Z                —— 来自 release tag，不带任何后缀
#   开发版 X.Y.Z-dev.YYYYMMDD-N —— N 为当天该版本的开发发版序号，从 1 递增
#
# 序号来源：GitHub Releases 已有的 `<prefix><base>-dev.<date>-N` 形态 tag，
# 当天最大 N + 1。调用方 workflow 用串行并发组（cancel-in-progress: false）
# 保证两个构建不会同时推号；发布前再用 check-tag 复核，tag 已被占用即失败
# ——宁可响报，也不产出与已有 Release 冲突的版本。
#
# 用法：
#   dev-version.sh next --base 0.2.0 --date 20261008 --prefix mobile-android-dev-v
#     stdout 输出 GITHUB_OUTPUT 友好的 kv：
#       version=0.2.0-dev.20261008-1
#       tag=mobile-android-dev-v0.2.0-dev.20261008-1
#     过程日志走 stderr。
#   dev-version.sh check-tag --tag mobile-android-dev-v0.2.0-dev.20261008-1
#     该 tag（或同名 Release）已存在 → exit 1；空闲 → exit 0。
#
# 仓库取值：默认 $GITHUB_REPOSITORY（Actions 内自动就绪），可用 --repo 覆盖；
# 只读 API 需要 GH_TOKEN（workflow 内传 github.token）。
#
# 纯逻辑（compose_version / next_sequence / 校验）与取数
# （collect_release_tags / tag_exists）分离，scripts/dev-version.test.sh
# 通过 source + 函数覆盖离线测试。
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
用法:
  dev-version.sh next --base X.Y.Z --date YYYYMMDD --prefix <tag-prefix> [--repo OWNER/REPO]
  dev-version.sh check-tag --tag <tag> [--repo OWNER/REPO]
EOF
  exit 2
}

die() {
  echo "::error::$1" >&2
  exit 1
}

# 只允许安全字符，防止前缀/base 里的元字符污染下游正则与 tag 名。
validate_base() {
  [[ "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "base 版本必须是 X.Y.Z 三段数字，得到 '$1'"
}

validate_date() {
  [[ "$1" =~ ^[0-9]{8}$ ]] || die "date 必须是 YYYYMMDD 八位数字，得到 '$1'"
}

validate_prefix() {
  [[ "$1" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || die "prefix 只允许字母数字与 . _ -，得到 '$1'"
}

validate_version() {
  [[ "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+-dev\.[0-9]{8}-[0-9]+$ ]] ||
    die "开发版本必须是 X.Y.Z-dev.YYYYMMDD-N，得到 '$1'"
}

validate_tag() {
  [[ "$1" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || die "tag 名包含不安全字符：'$1'"
}

# 转义正则元字符（前缀含 . 与 - 时必需）。
escape_re() {
  printf '%s' "$1" | sed -e 's/[][\.*^$+?()|{}/\\]/\\&/g'
}

# 纯函数：拼版本号与 tag。
compose_version() {
  local base=$1 date=$2 n=$3
  printf '%s-dev.%s-%s' "$base" "$date" "$n"
}

expected_tag() {
  local prefix=$1 base=$2 date=$3 n=$4
  printf '%s%s-dev.%s-%s' "$prefix" "$base" "$date" "$n"
}

# 从 stdin 的 tag 行里算出下一个当天序号：同前缀 + 同 base + 同日期的最大 N + 1，
# 没有则 1。其余通道/日期/版本的 tag 全部忽略。
next_sequence() {
  local base=$1 date=$2 prefix=$3
  local re
  re="^$(escape_re "$prefix")$(escape_re "$base")-dev\.${date}-([0-9]+)$"
  local max=0 n tag
  while IFS= read -r tag; do
    [[ "$tag" =~ $re ]] || continue
    n="${BASH_REMATCH[1]}"
    n=$((10#$n))
    (( n > max )) && max=$n
  done
  printf '%s' "$((max + 1))"
}

# 取数（impure）：列出仓库最近的 Release tag。每天的开发构建数量远小于
# 一页 100，不做翻页；超过时 check-tag 兜底。
collect_release_tags() {
  local repo=$1
  gh api "repos/${repo}/releases?per_page=100" -q '.[].tag_name'
}

# 取数（impure）：tag 是否已存在（含无 Release 的裸 tag）。
tag_exists() {
  local repo=$1 tag=$2
  gh api "repos/${repo}/git/ref/tags/${tag}" >/dev/null 2>&1
}

cmd_next() {
  local repo=${GITHUB_REPOSITORY:-} base= date= prefix=
  while (( $# > 0 )); do
    case "$1" in
      --base) base=$2; shift 2 ;;
      --date) date=$2; shift 2 ;;
      --prefix) prefix=$2; shift 2 ;;
      --repo) repo=$2; shift 2 ;;
      *) usage ;;
    esac
  done
  [[ -n "$repo" ]] || die "需要 --repo 或 GITHUB_REPOSITORY"
  validate_base "$base"
  validate_date "$date"
  validate_prefix "$prefix"

  local tags n version tag
  tags=$(collect_release_tags "$repo")
  n=$(next_sequence "$base" "$date" "$prefix" <<<"$tags")
  version=$(compose_version "$base" "$date" "$n")
  tag=$(expected_tag "$prefix" "$base" "$date" "$n")
  # 同批列表内已出现同名 tag（理论上被串行并发组排除）则顺延，最多 50 次。
  local i
  for ((i = 0; i < 50; i++)); do
    grep -qx "$tag" <<<"$tags" || break
    n=$((n + 1))
    version=$(compose_version "$base" "$date" "$n")
    tag=$(expected_tag "$prefix" "$base" "$date" "$n")
  done
  grep -qx "$tag" <<<"$tags" && die "50 次顺延后仍与已有 tag 冲突：$tag"
  validate_version "$version"

  echo "当天序号 N=$n" >&2
  printf 'version=%s\n' "$version"
  printf 'tag=%s\n' "$tag"
}

cmd_check_tag() {
  local repo=${GITHUB_REPOSITORY:-} tag=
  while (( $# > 0 )); do
    case "$1" in
      --tag) tag=$2; shift 2 ;;
      --repo) repo=$2; shift 2 ;;
      *) usage ;;
    esac
  done
  [[ -n "$repo" && -n "$tag" ]] || die "check-tag 需要 --tag 与 --repo 或 GITHUB_REPOSITORY"
  validate_tag "$tag"
  if tag_exists "$repo" "$tag"; then
    die "tag '$tag' 已存在，拒绝生成重复版本（同日重跑请使用新的序号或先清理旧 Release）"
  fi
  echo "tag '$tag' 空闲" >&2
}

main() {
  local cmd=${1:-}
  shift || true
  case "$cmd" in
    next) cmd_next "$@" ;;
    check-tag) cmd_check_tag "$@" ;;
    *) usage ;;
  esac
}

# 供测试 source：只有直接执行才进入 main。
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  main "$@"
fi
