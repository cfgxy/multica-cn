.PHONY: help makehelp dev server daemon cli multica build test migrate-up migrate-down sqlc seed clean setup start stop check worktree-env setup-main start-main stop-main check-main setup-worktree start-worktree stop-worktree check-worktree remove-worktree agent-branches db-up db-down db-drop db-reset selfhost selfhost-build selfhost-stop up down status list destroy gc env-exec api-dev web-dev desktop-dev daemon-build daemon-install daemon-update daemon-preflight daemon-uninstall mcp-build mcp-install mcp-update mcp-status mcp-uninstall mcp-http-install mcp-http-update mcp-http-status mcp-http-uninstall

MAIN_ENV_FILE ?= .env
WORKTREE_ENV_FILE ?= .env.worktree
ENV_FILE ?= $(if $(wildcard $(MAIN_ENV_FILE)),$(MAIN_ENV_FILE),$(if $(wildcard $(WORKTREE_ENV_FILE)),$(WORKTREE_ENV_FILE),$(MAIN_ENV_FILE)))

ifneq ($(wildcard $(ENV_FILE)),)
include $(ENV_FILE)
endif

POSTGRES_DB ?= multica
POSTGRES_USER ?= multica
POSTGRES_PASSWORD ?= multica
POSTGRES_PORT ?= 5432
PORT := $(or $(BACKEND_PORT),$(API_PORT),$(SERVER_PORT),$(PORT),8080)
ifeq ($(origin MULTICA_PUBLIC_URL), undefined)
MULTICA_PUBLIC_URL := http://localhost:$(PORT)
endif
FRONTEND_PORT ?= 3000
FRONTEND_ORIGIN ?= http://localhost:$(FRONTEND_PORT)
MULTICA_APP_URL ?= $(FRONTEND_ORIGIN)
DATABASE_URL ?= postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@localhost:$(POSTGRES_PORT)/$(POSTGRES_DB)?sslmode=disable
NEXT_PUBLIC_API_URL ?= http://localhost:$(PORT)
NEXT_PUBLIC_WS_URL ?= ws://localhost:$(PORT)/ws
GOOGLE_REDIRECT_URI ?= $(FRONTEND_ORIGIN)/auth/callback
MULTICA_SERVER_URL ?= ws://localhost:$(PORT)/ws
LOCAL_UPLOAD_BASE_URL ?= http://localhost:$(PORT)

export

MULTICA_ARGS ?= $(ARGS)

COMPOSE := docker compose

define REQUIRE_ENV
	@if [ ! -f "$(ENV_FILE)" ]; then \
		echo "Missing env file: $(ENV_FILE)"; \
		echo "Create .env from .env.example, or run 'make worktree-env' and use .env.worktree."; \
		exit 1; \
	fi
endef

# Self-hosting requires the Docker Compose CLI plugin (`docker compose`).
# The self-host compose files use compose-spec syntax (top-level `name:`, no
# `version:`) that the legacy v1 `docker-compose` standalone cannot parse, so we
# fail early with an actionable message instead of a cryptic CLI parse error
# (e.g. "unknown shorthand flag: 'f' in -f") when the plugin is missing or v1.
# Keep the message short and OS-agnostic: per-OS install steps belong in docs.
define REQUIRE_COMPOSE
	@if ! compose_version=$$($(COMPOSE) version --short 2>/dev/null); then \
		echo "Docker Compose ('docker compose') was not found."; \
		echo "Self-hosting requires the Compose CLI plugin; legacy 'docker-compose' v1 is not supported."; \
		echo "Install Docker Compose from https://docs.docker.com/compose/install/ and verify with: docker compose version"; \
		exit 1; \
	fi; \
	case "$$compose_version" in \
		1.*|v1.*) \
			echo "'$(COMPOSE)' is legacy Docker Compose v1 ($$compose_version)."; \
			echo "Self-hosting requires the Compose CLI plugin; legacy 'docker-compose' v1 is not supported."; \
			echo "Install Docker Compose from https://docs.docker.com/compose/install/ and verify with: docker compose version"; \
			exit 1; \
			;; \
	esac
endef

# Default target changed from selfhost to help: bare `make` now prints this help
# instead of launching a full Docker Compose build, which is safer for onboarding.
.DEFAULT_GOAL := help

##@ Help

