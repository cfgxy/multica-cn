package retrospective

// Workspace LLM resolution (RUYI-552). The self-evolution UI saves a
// workspace-scoped LLM config onto retrospective_config (base URL, model,
// API key sealed with the server secret box); "run now" and the scheduler
// resolve that config first and fall back to the deployment env defaults
// per field. Nothing here returns the plaintext key to an HTTP response —
// the sealed bytes go in, the resolved struct stays server-side, and the
// handler projects only masked hints.

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/internal/util/secretbox"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// DeploymentLLM carries the deployment-level LLM defaults (MULTICA_LLM_* as
// read at boot). They are the per-field fallback when the workspace has not
// saved its own value.
type DeploymentLLM struct {
	APIKey       string
	BaseURL      string
	DefaultModel string
}

// LLMResolver resolves one workspace's effective LLM client for a run pass:
// nil when the workspace has no effective config (the run records the
// disabled state), plus the secrets to redact from stored run errors. The
// scheduler holds this type without importing pkg/llm; the API server builds
// the closure (Handler.RetrospectiveLLMResolver).
type LLMResolver func(ctx context.Context, workspaceID string) (client LLMClient, redact []string)

// Where each resolved field came from. "default" means a built-in (the SDK's
// OpenAI endpoint, llm.FallbackModel), not user-supplied.
const (
	LLMSourceWorkspace  = "workspace"
	LLMSourceDeployment = "deployment"
	LLMSourceDefault    = "default"
	LLMSourceNone       = "none"
)

// ResolvedLLM is the effective LLM config for one workspace after fallback.
// Effective matches llm.New's own predicate (a key alone or a base URL alone
// is enough — keyless local gateways are a supported shape), so "effective"
// here and "the client will actually dial" never disagree. Issue is set when
// a saved workspace key exists but cannot be used (secret box missing or the
// ciphertext fails authentication): the config is then NOT effective and the
// message names the reason, instead of silently running on a different
// deployment's key.
type ResolvedLLM struct {
	APIKey      string
	BaseURL     string
	Model       string
	KeySource   string
	URLSource   string
	ModelSource string
	// Source is the coarse origin the status badge shows: "workspace" when
	// any workspace-saved field participates, else the deployment fallback,
	// else "none".
	Source    string
	Effective bool
	Issue     string
}

// ResolveWorkspaceLLM loads the workspace's saved LLM config, opens the
// sealed key with box (nil box + saved key → Issue), and applies the
// deployment defaults to every field the workspace left empty.
func ResolveWorkspaceLLM(ctx context.Context, q *db.Queries, box *secretbox.Box, workspaceID string, env DeploymentLLM) ResolvedLLM {
	res := ResolvedLLM{
		APIKey:      env.APIKey,
		BaseURL:     env.BaseURL,
		Model:       env.DefaultModel,
		KeySource:   sourceFor(env.APIKey, LLMSourceNone),
		URLSource:   sourceFor(env.BaseURL, LLMSourceDefault),
		ModelSource: sourceFor(env.DefaultModel, LLMSourceDefault),
	}

	cfg, err := q.GetRetrospectiveConfig(ctx, util.MustParseUUID(workspaceID))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		// A database failure is an Issue too: the run must not silently
		// pretend the workspace has no opinion.
		res.Issue = "读取复盘 LLM 配置失败"
		slog.Error("retrospective: read config for LLM resolution failed", "workspace_id", workspaceID, "error", err)
		return res
	}
	if errors.Is(err, pgx.ErrNoRows) {
		res.finish()
		return res
	}

	if cfg.LlmBaseUrl != "" {
		res.BaseURL = cfg.LlmBaseUrl
		res.URLSource = LLMSourceWorkspace
	}
	if cfg.LlmModel != "" {
		res.Model = cfg.LlmModel
		res.ModelSource = LLMSourceWorkspace
	}
	if len(cfg.LlmApiKeyEncrypted) > 0 {
		if box == nil {
			res.Issue = "服务器未配置凭据加密（MULTICA_RUNTIME_CREDENTIAL_SECRET_KEY 缺失），已保存的 LLM API Key 无法读取"
		} else {
			plaintext, openErr := box.Open(cfg.LlmApiKeyEncrypted)
			switch {
			case openErr != nil:
				res.Issue = "已保存的 LLM API Key 解密失败（密钥可能已轮换），请重新保存"
				slog.Error("retrospective: open workspace llm api key failed", "workspace_id", workspaceID, "error", openErr)
			case len(plaintext) > 0:
				res.APIKey = string(plaintext)
				res.KeySource = LLMSourceWorkspace
			}
		}
	}
	res.finish()
	return res
}

// finish derives the coarse source and the effective predicate. Called once
// after all per-field decisions.
func (r *ResolvedLLM) finish() {
	switch {
	case r.Issue != "":
		r.Effective = false
		r.Source = LLMSourceNone
	case r.KeySource == LLMSourceWorkspace || r.URLSource == LLMSourceWorkspace || r.ModelSource == LLMSourceWorkspace:
		r.Source = LLMSourceWorkspace
		r.Effective = r.APIKey != "" || r.BaseURL != ""
	case r.KeySource == LLMSourceDeployment || r.URLSource == LLMSourceDeployment:
		r.Source = LLMSourceDeployment
		r.Effective = r.APIKey != "" || r.BaseURL != ""
	default:
		r.Source = LLMSourceNone
		r.Effective = false
	}
}

func sourceFor(v, empty string) string {
	if v != "" {
		return LLMSourceDeployment
	}
	return empty
}

// APIKeyHint returns the last 4 characters of key — the only key surface any
// response ever carries. Empty when the key is shorter than that.
func APIKeyHint(key string) string {
	if len(key) < 4 {
		return ""
	}
	return key[len(key)-4:]
}
