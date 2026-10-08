.PHONY: help makehelp dev server daemon cli multica build test test-redis-down migrate-up migrate-down sqlc seed clean setup start stop check worktree-env setup-main start-main stop-main check-main setup-worktree start-worktree stop-worktree check-worktree remove-worktree agent-branches db-up db-down db-drop db-reset selfhost selfhost-build selfhost-stop up down status list destroy orphans gc audit env-exec api-dev web-dev desktop-dev mcp-dev daemon-build daemon-install daemon-update daemon-preflight daemon-uninstall mcp-build mcp-install mcp-update mcp-status mcp-uninstall mcp-http-install mcp-http-update mcp-http-status mcp-http-uninstall check-slot use
MAIN_ENV_FILE ?= .env
WORKTREE_ENV_FILE ?= .env.worktree
ENV_FILE ?= $(if $(wildcard $(MAIN_ENV_FILE)),$(MAIN_ENV_FILE),$(if $(wildcard $(WORKTREE_ENV_FILE)),$(WORKTREE_ENV_FILE),$(MAIN_ENV_FILE)))

# Including the env file directly makes a single multi-line value in it — a PEM
# key, a certificate — abort every target with "missing separator" before the
# first recipe runs, because makefile syntax has no line continuation for
# values (RUYI-218). scripts/env-make-include.sh rewrites those values as
# `define` blocks, which the `export` below still delivers to subprocesses with
# their newlines intact, and passes everything else through unchanged.
ifneq ($(wildcard $(ENV_FILE)),)
ENV_MAKEFRAG := $(shell bash scripts/env-make-include.sh '$(ENV_FILE)')
include $(ENV_MAKEFRAG)
endif
unexport ENV_MAKEFRAG

POSTGRES_DB ?= multica
POSTGRES_USER ?= multica
POSTGRES_PASSWORD ?= multica
POSTGRES_PORT ?= 5432
PORT := $(or $(BACKEND_PORT),$(API_PORT),$(SERVER_PORT),$(PORT),8080)
# Browser-facing URLs default to EMPTY (same-origin mode): the web app then
# sends relative URLs through the Next proxy (REMOTE_API_URL) and derives the
# WS URL from window.location. Do not bake absolute localhost URLs here — this
# block is exported, and an exported value outranks .env in compose variable
# interpolation, silently overriding the operator's .env (RUYI-256).
ifeq ($(origin MULTICA_PUBLIC_URL), undefined)
MULTICA_PUBLIC_URL :=
endif
FRONTEND_PORT ?= 3000
FRONTEND_ORIGIN ?= http://localhost:$(FRONTEND_PORT)
MULTICA_APP_URL ?= $(FRONTEND_ORIGIN)
DATABASE_URL ?= postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@localhost:$(POSTGRES_PORT)/$(POSTGRES_DB)?sslmode=disable
NEXT_PUBLIC_API_URL ?=
NEXT_PUBLIC_WS_URL ?=
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

# The MCP + OAuth shape of the self-hosted stack — the redis and mcp services, and
# the MCP_URL / OAUTH_SIGNING_KEY / REDIS_URL entries on frontend/backend — is
# declared only in docker-compose.selfhost.local.yml. Compose interpolates a
# variable into a container only when some compose file declares it, so setting
# those three in .env does nothing on its own: a selfhost run without this overlay
# rebuilds frontend/backend without them and silently drops the public /api/mcp
# entry point back to 404 (RUYI-260). Overlay the file whenever it exists, and keep
# the file list byte-identical when it does not, so upstream users, CI and
# deployments that never configured MCP are unaffected.
SELFHOST_LOCAL_FILE := docker-compose.selfhost.local.yml
SELFHOST_LOCAL_OVERRIDE := $(if $(wildcard $(SELFHOST_LOCAL_FILE)),-f $(SELFHOST_LOCAL_FILE))
SELFHOST_COMPOSE_FILES := $(strip -f docker-compose.selfhost.yml $(SELFHOST_LOCAL_OVERRIDE))
SELFHOST_BUILD_COMPOSE_FILES := $(strip -f docker-compose.selfhost.yml -f docker-compose.selfhost.build.yml $(SELFHOST_LOCAL_OVERRIDE))