help: ## Show available make targets and common local workflows
	@awk 'BEGIN {FS = ":.*## "; printf "\nUsage:\n  make \033[36m<target>\033[0m\n\nQuick start:\n  \033[36mmake up\033[0m           Start this checkout'"'"'s environment (C=api,web,daemon,desktop)\n  \033[36mmake status\033[0m       Show what is running, and prove it is yours\n  \033[36mmake down\033[0m         Stop it again, keeping the database\n  \033[36mmake check\033[0m        Run the full local verification pipeline\n\nCheckout modes:\n  Main checkout uses \033[36m.env\033[0m\n  Worktrees use \033[36m.env.worktree\033[0m (generate with \033[36mmake worktree-env\033[0m)\n\n"} \
		/^##@/ {printf "\n\033[1m%s\033[0m\n", substr($$0, 5); next} \
		/^[a-zA-Z0-9_.-]+:.*## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

makehelp: help ## Alias for `make help`

# ---------- Self-hosting (Docker Compose) ----------
##@ Self-hosting

selfhost: ## Create .env if needed, then pull and start the official self-hosted images
	$(REQUIRE_COMPOSE)
	@if [ ! -f .env ]; then \
		echo "==> Creating .env from .env.example..."; \
		cp .env.example .env; \
		JWT=$$(openssl rand -hex 32); \
		PGPASS=$$(openssl rand -hex 24); \
		VCSKEY=$$(openssl rand -base64 32); \
		if [ "$$(uname)" = "Darwin" ]; then \
			sed -i '' "s/^JWT_SECRET=.*/JWT_SECRET=$$JWT/" .env; \
			sed -i '' "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=$$PGPASS/" .env; \
			sed -i '' -E "s#^(DATABASE_URL=postgres://[^:]+:)[^@]*(@.*)#\1$$PGPASS\2#" .env; \
			sed -i '' "s#^MULTICA_VCS_SECRET_KEY=.*#MULTICA_VCS_SECRET_KEY=$$VCSKEY#" .env; \
		else \
			sed -i "s/^JWT_SECRET=.*/JWT_SECRET=$$JWT/" .env; \
			sed -i "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=$$PGPASS/" .env; \
			sed -i -E "s#^(DATABASE_URL=postgres://[^:]+:)[^@]*(@.*)#\1$$PGPASS\2#" .env; \
			sed -i "s#^MULTICA_VCS_SECRET_KEY=.*#MULTICA_VCS_SECRET_KEY=$$VCSKEY#" .env; \
		fi; \
		echo "==> Generated random JWT_SECRET, POSTGRES_PASSWORD, and MULTICA_VCS_SECRET_KEY"; \
	fi
	@echo "==> Pulling official Multica images..."
	@if ! $(COMPOSE) -f docker-compose.selfhost.yml pull; then \
		echo ""; \
		echo "Official images for tag '$${MULTICA_IMAGE_TAG:-latest}' are not published yet."; \
		echo "If this is before the first GHCR release, build from the current checkout:"; \
		echo "  make selfhost-build"; \
		exit 1; \
	fi
	@echo "==> Starting Multica via Docker Compose..."
	$(COMPOSE) -f docker-compose.selfhost.yml up -d
	@bash scripts/selfhost-wait.sh official

selfhost-build: ## Build backend/web from the current checkout and start the self-hosted stack
	$(REQUIRE_COMPOSE)
	@if [ ! -f .env ]; then \
		echo "==> Creating .env from .env.example..."; \
		cp .env.example .env; \
		JWT=$$(openssl rand -hex 32); \
		PGPASS=$$(openssl rand -hex 24); \
		VCSKEY=$$(openssl rand -base64 32); \
		if [ "$$(uname)" = "Darwin" ]; then \
			sed -i '' "s/^JWT_SECRET=.*/JWT_SECRET=$$JWT/" .env; \
			sed -i '' "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=$$PGPASS/" .env; \
			sed -i '' -E "s#^(DATABASE_URL=postgres://[^:]+:)[^@]*(@.*)#\1$$PGPASS\2#" .env; \
			sed -i '' "s#^MULTICA_VCS_SECRET_KEY=.*#MULTICA_VCS_SECRET_KEY=$$VCSKEY#" .env; \
		else \
			sed -i "s/^JWT_SECRET=.*/JWT_SECRET=$$JWT/" .env; \
			sed -i "s/^POSTGRES_PASSWORD=.*/POSTGRES_PASSWORD=$$PGPASS/" .env; \
			sed -i -E "s#^(DATABASE_URL=postgres://[^:]+:)[^@]*(@.*)#\1$$PGPASS\2#" .env; \
			sed -i "s#^MULTICA_VCS_SECRET_KEY=.*#MULTICA_VCS_SECRET_KEY=$$VCSKEY#" .env; \
		fi; \
		echo "==> Generated random JWT_SECRET, POSTGRES_PASSWORD, and MULTICA_VCS_SECRET_KEY"; \
	fi
	@echo "==> Building Multica from the current checkout..."
	$(COMPOSE) -f docker-compose.selfhost.yml -f docker-compose.selfhost.build.yml up -d --build
	@bash scripts/selfhost-wait.sh build

