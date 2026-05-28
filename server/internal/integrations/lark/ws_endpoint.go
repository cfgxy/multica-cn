package lark

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// HTTPConnectionTokenFetcher is the production EndpointFetcher. It
// exchanges per-installation app credentials for a short-lived
// WebSocket URL by way of two Lark open-platform calls:
//
//  1. POST /open-apis/auth/v3/tenant_access_token/internal — mint a
//     tenant_access_token. We piggyback on httpAPIClient's cached
//     mechanic by accepting the same APIClient surface; that keeps the
//     token pool in one place and avoids a parallel cache that could
//     drift from /open-apis/im/v1/messages calls happening from the
//     outbound side.
//
//  2. POST /open-apis/event-subscription/v1/connection_token —
//     returns the wss:// URL the connector dials and any headers the
//     server expects on the upgrade request. The URL is single-use
//     and short-TTL; the connector calls Endpoint() once per Run.
//
// We do NOT cache the connection_token. The HTTP /tenant_access_token
// already memoizes the long-lived bearer; the WS token is one-shot by
// design and re-resolving it on every reconnect is the safest behavior
// (a cached one-shot token would mean any retry storm replays the same
// URL and gets rejected, looking like a Lark outage).
//
// The endpoint shape (path / response field names) reflects the
// open-platform docs at the time of writing. Field names that the
// open-platform team renames in the future stay isolated to this file —
// the connector and the Hub do not care.
type HTTPConnectionTokenFetcher struct {
	cfg HTTPConnectionTokenConfig
}

// HTTPConnectionTokenConfig wires the fetcher's dependencies. BaseURL
// defaults to defaultLarkBaseURL; tests substitute an httptest.Server
// URL.
type HTTPConnectionTokenConfig struct {
	BaseURL    string
	HTTPClient *http.Client
	// TokenSource provides the tenant_access_token for the bearer
	// header on the connection_token POST. The production
	// implementation is httpAPIClient (via a small adapter); tests
	// inject a constant.
	TokenSource TenantTokenSource
	Now         func() time.Time
	Logger      *slog.Logger
}

// TenantTokenSource is the narrow contract HTTPConnectionTokenFetcher
// needs. It exists so tests can stub the auth flow without standing up
// a full APIClient.
type TenantTokenSource interface {
	TenantAccessToken(ctx context.Context, creds InstallationCredentials) (string, error)
}

// TenantTokenSourceFunc adapts a function literal to TenantTokenSource.
type TenantTokenSourceFunc func(ctx context.Context, creds InstallationCredentials) (string, error)

// TenantAccessToken implements TenantTokenSource.
func (f TenantTokenSourceFunc) TenantAccessToken(ctx context.Context, creds InstallationCredentials) (string, error) {
	return f(ctx, creds)
}

func (c HTTPConnectionTokenConfig) withDefaults() HTTPConnectionTokenConfig {
	if c.BaseURL == "" {
		c.BaseURL = defaultLarkBaseURL
	}
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: defaultRequestTimeout}
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	return c
}

// NewHTTPConnectionTokenFetcher returns the production EndpointFetcher
// bound to the supplied configuration. A nil TokenSource is rejected
// at construction so call sites do not get a fetcher that panics on
// first use.
func NewHTTPConnectionTokenFetcher(cfg HTTPConnectionTokenConfig) (*HTTPConnectionTokenFetcher, error) {
	if cfg.TokenSource == nil {
		return nil, errors.New("lark ws endpoint: TokenSource is required")
	}
	return &HTTPConnectionTokenFetcher{cfg: cfg.withDefaults()}, nil
}

// Endpoint implements EndpointFetcher.
func (f *HTTPConnectionTokenFetcher) Endpoint(ctx context.Context, creds InstallationCredentials) (WSEndpoint, error) {
	token, err := f.cfg.TokenSource.TenantAccessToken(ctx, creds)
	if err != nil {
		return WSEndpoint{}, fmt.Errorf("lark ws endpoint: tenant token: %w", err)
	}
	body := map[string]string{"app_id": creds.AppID}
	raw, err := json.Marshal(body)
	if err != nil {
		return WSEndpoint{}, fmt.Errorf("marshal body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.cfg.BaseURL+"/open-apis/event-subscription/v1/connection_token", bytes.NewReader(raw))
	if err != nil {
		return WSEndpoint{}, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := f.cfg.HTTPClient.Do(req)
	if err != nil {
		return WSEndpoint{}, fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()
	rawResp, err := io.ReadAll(resp.Body)
	if err != nil {
		return WSEndpoint{}, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return WSEndpoint{}, fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(rawResp), 512))
	}
	var decoded struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rawResp, &decoded); err != nil {
		return WSEndpoint{}, fmt.Errorf("decode response: %w (raw=%s)", err, truncate(string(rawResp), 256))
	}
	if decoded.Code != 0 || decoded.Data.URL == "" {
		return WSEndpoint{}, fmt.Errorf("lark ws endpoint: code=%d msg=%q", decoded.Code, decoded.Msg)
	}
	return WSEndpoint{URL: decoded.Data.URL, Headers: http.Header{}}, nil
}

// HTTPAPIClientTokenSource adapts an *httpAPIClient into a
// TenantTokenSource. We expose this here (rather than make
// httpAPIClient.tenantAccessToken public on the APIClient interface)
// because tenant_access_token retrieval is an implementation detail of
// the HTTP client; only the WS endpoint fetcher needs it, and only
// because the connection_token endpoint takes a bearer.
type HTTPAPIClientTokenSource struct {
	c *httpAPIClient
}

// NewHTTPAPIClientTokenSource returns a TenantTokenSource backed by the
// supplied *httpAPIClient (the same object NewHTTPAPIClient produces).
// Construction panics if the argument is nil because that would defer
// a clear configuration error to runtime.
func NewHTTPAPIClientTokenSource(client APIClient) (TenantTokenSource, error) {
	httpC, ok := client.(*httpAPIClient)
	if !ok {
		return nil, errors.New("lark ws endpoint: TokenSource requires the real httpAPIClient (stub is not usable)")
	}
	return &HTTPAPIClientTokenSource{c: httpC}, nil
}

// TenantAccessToken implements TenantTokenSource.
func (s *HTTPAPIClientTokenSource) TenantAccessToken(ctx context.Context, creds InstallationCredentials) (string, error) {
	return s.c.tenantAccessToken(ctx, creds)
}
