package agent

import (
	"context"
	"fmt"
)

// BuiltinRuntime describes a built-in runtime identity that the daemon probes
// and registers automatically — independent of the custom runtime profile
// protocol_family whitelist. Multiple runtime identities can share one
// protocol family (e.g. both "pi" and "omp" use the "pi" protocol backend).
//
// The descriptor is the single declaration site for a runtime identity:
// agents_probe.go, config.go, daemon.go display-name overrides, execenv
// skill/config paths, local_skills.go, ListModels, and the frontend display
// maps all derive from this. Adding a new compatible fork of an existing
// runtime is a descriptor entry, not a cross-stack change.
type BuiltinRuntime struct {
	// ID is the provider key the daemon registers under (e.g. "omp").
	// It is NOT a protocol_family — it does not appear in SupportedTypes or
	// the runtime_profile.protocol_family CHECK constraint.
	ID string

	// ProtocolFamily is the execution backend this runtime dispatches to.
	// It MUST be in SupportedTypes. NewRuntime builds that family's backend
	// via New(), then applies this descriptor's ID-specific defaults
	// (executable, label) to it.
	ProtocolFamily string

	// DefaultCommand is the bare CLI name the probe looks up on PATH
	// when MULTICA_<ID>_PATH is not set (e.g. "omp").
	DefaultCommand string

	// EnvPrefix is the MULTICA_ prefix for *_PATH and *_MODEL env overrides
	// (e.g. "MULTICA_OMP" → MULTICA_OMP_PATH / MULTICA_OMP_MODEL).
	EnvPrefix string

	// DisplayName is the human-facing runtime name. The daemon and frontend
	// both use this so they never drift apart.
	DisplayName string

	// SkillsDir is the project-level skills directory relative to the
	// workdir (e.g. ".omp/skills").
	SkillsDir string

	// UserSkillsDir is the user-level skills directory relative to $HOME
	// (e.g. ".omp/agent/skills").
	UserSkillsDir string

	// LaunchHeader is the user-visible launch skeleton shown in the UI
	// (e.g. "omp (json mode)").
	LaunchHeader string

	// DefaultExecutable is the binary name the backend falls back to when
	// cfg.ExecutablePath is empty (passed to piBackend.defaultExecutable).
	DefaultExecutable string

	// ProviderLabel is the label used in log/error messages (passed to
	// piBackend.providerLabel).
	ProviderLabel string

	// InstallCommand is the shell command to install or upgrade this runtime
	// to a specific version on a target Linux user account.
	// The placeholders {{version}} and {{user}} are replaced at runtime.
	// Example: "runuser -u {{user}} -- npm install -g package@{{version}}"
	// When empty, installation via the admin UI is not supported for this runtime.
	InstallCommand string
	// InstallerSource identifies the package or upstream installer used for asset records.
	InstallerSource string
	// LatestOnly opts out of version pinning. The zero value requires a version.
	LatestOnly bool

	// ModelDiscovery is the strategy for discovering available models.
	// When set, it replaces the protocol family's discovery entirely — omp
	// uses `omp models --json`, a different command and output shape from
	// pi's `--list-models`. When nil, ListModels returns an empty catalog
	// rather than falling back to the family's command: running a
	// semantically incompatible one (omp exits non-zero on `--list-models`)
	// is worse than degrading to manual entry.
	ModelDiscovery ModelDiscoveryFunc
}

// ModelDiscoveryFunc discovers available models for a runtime identity.
// It receives the context and the resolved command — executable plus the
// runtime's launch prefix — and returns a model catalog. When the binary is
// missing or too old, it returns an empty slice (ListModels swallows the
// error and degrades to manual entry).
type ModelDiscoveryFunc func(ctx context.Context, runtimeCmd Command) ([]Model, error)

// BuiltinRuntimes is the registry of built-in runtime identities that are
// NOT in SupportedTypes (they are protocol-family derivatives, not families
// themselves). The daemon probes each one independently and dispatches it
// to its protocol family's backend.
//
// A runtime identity appears here when it needs its own probe/display/skills
// but reuses an existing backend's protocol. The first entry is omp (oh-my-pi):
// a separate CLI that speaks the pi JSON event protocol.
var BuiltinRuntimes = []BuiltinRuntime{
	{
		ID:                "omp",
		ProtocolFamily:    "pi",
		DefaultCommand:    "omp",
		EnvPrefix:         "MULTICA_OMP",
		DisplayName:       "Oh-My-Pi",
		SkillsDir:         ".omp/skills",
		UserSkillsDir:     ".omp/agent/skills",
		LaunchHeader:      "omp (json mode)",
		DefaultExecutable: "omp",
		ProviderLabel:     "omp",
		ModelDiscovery:    discoverOmpModels,
		// Use the official standalone binary; the npm entry point requires Bun.
		InstallCommand:  ompRuntimeInstall(),
		InstallerSource: "https://raw.githubusercontent.com/can1357/oh-my-pi/main/scripts/install.sh",
	},
}