selfhost-stop: ## Stop the self-hosted Docker Compose stack
	$(REQUIRE_COMPOSE)
	@echo "==> Stopping Multica services..."
	$(COMPOSE) -f docker-compose.selfhost.yml down
	@echo "✓ All services stopped."

# ---------- Daemon (systemd service on this host) ----------
##@ Daemon (systemd)

daemon-build: ## Build the runtime daemon CLI with release version metadata
	cd server && go build -ldflags "-X main.version=$$(git tag -l 'v[0-9]*' --sort=-v:refname | head -1 | sed 's/^v//') -X main.commit=$$(git rev-parse --short HEAD) -X main.date=$$(date -u '+%Y-%m-%dT%H:%M:%SZ')" -o bin/multica ./cmd/multica

# 部署参数：以「执行 make 的普通用户」为 daemon 运行用户（内部自动 sudo，勿用 sudo make）。
# 换机器按规格覆盖：make daemon-install CPU_QUOTA=400% MEMORY_HIGH=8G MEMORY_MAX=12G
# PROFILE 决定 daemon 读取的 multica 配置（systemd 实例名即 profile 名）：
#   make daemon-install                → multica-daemon@default，读 ~/.multica/
#   make daemon-install PROFILE=idata  → multica-daemon@idata，读 ~/.multica/profiles/idata/
PROFILE ?= default
CPU_QUOTA ?= 600%
MEMORY_HIGH ?= 24G
MEMORY_MAX ?= 28G
# 运行用户/组：以执行 make 的普通用户为准（make 层展开，避免 shell 单引号吞掉命令替换）
USER ?= $(shell id -un)
GROUP ?= $(shell id -gn)

# Daemon 统一纳管为 systemd 模板实例 multica-daemon@<profile>，实例名即 profile 名。
# 实例名 default 是保留字（模板内特判为不带 --profile 的默认 profile），因此旧机器上
# 的非模板 multica-daemon.service 由 PROFILE=default 安装自动迁移：disable 并删除旧
# 单元 → enable multica-daemon@default；状态目录与 daemon.id 不变，服务端视角是
# 同一个 daemon。多个连接 = 逐个 profile 各跑一次。
daemon-install: daemon-build ## Install daemon as systemd instance: make daemon-install [PROFILE=idata] (default profile when PROFILE omitted)
	@if [ -n "$$SUDO_USER" ]; then echo "ERROR: run 'make daemon-install' as the regular user (sudo is invoked internally)"; exit 1; fi
	@test -f ~/.bashrc || echo "WARN: ~/.bashrc 不存在，daemon 将缺少登录环境"
	@if [ "$(PROFILE)" = default ]; then \
		test -f $(HOME)/.multica/config.json || { echo "ERROR: default profile is not authenticated — run 'multica login' first"; exit 1; }; \
	else \
		test -f $(HOME)/.multica/profiles/$(PROFILE)/config.json || { echo "ERROR: profile '$(PROFILE)' is not authenticated — run 'multica --profile $(PROFILE) login' first"; exit 1; }; \
	fi
	install -m755 server/bin/multica $(HOME)/.local/bin/multica
	@if [ "$(PROFILE)" = default ]; then \
		sudo systemctl disable --now multica-daemon.service >/dev/null 2>&1 || true; \
		sudo rm -f /etc/systemd/system/multica-daemon.service; \
		$(HOME)/.local/bin/multica daemon stop >/dev/null 2>&1 || true; \
	else \
		$(HOME)/.local/bin/multica daemon stop --profile $(PROFILE) >/dev/null 2>&1 || true; \
	fi
	sed -e 's|@USER@|$(USER)|g' \
	    -e 's|@GROUP@|$(GROUP)|g' \
	    -e 's|@HOME@|$(HOME)|g' \
	    -e 's|@BIN@|$(HOME)/.local/bin/multica|g' \
	    -e 's|@CPU_QUOTA@|$(CPU_QUOTA)|g' \
	    -e 's|@MEMORY_HIGH@|$(MEMORY_HIGH)|g' \
	    -e 's|@MEMORY_MAX@|$(MEMORY_MAX)|g' \
	    deploy/multica-daemon@.service.template > /tmp/multica-daemon@.service
	sudo install -m644 /tmp/multica-daemon@.service /etc/systemd/system/multica-daemon@.service
	sudo install -m644 deploy/multica-oom-guard.service /etc/systemd/system/multica-oom-guard.service
	sudo install -m755 deploy/oom-guard.sh /usr/local/sbin/multica-oom-guard.sh
	sudo systemctl daemon-reload
	sudo systemctl enable --now multica-oom-guard.service
	sudo systemctl enable --now multica-daemon@$(PROFILE).service
	sudo systemctl restart multica-oom-guard.service
	@systemctl --no-pager --lines=0 status multica-daemon@$(PROFILE).service
	@bash deploy/oom-preflight.sh || true
	@echo "daemon profile $(PROFILE) → multica-daemon@$(PROFILE)；日志: multica daemon logs$(if $(filter default,$(PROFILE)),, --profile $(PROFILE))"

