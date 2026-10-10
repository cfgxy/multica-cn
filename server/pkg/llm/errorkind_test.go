package llm

// Tests for ErrorKind (RUYI-551): the self-evolution module's four-state
// closed loop needs one stable vocabulary for gateway failures —
// not_configured / credentials / model / connection / other — that run
// records and validation responses can carry without the caller ever seeing
// credential material. The classification lives here because only this
// package may touch the SDK's error type.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"

	openai "github.com/openai/openai-go/v3"
)

func TestErrorKindClassifiesSentinelAndUnknown(t *testing.T) {
	if got := Classify(nil); got != ErrorKindNone {
		t.Fatalf("Classify(nil) = %q, want %q", got, ErrorKindNone)
	}
	if got := Classify(ErrNotConfigured); got != ErrorKindNotConfigured {
		t.Fatalf("Classify(ErrNotConfigured) = %q, want %q", got, ErrorKindNotConfigured)
	}
	if got := Classify(fmt.Errorf("wrapped: %w", ErrNotConfigured)); got != ErrorKindNotConfigured {
		t.Fatalf("wrapped ErrNotConfigured = %q, want %q", got, ErrorKindNotConfigured)
	}
	if got := Classify(errors.New("something exploded")); got != ErrorKindOther {
		t.Fatalf("plain error = %q, want %q", got, ErrorKindOther)
	}
}

func TestErrorKindClassifiesAPIErrorsByStatus(t *testing.T) {
	cases := []struct {
		status int
		want   ErrorKind
	}{
		{http.StatusUnauthorized, ErrorKindCredentials},
		{http.StatusForbidden, ErrorKindCredentials},
		{http.StatusBadRequest, ErrorKindModel},
		{http.StatusNotFound, ErrorKindModel},
		{http.StatusUnprocessableEntity, ErrorKindModel},
		{http.StatusInternalServerError, ErrorKindConnection},
		{http.StatusBadGateway, ErrorKindConnection},
		{http.StatusServiceUnavailable, ErrorKindConnection},
	}
	for _, tc := range cases {
		err := &openai.Error{StatusCode: tc.status, Message: "upstream said no"}
		if got := Classify(err); got != tc.want {
			t.Fatalf("status %d: got %q, want %q", tc.status, got, tc.want)
		}
		// The kind survives wrapping, the way callers naturally receive it.
		wrapped := fmt.Errorf("generate: %w", err)
		if got := Classify(wrapped); got != tc.want {
			t.Fatalf("status %d wrapped: got %q, want %q", tc.status, got, tc.want)
		}
	}
}

func TestErrorKindClassifiesNetworkAndDeadlineAsConnection(t *testing.T) {
	deadline := &timeoutError{}
	if got := Classify(deadline); got != ErrorKindConnection {
		t.Fatalf("context deadline = %q, want %q", got, ErrorKindConnection)
	}
	if got := Classify(context.Canceled); got != ErrorKindConnection {
		t.Fatalf("context canceled = %q, want %q", got, ErrorKindConnection)
	}
	if got := Classify(&net.OpError{Op: "dial", Err: errors.New("connection refused")}); got != ErrorKindConnection {
		t.Fatalf("net error = %q, want %q", got, ErrorKindConnection)
	}
}

type timeoutError struct{}

func (*timeoutError) Error() string { return context.DeadlineExceeded.Error() }
func (*timeoutError) Is(target error) bool {
	return target == context.DeadlineExceeded
}
func (*timeoutError) Timeout() bool    { return true }
func (*timeoutError) Temporary() bool  { return true }

// Summary is the user-facing counterpart of Classify (RUYI-551 P2-2): the
// category in user language plus at most the HTTP status — never the body
// or URL the SDK error carries.
func TestSummaryNamesCategoryAndStatusOnly(t *testing.T) {
	if got := Summary(nil); got != "" {
		t.Fatalf("Summary(nil) = %q, want empty", got)
	}
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"not configured", ErrNotConfigured, "未配置凭据"},
		{"401", &openai.Error{StatusCode: http.StatusUnauthorized, Message: "upstream said no"}, "网关拒绝凭据（HTTP 401）"},
		{"403", &openai.Error{StatusCode: http.StatusForbidden}, "网关拒绝凭据（HTTP 403）"},
		{"400", &openai.Error{StatusCode: http.StatusBadRequest, Message: "bad model"}, "网关拒绝请求（HTTP 400）"},
		{"502", &openai.Error{StatusCode: http.StatusBadGateway}, "网关服务异常（HTTP 502）"},
		{"deadline", &timeoutError{}, "连接超时"},
		{"canceled", context.Canceled, "请求已取消"},
		{"net", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, "网络连接失败"},
		{"other", errors.New("something exploded"), "网关返回异常响应"},
	}
	for _, tc := range cases {
		if got := Summary(tc.err); got != tc.want {
			t.Fatalf("%s: Summary = %q, want %q", tc.name, got, tc.want)
		}
	}
	// The hostile body must not survive into the summary, even wrapped the
	// way callers receive it.
	hostile := fmt.Errorf("generate: %w", &openai.Error{
		StatusCode: http.StatusUnauthorized,
		Message:    `{"secret":"INTERNAL_TOKEN_REJECTED","url":"/v1/chat/completions"}`,
	})
	if got := Summary(hostile); strings.Contains(got, "INTERNAL_TOKEN_REJECTED") {
		t.Fatalf("summary carries the response body: %q", got)
	}
}