selfhost: ## Create .env if needed, then pull and start the official images (overlays docker-compose.selfhost.local.yml when present)
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
# Pull stays on the official file alone: the overlay's mcp image is built from this
# checkout (Dockerfile.mcp), and `pull` treats a missing buildable image as a pull
# failure, which would misreport the official images as unpublished. The `up` below
# builds it and pulls redis on demand.
	@echo "==> Pulling official Multica images..."
	@if ! $(COMPOSE) -f docker-compose.selfhost.yml pull; then \
		echo ""; \
		echo "Official images for tag '$${MULTICA_IMAGE_TAG:-latest}' are not published yet."; \
		echo "If this is before the first GHCR release, build from the current checkout:"; \
		echo "  make selfhost-build"; \
		exit 1; \
	fi
	@echo "==> Starting Multica via Docker Compose..."
	$(COMPOSE) $(SELFHOST_COMPOSE_FILES) up -d
	@bash scripts/selfhost-wait.sh official

selfhost-build: ## Build backend/web from this checkout and start the stack (overlays docker-compose.selfhost.local.yml when present)
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
	$(COMPOSE) $(SELFHOST_BUILD_COMPOSE_FILES) up -d --build
	@bash scripts/selfhost-wait.sh build

selfhost-stop: ## Stop the self-hosted Docker Compose stack (overlays docker-compose.selfhost.local.yml when present)
	$(REQUIRE_COMPOSE)
	@echo "==> Stopping Multica services..."
	$(COMPOSE) $(SELFHOST_COMPOSE_FILES) down
	@echo "✓ All services stopped."

# ---------- Daemon (systemd service on this host) ----------
##@ Daemon (systemd)

daemon-build: ## Build the runtime daemon CLI with release version metadata
	cd server && go build -ldflags "-X main.version=$$(git tag -l 'v[0-9]*' --sort=-v:refname | head -1 | sed 's/^v//') -X main.commit=$$(git rev-parse --short HEAD) -X main.date=$$(date -u '+%Y-%m-%dT%H:%M:%SZ')" -o bin/multica ./cmd/multica

# 部署参数：以「执行 make 的普通用户」为 daemon 运行用户（内部自动 sudo，勿用 sudo make）。
# 换机器按规格覆盖：make daemon-install CPU_QUOTA=400% MEMORY_HIGH=8G MEMORY_MAX=12G
# 背压按机覆盖（GTI 校准值示例，覆盖值自动持久化、update 重渲染自动还原）：
#   make daemon-install BP_MEM_HIGH_PCT=30 BP_MEM_RECOVERY_PCT=40 BP_SWAP_HIGH_PCT=60 BP_SWAP_RECOVERY_PCT=45
# PROFILE 决定 daemon 读取的 multica 配置（systemd 实例名即 profile 名）：
#   make daemon-install                → multica-daemon@default，读 ~/.multica/
#   make daemon-install PROFILE=idata  → multica-daemon@idata，读 ~/.multica/profiles/idata/
PROFILE ?= default

# 主机侧渲染参数持久化（RUYI-396）：daemon-install / daemon-update 渲染安装成功后，
# 把当次生效值写回该文件；下次渲染前先读回——已按机配置的值不会被裸跑 update
# 静默冲回默认。优先级：make 命令行传参 > 本文件 > 下方代码默认；手工微调直接
# 编辑该文件，删除该文件即恢复代码默认。
DAEMON_RENDER_MK ?= $(HOME)/.multica/daemon-render.mk
-include $(DAEMON_RENDER_MK)

CPU_QUOTA ?= 600%
MEMORY_HIGH ?= 24G
MEMORY_MAX ?= 28G
# 背压准入阈值（RUYI-393）：默认 = 代码默认（server/internal/daemon/config.go 的
# DefaultBackpressure* 常量；swap 双阈值 <=0 可关断 swap 条件，同代码语义）。
BP_MEM_HIGH_PCT ?= 15
BP_MEM_RECOVERY_PCT ?= 25
BP_SWAP_HIGH_PCT ?= 80
BP_SWAP_RECOVERY_PCT ?= 60
# 运行用户/组：以执行 make 的普通用户为准（make 层展开，避免 shell 单引号吞掉命令替换）
USER ?= $(shell id -un)
GROUP ?= $(shell id -gn)