daemon-preflight: ## Read-only check of kernel/system prerequisites for the OOM guard
	@bash deploy/oom-preflight.sh

daemon-update: daemon-build ## Update the daemon binary only, then graceful restart of multica-daemon@<PROFILE> (run once per installed profile)
	install -m755 server/bin/multica $(HOME)/.local/bin/multica
	sudo systemctl restart multica-daemon@$(PROFILE).service
	@systemctl --no-pager --lines=0 status multica-daemon@$(PROFILE).service

# 卸载 = 只清 systemd 启动项与 oom-guard：默认 multica-daemon.service、模板
# multica-daemon@.service 及全部已启用实例、guard 脚本/单元，并顺带停掉手动
# 后台启动的 daemon。保留 ~/.multica（各 profile 的连接配置/token）、
# ~/multica_workspaces* 和 CLI 二进制；PURGE=1 额外删除 ~/.local/bin/multica。
daemon-uninstall: ## Remove daemon systemd units (default + all @profile instances) + OOM guard; keeps profiles/tokens; PURGE=1 also removes the CLI binary
	@if [ -n "$$SUDO_USER" ]; then echo "ERROR: run 'make daemon-uninstall' as the regular user (sudo is invoked internally)"; exit 1; fi
	@echo "== 停止并禁用 multica-oom-guard =="
	@sudo systemctl disable --now multica-oom-guard.service >/dev/null 2>&1 || true
	@echo "== 停止并禁用默认实例 multica-daemon.service（如已安装）=="
	@sudo systemctl disable --now multica-daemon.service >/dev/null 2>&1 || true
	@echo "== 停止并禁用全部 multica-daemon@<profile> 实例 =="
	@units="$$(systemctl list-unit-files 'multica-daemon@*.service' --no-legend 2>/dev/null | awk '{print $$1}'; \
	        ls /etc/systemd/system/multi-user.target.wants/ 2>/dev/null | grep '^multica-daemon@' || true)"; \
	for u in $$units; do sudo systemctl disable --now "$$u" >/dev/null 2>&1 || true; done
	@echo "== 停掉手动后台启动的 daemon（如有）=="
	@$(HOME)/.local/bin/multica daemon stop >/dev/null 2>&1 || true; \
	for cfg in $(HOME)/.multica/profiles/*/config.json; do \
		[ -f "$$cfg" ] || continue; \
		p=$${cfg#*/profiles/}; p=$${p%/config.json}; \
		$(HOME)/.local/bin/multica daemon stop --profile "$$p" >/dev/null 2>&1 || true; \
	done
	@echo "== 删除单元文件与 guard 脚本 =="
	@sudo rm -f /etc/systemd/system/multica-daemon.service \
	            /etc/systemd/system/multica-daemon@.service \
	            /etc/systemd/system/multica-oom-guard.service \
	            /usr/local/sbin/multica-oom-guard.sh
	@sudo systemctl daemon-reload
	@sudo systemctl reset-failed 2>/dev/null || true
	@if [ "$(PURGE)" = 1 ]; then rm -f $(HOME)/.local/bin/multica && echo "== 已删除 $(HOME)/.local/bin/multica（PURGE=1）=="; fi
	@echo "== 卸载完成 =="
	@echo "   已移除: multica-daemon.service / multica-daemon@.service 及全部实例 / multica-oom-guard / guard 脚本"
	@echo "   已保留: ~/.multica/（各 profile 连接配置与 token）、~/multica_workspaces*/$(if $(PURGE),,、CLI 二进制 ~/.local/bin/multica)"

# ---------- MCP (local stdio server, client installers) ----------
##@ MCP

mcp-build: ## Build @multica/mcp from this checkout only (tsc -> dist/), without touching any client config
	pnpm --filter @multica/mcp build

