package llm

// ErrorKind (RUYI-551) is the stable vocabulary the self-evolution module
// uses to name gateway failures: run records and validation responses carry
// the kind, so the UI can render the four-state closed loop (未配置 / 凭据
// 无效 / 连接失败 / 模型不可用) without any caller needing to inspect — or
// log — SDK error internals or credential material. It lives here because
// only this package may touch the SDK's error type.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	openai "github.com/openai/openai-go/v3"
)

// ErrorKind classifies an error from this package's call surface.
type ErrorKind string

const (
	// ErrorKindNone means "no error" — the zero question got a zero answer.
	ErrorKindNone ErrorKind = ""
	// ErrorKindNotConfigured: the client was constructed without credentials
	// (ErrNotConfigured). No request ever left.
	ErrorKindNotConfigured ErrorKind = "not_configured"
	// ErrorKindCredentials: the gateway rejected the authentication (401/403).
	ErrorKindCredentials ErrorKind = "credentials"
	// ErrorKindModel: the gateway understood us but refused the request
	// itself — unknown model name, malformed request (4xx other than auth).
	ErrorKindModel ErrorKind = "model"
	// ErrorKindConnection: the network path or the gateway's own health
	// failed — timeouts, refused dials, 5xx.
	ErrorKindConnection ErrorKind = "connection"
	// ErrorKindOther: anything the kinds above cannot name (e.g. the
	// upstream replied 200 with unparseable content).
	ErrorKindOther ErrorKind = "other"
)

// Classify maps err onto the vocabulary above. nil maps to ErrorKindNone.
// The kind survives fmt.Errorf-style wrapping; unrecognized errors are
// ErrorKindOther, never a panic and never a guess upward.
func Classify(err error) ErrorKind {
	if err == nil {
		return ErrorKindNone
	}
	if errors.Is(err, ErrNotConfigured) {
		return ErrorKindNotConfigured
	}
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden:
			return ErrorKindCredentials
		case apiErr.StatusCode >= 500:
			return ErrorKindConnection
		case apiErr.StatusCode >= 400:
			return ErrorKindModel
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ErrorKindConnection
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return ErrorKindConnection
	}
	return ErrorKindOther
}

// Summary renders err as a short, user-displayable reason: the classified
// category plus, for gateway responses, only the HTTP status code. It
// deliberately omits the response body and the endpoint URL — either could
// carry hostile or internal detail into stored validation messages and the
// UI — so user-facing text must come from here, never from err.Error().
func Summary(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrNotConfigured) {
		return "未配置凭据"
	}
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden:
			return fmt.Sprintf("网关拒绝凭据（HTTP %d）", apiErr.StatusCode)
		case apiErr.StatusCode >= 500:
			return fmt.Sprintf("网关服务异常（HTTP %d）", apiErr.StatusCode)
		case apiErr.StatusCode >= 400:
			return fmt.Sprintf("网关拒绝请求（HTTP %d）", apiErr.StatusCode)
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "连接超时"
	}
	if errors.Is(err, context.Canceled) {
		return "请求已取消"
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return "网络连接失败"
	}
	return "网关返回异常响应"
}
