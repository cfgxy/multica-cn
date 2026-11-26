// Package sub2api implements the explicit key-directory contract. Tokens never
// enter directory snapshots, errors, or client caches.
package sub2api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Key struct {
	ID              string   `json:"id"`
	APIKeyName      string   `json:"api_key_name"`
	Status          string   `json:"status"`
	AllowedRuntimes []string `json:"allowed_runtimes"`
}
type Endpoint struct {
	APIType      string `json:"api_type"`
	BaseURL      string `json:"base_url"`
	DefaultModel string `json:"default_model"`
}
type Model struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	APITypes []string `json:"api_types"`
}
type Resolved struct {
	Key
	APIKeyToken string     `json:"api_key_token"`
	BaseURL     string     `json:"base_url"`
	Endpoints   []Endpoint `json:"endpoints"`
	Models      []Model    `json:"models"`
}
type Client struct{ HTTP *http.Client }
type Error struct {
	Code       string
	Status     int
	RetryAfter time.Time
}

func (e *Error) Error() string { return e.Code }
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "source_unavailable"
}
func invalid() error { return &Error{Code: "source_invalid_response"} }

var idPattern = regexp.MustCompile(`^[1-9][0-9]{0,38}$`)

func ValidateBaseURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(raw, "\r\n\t ") {
		return &Error{Code: "source_invalid"}
	}
	return nil
}
func KnownRuntime(s string) bool {
	switch s {
	case "claude", "codex", "opencode", "openclaw", "grok", "kimi", "omp", "pi":
		return true
	}
	return false
}
func KnownProtocol(s string) bool {
	switch s {
	case "anthropic-messages", "openai-responses", "openai-chat-completions":
		return true
	}
	return false
}
func Contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
func (k Key) Allows(runtime string) bool {
	return k.Status == "enabled" && KnownRuntime(runtime) && Contains(k.AllowedRuntimes, runtime)
}

// wireKey preserves integer IDs without a float64 round-trip.
type wireKey struct {
	ID         json.Number `json:"id"`
	APIKeyName string      `json:"api_key_name"`
	Status     string      `json:"status"`
	Platform   *string     `json:"platform"`
	Protocols  []string    `json:"protocols"`
}

func (w wireKey) key() (Key, error) {
	if !idPattern.MatchString(w.ID.String()) || strings.TrimSpace(w.APIKeyName) == "" || len(w.APIKeyName) > 512 || w.Status == "" || w.Platform == nil || ((*w.Platform == "anthropic" || *w.Platform == "openai") && w.Protocols == nil) {
		return Key{}, invalid()
	}
	status := w.Status
	switch status {
	case "active":
		status = "enabled"
	case "inactive":
		status = "disabled"
	default:
		// Unknown upstream statuses never acquire enabled semantics.
		status = "unknown"
	}
	return Key{ID: w.ID.String(), APIKeyName: w.APIKeyName, Status: status, AllowedRuntimes: CompatibleRuntimes(*w.Platform, w.Protocols)}, nil
}

func (c *Client) request(ctx context.Context, base, token, method, path string, payload any, out any) error {
	return c.requestAuthenticated(ctx, base, token, method, path, payload, out, false)
}

func (c *Client) requestAuthenticated(ctx context.Context, base, token, method, path string, payload any, out any, anthropic bool) error {
	if err := ValidateBaseURL(base); err != nil {
		return err
	}
	if strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\r\n\t ") {
		return &Error{Code: "source_invalid"}
	}
	var body io.Reader
	if payload != nil {
		b, e := json.Marshal(payload)
		if e != nil {
			return invalid()
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, body)
	if err != nil {
		return &Error{Code: "source_invalid"}
	}
	if anthropic {
		req.Header.Set("x-api-key", token)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 20 * time.Second}
	if c.HTTP != nil {
		client = *c.HTTP
	}
	// Even trusted directory services cannot redirect a directory credential.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return &Error{Code: "source_unavailable"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		code := "source_unavailable"
		switch resp.StatusCode {
		case 401:
			code = "source_unauthorized"
		case 403:
			code = "key_forbidden"
		case 404:
			code = "key_missing"
		case 409:
			code = "key_ambiguous"
		case 429:
			code = "source_rate_limited"
		}
		retryAt := time.Now().Add(60 * time.Second)
		if resp.StatusCode == 429 {
			value := resp.Header.Get("Retry-After")
			if seconds, e := strconv.ParseInt(value, 10, 32); e == nil && seconds > 0 {
				retryAt = time.Now().Add(time.Duration(seconds) * time.Second)
			} else if date, e := http.ParseTime(value); e == nil && date.After(time.Now()) {
				retryAt = date
			}
		}
		return &Error{Code: code, Status: resp.StatusCode, RetryAfter: retryAt}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(b) > 2<<20 {
		return invalid()
	}
	if json.Unmarshal(b, out) != nil {
		return invalid()
	}
	return nil
}