mcp-install: mcp-build ## Register @multica/mcp with every detected client (Claude Code/Codex/Kimi/ZCode/Cursor/OpenCode). Uses this checkout's absolute dist path, so re-run after moving/removing the checkout.
	node "$(CURDIR)/apps/mcp/dist/install-cli.js" "$(CURDIR)/apps/mcp/dist/index.js"

mcp-update: mcp-build ## Rebuild and refresh only clients already registered with multica (fixes a stale dist path after moving/rebuilding the checkout); never registers a newly-detected client
	node "$(CURDIR)/apps/mcp/dist/update-cli.js" "$(CURDIR)/apps/mcp/dist/index.js"

mcp-status: mcp-build ## Read-only: show which clients are registered, the dist path each points at, and whether that path is stale
	node "$(CURDIR)/apps/mcp/dist/status-cli.js"

mcp-uninstall: mcp-build ## Remove the multica entry from every detected client's config; other entries and file formatting are preserved
	node "$(CURDIR)/apps/mcp/dist/uninstall-cli.js"

# ---------- MCP HTTP (systemd service, streamable HTTP transport) ----------
# 与上面 mcp-* 的形态不同：mcp-* 把 stdio server 注册进本机 MCP 客户端配置；
# 这里是把 http transport 常驻托管为 systemd 服务，供远程 connector（ChatGPT
# 等）连接。两者互不影响，可同时存在。
#
# 无状态设计：每个 POST /mcp 自带 Authorization: Bearer <PAT>，服务端不持久化
# 任何凭据；因此本组 target 不做「profile 未认证」前置检查（对照 daemon-install），
# 单元文件与日志也绝不写入 token——凭据只走调用方的请求头。
#
# 单实例，非 @ 模板：一台机器一般只需要一个 MCP http 端点（多端口场景可用
# MCP_HTTP_PORT/MCP_HTTP_HOST 覆盖后重装），跟 daemon 的多 profile 并存不是
# 同一类需求，模板化只会徒增管理面，故按简单单元处理。
MCP_HTTP_PORT ?= 8080
MCP_HTTP_HOST ?= 127.0.0.1
MCP_CPU_QUOTA ?= 100%
MCP_MEMORY_HIGH ?= 512M
MCP_MEMORY_MAX ?= 768M

MCP_HTTP_UNIT_DIR := $(HOME)/.config/systemd/user
MCP_HTTP_UNIT := $(MCP_HTTP_UNIT_DIR)/multica-mcp-http.service

mcp-http-install: mcp-build ## Install MCP streamable-HTTP transport as a systemd --user service (zero sudo): make mcp-http-install [MCP_HTTP_PORT=8080] [MCP_HTTP_HOST=127.0.0.1]
	@export XDG_RUNTIME_DIR=$${XDG_RUNTIME_DIR:-/run/user/$$(id -u)}; \
	case "$(MCP_HTTP_HOST)" in \
		127.0.0.1|localhost|::1) ;; \
		*) echo "WARN: MCP_HTTP_HOST=$(MCP_HTTP_HOST) 非 loopback——对外暴露前请确认已置于 TLS 反代之后" ;; \
	esac; \
	mkdir -p "$(MCP_HTTP_UNIT_DIR)"; \
	sed -e 's|@MCP_DIR@|$(CURDIR)/apps/mcp|g' \
	    -e 's|@PORT@|$(MCP_HTTP_PORT)|g' \
	    -e 's|@HOST@|$(MCP_HTTP_HOST)|g' \
	    -e 's|@CPU_QUOTA@|$(MCP_CPU_QUOTA)|g' \
	    -e 's|@MEMORY_HIGH@|$(MCP_MEMORY_HIGH)|g' \
	    -e 's|@MEMORY_MAX@|$(MCP_MEMORY_MAX)|g' \
	    deploy/multica-mcp-http.service.template > "$(MCP_HTTP_UNIT)"; \
	systemctl --user daemon-reload; \
	systemctl --user enable --now multica-mcp-http.service; \
	if [ "$$(loginctl show-user $$(id -un) -p Linger --value 2>/dev/null)" != "yes" ]; then \
		echo "WARN: 当前用户未开启 Linger，注销后该服务会随会话结束停止；如需注销/重启后仍常驻，请让有权限者执行: loginctl enable-linger $$(id -un)"; \
	fi; \
	systemctl --user --no-pager --lines=0 status multica-mcp-http.service; \
	echo "multica-mcp-http → http://$(MCP_HTTP_HOST):$(MCP_HTTP_PORT)/mcp（探活: /healthz，鉴权: Authorization: Bearer <PAT>，逐请求携带，服务端不落盘）"