// ProtocolFamilyInstall describes how to install a first-class protocol family
// (one that is in SupportedTypes) on a target Linux user account.
// These are separate from BuiltinRuntimes because protocol families are
// dispatched directly by New(), not through NewRuntime(), and adding them to
// BuiltinRuntimes would break ResolveBackend by routing them through the wrong
// factory. InstallCommand supports {{user}} and {{version}} placeholders.
type ProtocolFamilyInstall struct {
	// ID matches a value in SupportedTypes.
	ID string
	// DisplayName is the human-facing runtime name shown in the admin UI.
	DisplayName string
	// DefaultCommand is the bare CLI name used to probe the installed version.
	DefaultCommand string
	// InstallCommand is the shell command template. {{user}} and {{version}}
	// are substituted at runtime. Latest-only installers omit {{version}}.
	InstallCommand string
	// InstallerSource identifies the package or upstream installer used for asset records.
	InstallerSource string
	// LatestOnly opts out of version pinning. The zero value requires a version.
	LatestOnly bool
}

// ProtocolFamilyInstalls lists install descriptors for first-class protocol
// families. The admin UI merges this with BuiltinRuntimes to show all
// installable runtimes for a Computer.
var ProtocolFamilyInstalls = []ProtocolFamilyInstall{
	{
		ID:              "claude",
		InstallerSource: "npm:@anthropic-ai/claude-code",
		DisplayName:     "Claude Code",
		DefaultCommand:  "claude",
		InstallCommand:  npmRuntimeInstall("@anthropic-ai/claude-code", "claude"),
	},
	{
		ID:              "codex",
		InstallerSource: "npm:@openai/codex",
		DisplayName:     "Codex",
		DefaultCommand:  "codex",
		InstallCommand:  npmRuntimeInstall("@openai/codex", "codex"),
	},
	{
		ID:              "opencode",
		InstallerSource: "npm:opencode-ai",
		DisplayName:     "OpenCode",
		DefaultCommand:  "opencode",
		InstallCommand:  npmRuntimeInstall("opencode-ai", "opencode"),
	},
	{
		ID:              "pi",
		InstallerSource: "npm:@earendil-works/pi-coding-agent",
		DisplayName:     "Pi",
		DefaultCommand:  "pi",
		InstallCommand:  npmRuntimeInstall("@earendil-works/pi-coding-agent", "pi"),
	},
	{
		ID:              "grok",
		InstallerSource: "https://x.ai/cli/install.sh",
		DisplayName:     "Grok",
		DefaultCommand:  "grok",
		// Latest uses no argument; the upstream installer rejects the literal "latest".
		InstallCommand: scriptRuntimeInstall("https://x.ai/cli/install.sh", `if [ "{{version}}" = latest ]; then bash "$installer"; else bash "$installer" "{{version}}"; fi`, "grok"),
	},
	{
		ID:              "kimi",
		InstallerSource: "https://code.kimi.com/kimi-code/install.sh",
		DisplayName:     "Kimi",
		DefaultCommand:  "kimi",
		// Keep the native installation directory and pass pinned versions explicitly.
		InstallCommand: scriptRuntimeInstall("https://code.kimi.com/kimi-code/install.sh", `export KIMI_INSTALL_DIR="$HOME/.kimi-code" KIMI_VERSION=; if [ "{{version}}" = latest ]; then bash "$installer"; else bash "$installer" --version "{{version}}"; fi`, "kimi"),
	},
}

// ProtocolFamilyInstallByID looks up an install descriptor for a protocol family.
func ProtocolFamilyInstallByID(id string) (ProtocolFamilyInstall, bool) {
	for _, p := range ProtocolFamilyInstalls {
		if p.ID == id {
			return p, true
		}
	}
	return ProtocolFamilyInstall{}, false
}

// BuiltinRuntimeByID returns the descriptor for the given runtime identity,
// or false if no such built-in runtime exists.
func BuiltinRuntimeByID(id string) (BuiltinRuntime, bool) {
	for _, r := range BuiltinRuntimes {
		if r.ID == id {
			return r, true
		}
	}
	return BuiltinRuntime{}, false
}

// IsBuiltinRuntime reports whether id is a registered built-in runtime
// identity (as opposed to a protocol family in SupportedTypes).
func IsBuiltinRuntime(id string) bool {
	_, ok := BuiltinRuntimeByID(id)
	return ok
}

// BuiltinRuntimeCommands returns the default CLI command names for all
// built-in runtimes, for the daemon's defaultAgentCommandNames list.
func BuiltinRuntimeCommands() []string {
	cmds := make([]string, len(BuiltinRuntimes))
	for i, r := range BuiltinRuntimes {
		cmds[i] = r.DefaultCommand
	}
	return cmds
}

