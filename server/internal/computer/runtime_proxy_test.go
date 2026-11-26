package computer

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestRuntimeDownloadProxyUsesConnect(t *testing.T) {
	requests := make(chan *http.Request, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Clone(r.Context())
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	client := NewRuntimeDownloadClient(proxy.URL)
	defer client.CloseIdleConnections()
	response, err := client.Get("https://x.ai/cli/install.sh")
	if response != nil {
		response.Body.Close()
	}
	if err == nil {
		t.Fatal("ignored proxy failure")
	}
	select {
	case req := <-requests:
		if req.Method != "CONNECT" || req.Host != "x.ai:443" {
			t.Fatalf("unexpected proxy request: %s %s", req.Method, req.Host)
		}
	default:
		t.Fatal("request bypassed configured proxy")
	}
}

func TestRuntimeDownloadProxyRejectsInvalidConfigurationWithoutLeakingCredentials(t *testing.T) {
	for _, address := range []string{"47.86.161.114", "ftp://user:secret@proxy.invalid", "http://user:secret@proxy.invalid/path", "http://user:secret@proxy.invalid:99999", "http://user:secret@proxy.invalid?token=secret", "http://%secret"} {
		client := NewRuntimeDownloadClient(address)
		response, err := client.Get("https://x.ai/cli/install.sh")
		if response != nil {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
		client.CloseIdleConnections()
		if err == nil || !strings.Contains(err.Error(), "invalid MULTICA_RUNTIME_DOWNLOAD_PROXY configuration") || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe or missing configuration error: %v", err)
		}
	}
}

func TestRuntimeDownloadProxySupportsExplicitProtocolsWithoutChangingDefaultTransport(t *testing.T) {
	defaultTransport := http.DefaultTransport
	req := &http.Request{URL: &url.URL{Scheme: "https", Host: "x.ai"}}
	before, beforeErr := defaultTransport.(*http.Transport).Proxy(req)
	for _, scheme := range []string{"http", "https", "socks5", "socks5h"} {
		address := scheme + "://user:secret@proxy.invalid:1080"
		client := NewRuntimeDownloadClient(address)
		got, err := client.Transport.(*http.Transport).Proxy(req)
		if err != nil || got.String() != address {
			t.Fatalf("unsupported %s proxy", scheme)
		}
		if client.Timeout != RuntimeDownloadTimeout {
			t.Fatal("lost cache deadline")
		}
		client.CloseIdleConnections()
	}
	client := NewRuntimeDownloadClient("")
	defer client.CloseIdleConnections()
	after, afterErr := client.Transport.(*http.Transport).Proxy(req)
	if client.Transport == defaultTransport || http.DefaultTransport != defaultTransport || beforeErr != afterErr || before != after {
		t.Fatal("changed default environment proxy behavior")
	}
}