mcp-http-update: mcp-build ## Rebuild dist/ only, then restart the running service (unit file / port / host untouched)
	@export XDG_RUNTIME_DIR=$${XDG_RUNTIME_DIR:-/run/user/$$(id -u)}; \
	systemctl --user restart multica-mcp-http.service; \
	systemctl --user --no-pager --lines=0 status multica-mcp-http.service

mcp-http-status: ## Read-only: systemd --user unit status for the MCP HTTP service
	@export XDG_RUNTIME_DIR=$${XDG_RUNTIME_DIR:-/run/user/$$(id -u)}; \
	systemctl --user --no-pager status multica-mcp-http.service 2>/dev/null || echo "multica-mcp-http.service 未安装（make mcp-http-install）"

mcp-http-uninstall: ## Remove the MCP HTTP systemd --user service only; never touches multica-daemon*, multica-oom-guard, or other systemd --user units
	@export XDG_RUNTIME_DIR=$${XDG_RUNTIME_DIR:-/run/user/$$(id -u)}; \
	systemctl --user disable --now multica-mcp-http.service >/dev/null 2>&1 || true; \
	rm -f "$(MCP_HTTP_UNIT)"; \
	systemctl --user daemon-reload; \
	systemctl --user reset-failed 2>/dev/null || true; \
	echo "multica-mcp-http.service 已移除（multica-daemon*、multica-oom-guard 与其他 systemd --user 单元未受影响）"

# ---------- Environments ----------
##@ Environments

# One verb per lifecycle step, shared by humans and agents. C= picks the
# components; ARGS= forwards anything else to the script.
#
#   make up                     api + web
#   make up C=api,web,daemon    add the agent daemon
#   make up C=desktop           Electron against this environment's backend
#   make up ARGS=--ephemeral    agent-owned, expires, collected by `make gc`

up: ## Start this checkout's environment (C=api,web,daemon,desktop; default api,web)
	@bash scripts/dev-env.sh up $(if $(C),--components $(C)) $(ARGS)

down: ## Stop this environment's processes, keeping its database and profile
	@bash scripts/dev-env.sh down $(ARGS)

status: ## Show what is running for this environment, with proof of identity
	@bash scripts/dev-env.sh status $(ARGS)

list: ## List every registered development environment on this machine
	@bash scripts/dev-env.sh list $(ARGS)

destroy: ## Stop this environment, drop its database and profile, free its slot
	@bash scripts/dev-env.sh destroy $(ARGS)

gc: ## Collect environments whose directory is gone or whose TTL expired
	@bash scripts/dev-env.sh gc $(ARGS)

env-exec: ## Run a command with this environment's variables (ARGS="-- pnpm dev:desktop")
	@bash scripts/dev-env.sh exec $(ARGS)

# Single-component entry points. No database preflight: `up` has already proven
# the database is reachable before it launches anything, and repeating the
# check here would make each component slower to restart on its own.
# The commit ldflag is what makes /health's identity useful: without it every
# environment reports "unknown" and cannot prove which revision it is serving.
api-dev: ## Run only the Go backend for the current env file
	cd server && go run -ldflags "-X main.commit=$(COMMIT)" ./cmd/server

web-dev: ## Run only the Next.js dev server for the current env file
	pnpm dev:web

desktop-dev: ## Run only the Electron desktop app for the current env file
	pnpm dev:desktop

# ---------- One-click commands ----------
##@ One-click

setup: ## Prepare the current checkout from its env file: install deps, ensure DB, run migrations
	$(REQUIRE_ENV)
	@echo "==> Using env file: $(ENV_FILE)"
	@echo "==> Installing dependencies..."
	pnpm install
	@bash scripts/ensure-postgres.sh "$(ENV_FILE)"
	@echo "==> Running migrations..."
	cd server && go run ./cmd/migrate up
	@echo ""
	@echo "✓ Setup complete! Run 'make start' to launch the app."

start: ## Start backend and frontend for the current checkout and run migrations first
	$(REQUIRE_ENV)
	@echo "Using env file: $(ENV_FILE)"
	@echo "Backend: http://localhost:$(PORT)"
	@echo "Frontend: http://localhost:$(FRONTEND_PORT)"
	@bash scripts/ensure-postgres.sh "$(ENV_FILE)"
	@echo "Running migrations..."
	cd server && go run ./cmd/migrate up
	@echo "Starting backend and frontend..."
	@trap 'kill 0' EXIT; \
		(cd server && go run ./cmd/server) & \
		pnpm dev:web & \
		wait

