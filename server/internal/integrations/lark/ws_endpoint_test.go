package lark

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPConnectionTokenFetcherEndpointSuccess(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/open-apis/event-subscription/v1/connection_token" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok-123" {
			t.Errorf("Authorization = %q", got)
		}
		_, _ = io.WriteString(w, `{"code":0,"data":{"url":"wss://lark.example/test"}}`)
	}))
	defer srv.Close()

	f, err := NewHTTPConnectionTokenFetcher(HTTPConnectionTokenConfig{
		BaseURL: srv.URL,
		TokenSource: TenantTokenSourceFunc(func(ctx context.Context, creds InstallationCredentials) (string, error) {
			return "tok-123", nil
		}),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	ep, err := f.Endpoint(context.Background(), InstallationCredentials{AppID: "cli_x", AppSecret: "s"})
	if err != nil {
		t.Fatalf("Endpoint: %v", err)
	}
	if ep.URL != "wss://lark.example/test" {
		t.Errorf("URL = %q", ep.URL)
	}
}

func TestHTTPConnectionTokenFetcherSurfacesLarkErrorCode(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"code":99991663,"msg":"token expired"}`)
	}))
	defer srv.Close()

	f, _ := NewHTTPConnectionTokenFetcher(HTTPConnectionTokenConfig{
		BaseURL: srv.URL,
		TokenSource: TenantTokenSourceFunc(func(context.Context, InstallationCredentials) (string, error) {
			return "tok", nil
		}),
	})
	_, err := f.Endpoint(context.Background(), InstallationCredentials{AppID: "a"})
	if err == nil {
		t.Fatal("expected error for non-zero Lark code")
	}
}

func TestHTTPConnectionTokenFetcherWrapsTokenSourceError(t *testing.T) {
	t.Parallel()
	src := errors.New("token decryption broke")
	f, _ := NewHTTPConnectionTokenFetcher(HTTPConnectionTokenConfig{
		BaseURL: "http://unused",
		TokenSource: TenantTokenSourceFunc(func(context.Context, InstallationCredentials) (string, error) {
			return "", src
		}),
	})
	_, err := f.Endpoint(context.Background(), InstallationCredentials{AppID: "a"})
	if err == nil || !errors.Is(err, src) {
		t.Fatalf("expected wrapped TokenSource error, got %v", err)
	}
}

func TestHTTPConnectionTokenFetcherRejectsHTTPErrorStatus(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `boom`)
	}))
	defer srv.Close()

	f, _ := NewHTTPConnectionTokenFetcher(HTTPConnectionTokenConfig{
		BaseURL: srv.URL,
		TokenSource: TenantTokenSourceFunc(func(context.Context, InstallationCredentials) (string, error) {
			return "tok", nil
		}),
	})
	_, err := f.Endpoint(context.Background(), InstallationCredentials{AppID: "a"})
	if err == nil {
		t.Fatal("expected error on 5xx")
	}
}

func TestNewHTTPConnectionTokenFetcherRequiresTokenSource(t *testing.T) {
	t.Parallel()
	if _, err := NewHTTPConnectionTokenFetcher(HTTPConnectionTokenConfig{}); err == nil {
		t.Fatal("expected error when TokenSource is nil")
	}
}

func TestNewHTTPAPIClientTokenSourceRejectsStub(t *testing.T) {
	t.Parallel()
	if _, err := NewHTTPAPIClientTokenSource(NewStubAPIClient(nil)); err == nil {
		t.Fatal("expected error when wrapping the stub APIClient")
	}
}
