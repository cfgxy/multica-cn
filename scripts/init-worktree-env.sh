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

postgres_db="multica_${slug}_${offset}"
postgres_port=5432
backend_port=$((18080 + offset))

# 数据库口令必须与运行中平台（主检出）一致：compose 项目名固定为 multica，
# worktree 实例与平台共享同一个 postgres 容器与角色，口令不一致会导致
# 平台后端认证失败（确保脚本或人工会反复 ALTER 口令，形成“密码被篡改”）。
main_env="$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null | sed 's|/\.git$||')/.env"
if [ -f "$main_env" ]; then
  POSTGRES_PASSWORD="$(grep -E '^POSTGRES_PASSWORD=' "$main_env" | head -1 | cut -d= -f2-)"
fi
if [ -z "${POSTGRES_PASSWORD:-}" ]; then
  echo "错误：无法从主检出 $main_env 读取 POSTGRES_PASSWORD，拒绝生成弱口令环境文件" >&2
  exit 1
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