stop: ## Stop backend and frontend processes for the current checkout
	$(REQUIRE_ENV)
	@echo "Stopping services..."
	@-lsof -nP -iTCP:$(PORT) -sTCP:LISTEN -t | xargs kill -9 2>/dev/null
	@-lsof -nP -iTCP:$(FRONTEND_PORT) -sTCP:LISTEN -t | xargs kill -9 2>/dev/null
	@case "$(DATABASE_URL)" in \
		""|*@localhost:*|*@localhost/*|*@127.0.0.1:*|*@127.0.0.1/*|*@\[::1\]:*|*@\[::1\]/*) \
			echo "✓ App processes stopped. Shared PostgreSQL is still running on localhost:$(POSTGRES_PORT)." ;; \
		*) \
			echo "✓ App processes stopped. Remote PostgreSQL was not affected." ;; \
	esac

check: ## Run typecheck, TS tests, Go tests, and Playwright E2E for the current checkout
	$(REQUIRE_ENV)
	@ENV_FILE="$(ENV_FILE)" bash scripts/check.sh

db-up: ## Start the shared PostgreSQL container used by main and worktrees
	@$(COMPOSE) up -d postgres

db-down: ## Stop the shared PostgreSQL container without removing its Docker volume
	@$(COMPOSE) down

db-drop: ## Permanently drop the current env's local database after confirmation
	$(REQUIRE_ENV)
	@status=0; bash scripts/drop-database.sh "$(ENV_FILE)" || status=$$?; \
		if [ "$$status" -eq 2 ]; then exit 0; fi; \
		exit "$$status"

# Drop + recreate the database named by the current env, then run all migrations.
# Use only with a disposable database for a clean slate in local dev. Refuses
# to run against a remote host, and refuses the main database `multica` without
# ALLOW_MAIN_DB_DROP=1: under
# the shared-database model a worktree env file names it by default, and
# this target drops without a confirmation prompt (RUYI-66).
db-reset: ## Drop and recreate the current env's database, then re-run all migrations
	$(REQUIRE_ENV)
	@case "$(DATABASE_URL)" in \
		""|*@localhost:*|*@localhost/*|*@127.0.0.1:*|*@127.0.0.1/*|*@\[::1\]:*|*@\[::1\]/*) ;; \
		*) echo "Refusing to reset: DATABASE_URL points at a remote host."; exit 1 ;; \
	esac
	@if [ "$(POSTGRES_DB)" = "multica" ] && [ "$(ALLOW_MAIN_DB_DROP)" != "1" ]; then \
		echo "Refusing to reset the default main database 'multica'."; \
		echo "It backs the running platform instance. Re-run with ALLOW_MAIN_DB_DROP=1 only if wiping it is intentional."; \
		exit 1; \
	fi
	@bash scripts/ensure-postgres.sh "$(ENV_FILE)"
	@echo "==> Dropping and recreating database '$(POSTGRES_DB)'..."
	@$(COMPOSE) exec -T postgres psql -U $(POSTGRES_USER) -d postgres -v ON_ERROR_STOP=1 \
		-c "DROP DATABASE IF EXISTS \"$(POSTGRES_DB)\" WITH (FORCE);" \
		-c "CREATE DATABASE \"$(POSTGRES_DB)\";"
	@echo "==> Running migrations..."
	cd server && go run ./cmd/migrate up
	@echo ""
	@echo "✓ Database '$(POSTGRES_DB)' reset. Run 'make start' to launch the app."

worktree-env: ## Generate .env.worktree with shared DB settings and unique app ports
	@bash scripts/init-worktree-env.sh .env.worktree

setup-main: ## Prepare the main checkout using .env
	@$(MAKE) setup ENV_FILE=$(MAIN_ENV_FILE)

start-main: ## Start the main checkout using .env
	@$(MAKE) start ENV_FILE=$(MAIN_ENV_FILE)

stop-main: ## Stop the main checkout processes defined by .env
	@$(MAKE) stop ENV_FILE=$(MAIN_ENV_FILE)

check-main: ## Run the full verification pipeline for the main checkout
	@ENV_FILE=$(MAIN_ENV_FILE) bash scripts/check.sh

setup-worktree: ## Ensure .env.worktree exists, then prepare this worktree
	@if [ ! -f "$(WORKTREE_ENV_FILE)" ]; then \
		echo "==> Generating $(WORKTREE_ENV_FILE) with unique ports..."; \
		bash scripts/init-worktree-env.sh $(WORKTREE_ENV_FILE); \
	else \
		echo "==> Using existing $(WORKTREE_ENV_FILE)"; \
	fi
	@$(MAKE) setup ENV_FILE=$(WORKTREE_ENV_FILE)

start-worktree: ## Start this worktree using .env.worktree
	@$(MAKE) start ENV_FILE=$(WORKTREE_ENV_FILE)

stop-worktree: ## Stop this worktree's backend and frontend processes
	@$(MAKE) stop ENV_FILE=$(WORKTREE_ENV_FILE)

check-worktree: ## Run the full verification pipeline for this worktree
	@ENV_FILE=$(WORKTREE_ENV_FILE) bash scripts/check.sh

remove-worktree: ## Preserve multica, clean an optional disposable DB, then remove a worktree
	@bash scripts/remove-worktree.sh "$(WORKTREE)"

agent-branches: ## List the agent task branches a local_directory repo carries (read-only; add DELETE=1 to prune the safe ones)
	@bash scripts/agent-branch-cleanup.sh $(if $(REPO),--repo "$(REPO)") $(if $(DELETE),--delete)

# ---------- Individual commands ----------
##@ Individual commands

dev: ## Bootstrap this checkout end-to-end: create env if needed, ensure DB, migrate, start services
	@bash scripts/dev.sh

server: ## Run only the Go server for the current checkout
	$(REQUIRE_ENV)
	@bash scripts/ensure-postgres.sh "$(ENV_FILE)"
	cd server && go run ./cmd/server

daemon: ## Restart the local agent daemon using the CLI's stored auth/session
	@$(MAKE) multica MULTICA_ARGS="daemon restart --profile local"

cli: ## Run the multica CLI with ARGS or MULTICA_ARGS from source
	@$(MAKE) multica MULTICA_ARGS="$(MULTICA_ARGS)"

multica: ## Run the multica CLI entrypoint directly from the Go source tree
	cd server && go run -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)" ./cmd/multica $(MULTICA_ARGS)

VERSION ?= $(shell git describe --tags --match 'v[0-9]*' --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
# Windows will not execute an extensionless binary, so a source build there has
# to name its outputs the way the target platform expects — otherwise the CLI
# builds fine and then fails to re-exec itself as a daemon (#7255). GOOS reaches
# a build two ways: as an environment variable (`GOOS=windows make build`) and
# as a Make variable (`make build GOOS=windows`). The top-level `export` sends
# both forms to the recipe, so `go build` honors both and the suffix has to as
# well; `$(GOOS)` covers the Make-variable form, which a parse-time
# `go env GOOS` cannot see. Target-specific so only `build` pays for the probe:
# a global assignment runs `go env` on every target — `export` expands even a
# recursive one — which prints `go: Command not found` on frontend-only
# checkouts with no Go toolchain installed.
build: EXE = $(if $(filter windows,$(or $(GOOS),$(shell go env GOOS))),.exe,)
build: ## Build the server, CLI, and migrate binaries into server/bin
	cd server && go build -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT)" -o bin/server$(EXE) ./cmd/server
	cd server && go build -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)" -o bin/multica$(EXE) ./cmd/multica
	cd server && go build -o bin/migrate$(EXE) ./cmd/migrate

test: ## Run Go tests after ensuring the target DB exists and migrations are applied
	$(REQUIRE_ENV)
	@bash scripts/ensure-postgres.sh "$(ENV_FILE)"
	cd server && go run ./cmd/migrate up
	bash scripts/test-go.sh --race

# Database
##@ Database

migrate-up: ## Create the target DB if needed, then apply database migrations
	$(REQUIRE_ENV)
	@bash scripts/ensure-postgres.sh "$(ENV_FILE)"
	cd server && go run ./cmd/migrate up

migrate-down: ## Create the target DB if needed, then roll back database migrations
	$(REQUIRE_ENV)
	@bash scripts/ensure-postgres.sh "$(ENV_FILE)"
	cd server && go run ./cmd/migrate down

sqlc: ## Regenerate sqlc code
	cd server && go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate

# Cleanup
##@ Cleanup

clean: ## Remove build caches, generated binaries, and temp files
	rm -rf server/bin server/tmp
	rm -rf apps/*/.next apps/*/.source apps/*/.expo
	rm -rf apps/*/out apps/*/dist apps/*/dist-electron packages/*/dist
	rm -rf .turbo apps/*/.turbo packages/*/.turbo
	rm -rf apps/*/*.tsbuildinfo packages/*/*.tsbuildinfo
	@echo "✓ Clean complete."