# unit 渲染命令：install / update 共用同一套替换，保证两条链路渲染产物一致
DAEMON_UNIT_RENDER = sed \
	  -e 's|@USER@|$(USER)|g' \
	  -e 's|@GROUP@|$(GROUP)|g' \
	  -e 's|@HOME@|$(HOME)|g' \
	  -e 's|@BIN@|$(HOME)/.local/bin/multica|g' \
	  -e 's|@CPU_QUOTA@|$(CPU_QUOTA)|g' \
	  -e 's|@MEMORY_HIGH@|$(MEMORY_HIGH)|g' \
	  -e 's|@MEMORY_MAX@|$(MEMORY_MAX)|g' \
	  -e 's|@BP_MEM_HIGH_PCT@|$(BP_MEM_HIGH_PCT)|g' \
	  -e 's|@BP_MEM_RECOVERY_PCT@|$(BP_MEM_RECOVERY_PCT)|g' \
	  -e 's|@BP_SWAP_HIGH_PCT@|$(BP_SWAP_HIGH_PCT)|g' \
	  -e 's|@BP_SWAP_RECOVERY_PCT@|$(BP_SWAP_RECOVERY_PCT)|g'

# 渲染参数持久化写回（install/update 安装成功后调用；在 recipe 内展开为多行 shell）
define DAEMON_RENDER_PERSIST
printf '%s\n' \
	  '# multica-daemon systemd unit 渲染参数（主机侧持久化，由 make daemon-install / daemon-update 维护）' \
	  '# 优先级：make 命令行传参 > 本文件 > Makefile 代码默认；手工微调直接编辑本文件，删除即恢复默认' \
	  'CPU_QUOTA ?= $(CPU_QUOTA)' \
	  'MEMORY_HIGH ?= $(MEMORY_HIGH)' \
	  'MEMORY_MAX ?= $(MEMORY_MAX)' \
	  'BP_MEM_HIGH_PCT ?= $(BP_MEM_HIGH_PCT)' \
	  'BP_MEM_RECOVERY_PCT ?= $(BP_MEM_RECOVERY_PCT)' \
	  'BP_SWAP_HIGH_PCT ?= $(BP_SWAP_HIGH_PCT)' \
	  'BP_SWAP_RECOVERY_PCT ?= $(BP_SWAP_RECOVERY_PCT)' > $(DAEMON_RENDER_MK)
endef

# DeerFlow 配置收敛（RUYI-548）：daemon 安装/更新两条链路共用——目标实例 config.json
# 缺 backends.deerflow.home 且本机存在 DeerFlow 部署（$(DEERFLOW_HOME)/config.yaml 在位）
# 时自动补写，先于实例启动/重启执行，重启即生效（Owner 指令：安装和更新部署时自动配置）。
# 幂等：键已存在不覆盖、只回显既有值；写入经同目录临时文件原子替换，保留原文件权限与
# 全部既有键（token 不失）。DEERFLOW_HOME 置空整体禁用；按机覆盖：
#   make daemon-install DEERFLOW_HOME=/path/to/deerflow
# 仅收敛 deerflow 一项，不建通用 backend 配置框架。
DEERFLOW_HOME ?= $(HOME)/srv/deerflow

# $(1) = 目标 profile 名；ALL = default 配置 + 全部 enabled 实例的 profile 配置。
define DEERFLOW_CONVERGE_SCOPE
if [ -z "$(DEERFLOW_HOME)" ]; then \
	  echo "deerflow 配置收敛：跳过（DEERFLOW_HOME 置空，整体禁用）"; \
elif [ ! -f "$(DEERFLOW_HOME)/config.yaml" ]; then \
	  echo "deerflow 配置收敛：跳过（未检测到 DeerFlow 部署：$(DEERFLOW_HOME)/config.yaml）"; \