// backendOverrideApplicator is the interface that lets a descriptor apply
// per-runtime overrides (executable, label) to the backend its protocol family
// returned. A family whose backend does not implement it cannot host a runtime
// identity at all: NewRuntime fails closed rather than handing back a backend
// with the overrides silently dropped.
type backendOverrideApplicator interface {
	applyBuiltinRuntimeOverrides(desc BuiltinRuntime)
}

// piBackend already implements this via its defaultExecutable and
// providerLabel fields. The method is defined here (not in pi.go) to keep
// the descriptor→backend contract in one file.
func (b *piBackend) applyBuiltinRuntimeOverrides(desc BuiltinRuntime) {
	b.defaultExecutable = desc.DefaultExecutable
	b.providerLabel = desc.ProviderLabel
}

// ResolveBackend is the single production entry point the daemon uses to
// construct a backend from a provider key. It routes built-in runtime
// identities (e.g. "omp") through NewRuntime, and protocol families (e.g.
// "pi", "claude") through New. This keeps both factories meaning exactly one
// thing: New() is family-only, NewRuntime() is runtime-identity-only, and
// the daemon never has to know which is which.
func ResolveBackend(provider string, cfg Config) (Backend, error) {
	if IsBuiltinRuntime(provider) {
		return NewRuntime(provider, cfg)
	}
	return New(provider, cfg)
}

// NewRuntime creates a Backend for a built-in runtime identity (e.g. "omp").
// It builds the descriptor's protocol family backend via New(), then applies
// the per-runtime overrides through the backendOverrideApplicator interface.
// This is the production entry point for runtime identities; New() is
// family-only and rejects runtime ids.
//
// Fails closed: if the protocol family backend does not implement
// backendOverrideApplicator, the descriptor's overrides cannot be applied and
// the runtime would launch under the wrong executable and label — so
// NewRuntime returns an error rather than returning an unmodified backend.
func NewRuntime(runtimeID string, cfg Config) (Backend, error) {
	desc, ok := BuiltinRuntimeByID(runtimeID)
	if !ok {
		return nil, fmt.Errorf("unknown runtime identity: %q", runtimeID)
	}
	cfg.provider = runtimeID
	backend, err := New(desc.ProtocolFamily, cfg)
	if err != nil {
		return nil, fmt.Errorf("runtime %q (family %q): %w", runtimeID, desc.ProtocolFamily, err)
	}
	applicator, ok := backend.(backendOverrideApplicator)
	if !ok {
		return nil, fmt.Errorf("runtime %q: protocol family %q backend %T does not support runtime overrides", runtimeID, desc.ProtocolFamily, backend)
	}
	applicator.applyBuiltinRuntimeOverrides(desc)
	return backend, nil
}
// Admin installations and probes include the user-local and vendor bin directories.
// npm runtimes use system Node/npm. The operator's shell configuration must not affect installs.
func runtimeUserCommand(script string) string {
	script = `set -eu; export PATH="$HOME/.local/bin:$HOME/.kimi-code/bin:$HOME/.grok/bin:/usr/local/bin:/usr/bin:/bin"; cd "$HOME"; mkdir -p "$HOME/.local/bin"; ` + script
	// runuser creates a new session. Put the timeout inside that session so it
	// can terminate the installer and its children before the outer SSH expires.
	return "runuser -u {{user}} -- timeout -k 10s 270s sh -c " + shellQuote(script)
}

func npmRuntimeInstall(pkg, command string) string {
	// Reuse the target user's npm cache for pinned releases. The mutable latest
	// tag still checks the registry so a manual update cannot reuse an old tag.
	return runtimeUserCommand(`command -v node >/dev/null && command -v npm >/dev/null || { echo "Install Node.js and npm in /usr/local/bin or /usr/bin; login-shell and nvm paths are not loaded" >&2; exit 127; }; ` +
		`cache_mode=--prefer-offline; if [ "{{version}}" = latest ]; then cache_mode=--prefer-online; fi; ` +
		`NPM_CONFIG_PREFIX="$HOME/.local" npm install "$cache_mode" --prefix "$HOME/.local" --no-audit --no-fund --fetch-timeout=60000 --fetch-retries=1 -g ` + pkg + `@{{version}}; ` + command + ` --version >/dev/null`)
}

func scriptRuntimeInstall(url, invocation, command string) string {
	// Download completely before execution. A truncated response must never be
	// run, even when curl has already written a valid shell prefix.
	return runtimeUserCommand(`installer=$(mktemp); trap 'rm -f "$installer"' EXIT; curl --connect-timeout 10 --max-time 60 -fsSL ` + url + ` -o "$installer"; ` + invocation + `; ` + command + ` --version >/dev/null`)
}

func ompRuntimeInstall() string {
	return scriptRuntimeInstall("https://raw.githubusercontent.com/can1357/oh-my-pi/main/scripts/install.sh",
		`export PI_INSTALL_DIR="$HOME/.local/bin"; version="{{version}}"; if [ "$version" = latest ]; then sh "$installer" --binary; else sh "$installer" --binary --ref "v${version#v}"; fi`, "omp")
}