func (c *Client) ListAll(ctx context.Context, base, token string) ([]Key, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	items := []Key{}
	seen := map[string]bool{}
	total := -1
	for page := 1; page <= 1000; page++ {
		var envelope struct {
			Data *struct {
				Items    []wireKey `json:"items"`
				Page     int       `json:"page"`
				PageSize int       `json:"page_size"`
				Total    *int      `json:"total"`
			} `json:"data"`
		}
		if err := c.request(ctx, base, token, http.MethodGet, "/api/v1/key-directory?page="+strconv.Itoa(page)+"&page_size=100", nil, &envelope); err != nil {
			return nil, err
		}
		p := envelope.Data
		if p == nil || p.Items == nil || p.Total == nil || *p.Total < 0 || *p.Total > 100000 || p.Page != page || p.PageSize < 1 || p.PageSize > 100 || len(p.Items) > p.PageSize {
			return nil, invalid()
		}
		if total < 0 {
			total = *p.Total
		}
		if *p.Total != total {
			return nil, invalid()
		}
		for _, w := range p.Items {
			k, e := w.key()
			if e != nil {
				return nil, e
			}
			if seen[k.ID] {
				return nil, invalid()
			}
			seen[k.ID] = true
			items = append(items, k)
		}
		if len(items) == total {
			return items, nil
		}
		if len(items) > total || len(p.Items) < p.PageSize {
			return nil, invalid()
		}
	}
	return nil, invalid()
}

// Resolve retrieves credentials and declared platform/protocol capabilities.
// Models are discovered separately after the caller rechecks authorization.
func (c *Client) Resolve(ctx context.Context, base, token, id, name string) (Resolved, error) {
	var result Resolved
	if !idPattern.MatchString(id) {
		return result, invalid()
	}
	var envelope struct {
		Data *struct {
			wireKey
			APIKeyToken string `json:"api_key_token"`
			BaseURL     string `json:"base_url"`
		} `json:"data"`
	}
	err := c.request(ctx, base, token, http.MethodPost, "/api/v1/key-directory/resolve", map[string]any{"id": json.Number(id), "api_key_name": name}, &envelope)
	if err != nil {
		return result, err
	}
	w := envelope.Data
	if w == nil {
		return result, invalid()
	}
	key, err := w.wireKey.key()
	if err != nil || key.ID != id || key.APIKeyName != name || key.Status != "enabled" || w.APIKeyToken == "" || len(w.APIKeyToken) > 16384 || strings.ContainsAny(w.APIKeyToken, "\r\n\x00") || ValidateBaseURL(w.BaseURL) != nil || len(w.Protocols) == 0 || len(key.AllowedRuntimes) == 0 {
		return result, invalid()
	}
	// sub2api supplies one service root, not a protocol-specific SDK URL.
	root, _ := url.Parse(w.BaseURL)
	if root.Path != "" && root.Path != "/" {
		return result, invalid()
	}
	baseURL := strings.TrimRight(w.BaseURL, "/")
	endpoints := []Endpoint{}
	seen := map[string]bool{}
	for _, protocol := range w.Protocols {
		if !platformProtocol(*w.Platform, protocol) {
			continue
		}
		apiType, endpointURL := "", baseURL+"/v1"
		switch protocol {
		case "anthropic_messages":
			apiType, endpointURL = "anthropic-messages", baseURL
		case "openai_responses":
			apiType = "openai-responses"
		case "openai_chat_completions":
			apiType = "openai-chat-completions"
		default:
			return result, invalid()
		}
		if seen[apiType] {
			return result, invalid()
		}
		seen[apiType] = true
		endpoints = append(endpoints, Endpoint{APIType: apiType, BaseURL: endpointURL})
	}
	return Resolved{Key: key, APIKeyToken: w.APIKeyToken, BaseURL: baseURL, Endpoints: endpoints}, nil
}