else \
	  if [ "$(1)" = ALL ]; then \
		    cfgs="$(HOME)/.multica/config.json"; \
		    for u in $$(systemctl list-unit-files 'multica-daemon@*.service' --no-legend 2>/dev/null | awk '$$2 == "enabled" {print $$1}'); do \
			      p=$${u#multica-daemon@}; p=$${p%.service}; \
			      [ "$$p" = default ] || cfgs="$$cfgs $(HOME)/.multica/profiles/$$p/config.json"; \
		    done; \
	  elif [ "$(1)" = default ]; then \
		    cfgs="$(HOME)/.multica/config.json"; \
	  else \
		    cfgs="$(HOME)/.multica/profiles/$(1)/config.json"; \
	  fi; \
	  for c in $$cfgs; do \
		    if [ ! -f "$$c" ]; then \
			      echo "deerflow 配置收敛：跳过（config 不存在：$$c）"; \
		    elif jq -e '.backends.deerflow.home != null' "$$c" >/dev/null 2>&1; then \
			      echo "deerflow 配置收敛：已配置，保持 backends.deerflow.home=$$(jq -r '.backends.deerflow.home' "$$c")（$$c）"; \
		    else \
			      dftmp=""; \
			      if dftmp="$$(mktemp "$$c.XXXXXX")" \
				      && jq --arg h "$(DEERFLOW_HOME)" '.backends.deerflow.home = $$h' "$$c" > "$$dftmp" \
				      && chmod --reference="$$c" "$$dftmp" \
				      && mv -f "$$dftmp" "$$c"; then \
				        echo "deerflow 配置收敛：已写入 $$c → backends.deerflow.home=$(DEERFLOW_HOME)"; \
			      else \
				        [ -n "$$dftmp" ] && rm -f "$$dftmp"; \
				        echo "WARN: deerflow 配置写入失败（已保留原 config）：$$c"; \
			      fi; \
		    fi; \
	  done; \
fi
endef

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
	$(DAEMON_UNIT_RENDER) deploy/multica-daemon@.service.template > /tmp/multica-daemon@.service
	sudo install -m644 /tmp/multica-daemon@.service /etc/systemd/system/multica-daemon@.service
	@mkdir -p $(HOME)/.multica && $(DAEMON_RENDER_PERSIST)
	@echo "渲染参数已持久化 → $(DAEMON_RENDER_MK)（daemon-update 重渲染时自动读回）"
	# deerflow 配置收敛（RUYI-548）：目标实例 config.json 缺 backends.deerflow.home 时自动补写（先于启动）
	@$(call DEERFLOW_CONVERGE_SCOPE,$(PROFILE))
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

daemon-update: daemon-build ## Re-render and converge multica-daemon@.service from the current template (auto snapshot + diff first), then install, daemon-reload, restart every enabled multica-daemon@* instance (pass PROFILE=<name> to restart only that one)
	@if [ -n "$$SUDO_USER" ]; then echo "ERROR: run 'make daemon-update' as the regular user (sudo is invoked internally)"; exit 1; fi
	install -m755 server/bin/multica $(HOME)/.local/bin/multica
	@if [ -f $(DAEMON_RENDER_MK) ]; then echo "== 读回主机渲染参数：$(DAEMON_RENDER_MK) =="; fi
	$(DAEMON_UNIT_RENDER) deploy/multica-daemon@.service.template > /tmp/multica-daemon@.service
	@if [ -f /etc/systemd/system/multica-daemon@.service ]; then \
		ts=$$(date +%Y%m%d-%H%M%S); \
		snap=/var/backups/multica-daemon/multica-daemon@.service.bak-$$ts; \
		sudo mkdir -p /var/backups/multica-daemon; \
		sudo cp -a /etc/systemd/system/multica-daemon@.service "$$snap"; \
		echo "== unit 快照（回滚锚点）→ $$snap =="; \
		echo "== 重渲染 diff（旧 → 新）=="; \
		diff -u "$$snap" /tmp/multica-daemon@.service || true; \
	else \
		echo "== 首次渲染：无既有 unit，跳过快照与 diff =="; \
	fi
	sudo install -m644 /tmp/multica-daemon@.service /etc/systemd/system/multica-daemon@.service
	@mkdir -p $(HOME)/.multica && $(DAEMON_RENDER_PERSIST)
	@echo "渲染参数已持久化 → $(DAEMON_RENDER_MK)"
	# deerflow 配置收敛（RUYI-548）：带 PROFILE 只收敛该实例；不带则收敛 default + 全部 enabled 实例（先于重启）
	@$(call DEERFLOW_CONVERGE_SCOPE,$(if $(filter command line,$(origin PROFILE)),$(PROFILE),ALL))
	sudo systemctl daemon-reload
	@if [ "$(origin PROFILE)" = "command line" ]; then \
		units="multica-daemon@$(PROFILE).service"; \
	else \
		units="$$(systemctl list-unit-files 'multica-daemon@*.service' --no-legend 2>/dev/null | awk '$$2 == "enabled" {print $$1}' | grep -v '^multica-daemon@\.service$$' | sort -u)"; \
	fi; \
	if [ -z "$$units" ]; then \
		echo "WARN: 未发现 enabled 的 multica-daemon@* 实例；unit 已重渲染安装，实例需手动启动"; \
	else \
		for u in $$units; do echo "== restart $$u =="; sudo systemctl restart "$$u"; done; \
		for u in $$units; do systemctl --no-pager --lines=0 status "$$u" || true; done; \
	fi

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
	@echo "   已保留: ~/.multica/（各 profile 连接配置与 token、daemon-render.mk 渲染参数）、~/multica_workspaces*/$(if $(PURGE),,、CLI 二进制 ~/.local/bin/multica)"

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

# Fixed slots (RUYI-333): every command names the slot it acts on. There are
# exactly two general slots (dev1, dev2); QA verification reuses the issue's
# own slot by handing its lease to the qa role (`make use SLOT=dev1 ...
# handoff`). A slot's ports, database and account are fixed facts in
# scripts/slots.json; write commands additionally require the owning issue
# via MULTICA_CALLER_OWNER.
#
#   make up SLOT=dev1                    api + web for slot dev1
#   make up SLOT=dev1 C=api,web,daemon   add the agent daemon
#   scripts/dev-env.sh dev1 handoff --to qa     release dev1 to QA verification
#
# SLOT is required: the old dynamic name+offset allocator is gone, so two
# environments can no longer drift into sharing a database.

check-slot:
	@if [ -z "$(SLOT)" ]; then \
		echo "SLOT is required: make up SLOT=dev1 (slots: dev1, dev2 — scripts/slots.json)"; \
		exit 2; \
	fi

up: check-slot ## Start a slot's environment (C=api,web,daemon,desktop; default api,web)
	@bash scripts/dev-env.sh $(SLOT) up $(if $(C),--components $(C)) $(ARGS)

down: check-slot ## Stop a slot's processes, keeping its database and profile
	@bash scripts/dev-env.sh $(SLOT) down $(ARGS)

status: check-slot ## Show what is running for a slot, with proof of identity
	@bash scripts/dev-env.sh $(SLOT) status $(ARGS)

list: ## List every fixed slot on this machine (registered/free)
	@bash scripts/dev-env.sh list $(ARGS)

use: check-slot ## Bind this checkout, or load a revision into a slot (ARGS="<sha>")
	@bash scripts/dev-env.sh $(SLOT) use $(ARGS)

destroy: check-slot ## Stop a slot, drop its database and account, free it
	@bash scripts/dev-env.sh $(SLOT) destroy $(ARGS)

orphans: check-slot ## Acceptance check: nothing of this slot survives down/destroy
	@bash scripts/dev-env.sh $(SLOT) orphans $(ARGS)

gc: ## Collect expired qa-phase slots (and slots whose code directory is gone)
	@bash scripts/dev-env.sh gc $(ARGS)

audit: ## Read-only bypass sweep: unregistered multica_% DBs + off-registry Multica listeners (exit 1 = findings)
	@bash scripts/dev-env.sh audit $(ARGS)

qa-clean: ## Reclaim QA leftovers (ARGS="--issue ruyi-283 --yes [--docker]"; default is a dry run)
	@bash scripts/qa-clean.sh $(ARGS)

env-exec: check-slot ## Run a command with a slot's variables (ARGS="-- pnpm dev:desktop")
	@bash scripts/dev-env.sh $(SLOT) exec $(ARGS)

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

# Dev-slot MCP Node (RUYI-428): behind the web /api/mcp rewrite. --server-url
# overrides the env file's daemon-shaped ws://.../ws MULTICA_SERVER_URL with
# the REST origin; MULTICA_MCP_PORT (slot env) picks the listener port.
mcp-dev: ## Run only the MCP HTTP server for the current env file
	node apps/mcp/dist/index.js --transport http --host 127.0.0.1 --server-url http://localhost:$(PORT)

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
# The auto-computed COMMIT must not reach sub-makes: `make up` recurses into
# the slot worktree via `make -C` (scripts/dev-env.sh), and the top-level
# `export` above would hand it this checkout's HEAD, outranking the sub-make's
# own `COMMIT ?=` — the api binary then gets stamped with the wrong commit and
# the identity gate kills it (RUYI-420). An explicitly provided COMMIT (command
# line or environment) keeps origin `command line`/`environment` and still
# exports, so `make up COMMIT=xyz` keeps working.
ifeq ($(origin COMMIT),file)
unexport COMMIT
endif
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
	REDIS_TEST_URL="$$(bash scripts/ensure-redis.sh url)" bash scripts/test-go.sh --race --verbose

test-redis-down: ## Remove the throwaway test Redis container started for `make test`
	@bash scripts/ensure-redis.sh down

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
