#!/usr/bin/env bash
set -euo pipefail

ENV_FILE="${1:-.env.worktree}"

if [ -f "$ENV_FILE" ] && [ "${FORCE:-0}" != "1" ]; then
  echo "Refusing to overwrite existing $ENV_FILE. Re-run with FORCE=1 if you want to regenerate it."
  exit 1
fi

worktree_name="${WORKTREE_NAME:-$(basename "$PWD")}"
slug="$(printf '%s' "$worktree_name" | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9]/_/g; s/__*/_/g; s/^_//; s/_$//')"
if [ -z "$slug" ]; then
  slug="multica"
fi

hash_value="$(printf '%s' "$PWD" | cksum | awk '{print $1}')"
offset=$((hash_value % 1000))

# worktree 环境共享平台主库 `multica` 是显式行为（RUYI-66，Owner 方案B）：
# 库名只写这一处，不读主检出 .env 的 POSTGRES_DB——口令继承、库名固定。
# `make destroy` 据此拒绝 drop 主库；确需独占库的检出手工改写本文件的
# POSTGRES_DB 与 DATABASE_URL，销毁路径会要求 registry 与本文件一致后才删。
postgres_db="multica"
postgres_port=5432
backend_port=$((18080 + offset))

# 数据库口令必须与运行中平台（主检出）一致：compose 项目名固定为 multica，
# worktree 实例与平台共享同一个 postgres 容器与角色，口令不一致会导致
# 平台后端认证失败（确保脚本或人工会反复 ALTER 口令，形成“密码被篡改”）。
#
# 仅链接 worktree（git-dir 是主检出 .git/worktrees/* 子目录）强制继承；
# 独立检出（CI、普通 clone）没有共享栈也无主检出可继承，回落上游自足
# 行为生成默认弱口令，保证无 .env 环境可运行（RUYI-75）。
git_dir="$(git rev-parse --path-format=absolute --git-dir 2>/dev/null || true)"
common_dir="$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null || true)"
main_env="$(printf '%s' "$common_dir" | sed 's|/\.git$||')/.env"
if [ -n "$common_dir" ] && [ -f "$main_env" ]; then
  POSTGRES_PASSWORD="$(sed -n 's/^POSTGRES_PASSWORD=//p' "$main_env" | head -n 1)"
fi
if [ -z "${POSTGRES_PASSWORD:-}" ]; then
  if [ -n "$git_dir" ] && [ "$git_dir" != "$common_dir" ]; then
    echo "错误：无法从主检出 $main_env 读取 POSTGRES_PASSWORD，拒绝生成弱口令环境文件" >&2
    exit 1
  fi
  POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-multica}"
fi
frontend_port=$((13000 + offset))
frontend_origin="http://localhost:${frontend_port}"

cat > "$ENV_FILE" <<EOF
POSTGRES_DB=${postgres_db}
POSTGRES_USER=multica
POSTGRES_PASSWORD=${POSTGRES_PASSWORD}
POSTGRES_PORT=${postgres_port}
DATABASE_URL=postgres://multica:${POSTGRES_PASSWORD}@localhost:${postgres_port}/${postgres_db}?sslmode=disable

PORT=${backend_port}
JWT_SECRET=change-me-in-production
MULTICA_DEV_VERIFICATION_CODE=888888
MULTICA_SERVER_URL=ws://localhost:${backend_port}/ws
MULTICA_PUBLIC_URL=http://localhost:${backend_port}
MULTICA_APP_URL=${frontend_origin}

GOOGLE_CLIENT_ID=
GOOGLE_CLIENT_SECRET=
GOOGLE_REDIRECT_URI=${frontend_origin}/auth/callback

FRONTEND_PORT=${frontend_port}
FRONTEND_ORIGIN=${frontend_origin}
NEXT_PUBLIC_API_URL=http://localhost:${backend_port}
NEXT_PUBLIC_WS_URL=ws://localhost:${backend_port}/ws
EOF

echo "Generated $ENV_FILE for worktree '$worktree_name'"
echo "  Shared Postgres: localhost:${postgres_port}"
echo "  Database: ${postgres_db}"
echo "  Backend:  http://localhost:${backend_port}"
echo "  Frontend: ${frontend_origin}"
echo ""
echo "Next steps:"
echo "  make setup-worktree"
echo "  make start-worktree"
