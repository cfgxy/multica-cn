package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/internal/testutil"
)

type runtimeCacheTestTransport func(*http.Request) (*http.Response, error)

func (f runtimeCacheTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestRuntimeDownloadCapabilityAndSourceBoundaries(t *testing.T) {
	t.Setenv("MULTICA_COMPUTER_SERVER_URL", "https://multica.invalid")
	_, binding := operationBinding(t)
	op, err := testHandler.beginBindingOperation(context.Background(), testUserID, binding, "runtime_install", "codex", "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `UPDATE computer_operation SET state='running' WHERE id=$1`, op)
	client := &http.Client{Transport: runtimeCacheTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/tool/latest" && r.URL.Path != "/@openai/codex" && r.URL.Path != "/@openai/codex/-/codex-1.2.3.tgz" {
			t.Errorf("unexpected upstream package path: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"version":"1.2.3","dist":{"tarball":"https://registry.npmjs.org/tool/-/tool.tgz"}}`))}, nil
	})}
	h := &Handler{DB: testHandler.DB, runtimeDownloads: computer.NewRuntimeDownloadCache(t.TempDir(), client)}
	router := chi.NewRouter()
	router.Get("/api/runtime-downloads/{operation}/{expiry}/{signature}/{source}/*", h.RuntimeDownload)
	base := runtimeDownloadBase("", op, time.Now())
	call := func(path string, status int) *httptest.ResponseRecorder {
		t.Helper()
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
		if rr.Code != status {
			t.Fatalf("response %d, want %d: %s", rr.Code, status, rr.Body.String())
		}
		return rr
	}
	result := call(base+"/registry.npmjs.org/tool/latest", 200)
	if !strings.Contains(result.Body.String(), base+"/registry.npmjs.org/tool/-/tool.tgz") {
		t.Fatal("package tarball did not use server cache")
	}
	call(base+"/registry.npmjs.org/@openai%2fcodex", 200)
	call(base+"/registry.npmjs.org/@openai/codex", 200)
	call(base+"/registry.npmjs.org/@openai%2Fcodex/-/codex-1.2.3.tgz", 200)
	call(base+"/github.com/can1357/oh-my-pi/releases/latest", 403)
	call(base+"/registry.npmjs.org/../private", 403)
	call(base+"/registry.npmjs.org/%2e%2e/private", 403)
	call(strings.Replace(base, "/"+op+"/", "/10000000-0000-0000-0000-000000000099/", 1)+"/registry.npmjs.org/tool/latest", 401)
	dbfx.Exec(t, `UPDATE computer_operation SET state='succeeded',finished_at=now() WHERE id=$1`, op)
	call(base+"/registry.npmjs.org/tool/latest", 403)
	expiry := strconv.FormatInt(time.Now().Add(-time.Second).Unix(), 10)
	if validRuntimeDownloadSignature(op, expiry, runtimeDownloadSignature(op, expiry), time.Now()) {
		t.Fatal("accepted expired capability")
	}
}
func TestRuntimeCacheAdminGateAndActiveInstallClear(t *testing.T) {
	t.Setenv("MULTICA_INSTANCE_ADMIN_IDS", testUserID)
	h := &Handler{DB: testHandler.DB, runtimeDownloads: computer.NewRuntimeDownloadCache(t.TempDir(), &http.Client{})}
	req := computerTestRequest("DELETE", "/api/admin/runtime-cache", nil)
	req.Header.Set("X-User-ID", "10000000-0000-0000-0000-000000000099")
	testutil.Call(t, h.AdminRuntimeCacheClear, req).Want(403)
	_, binding := operationBinding(t)
	if _, err := testHandler.beginBindingOperation(context.Background(), testUserID, binding, "runtime_install", "codex", "1.2.3"); err != nil {
		t.Fatal(err)
	}
	testutil.Call(t, h.AdminRuntimeCacheClear, computerTestRequest("DELETE", "/api/admin/runtime-cache", nil)).Want(409)
}

func TestCachedRuntimeInstallCommandUsesServerRegistry(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"runuser": `shift 3; exec "$@"`, "node": "exit 0", "npm": `printf '%s\n' "$@" > "$HOME/npm-args"`, "codex": "exit 0"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	var target runtimeTarget
	for _, candidate := range runtimeTargets() {
		if candidate.id == "codex" {
			target = candidate
		}
	}
	base := "https://multica.invalid/api/runtime-downloads/operation/expiry/signature"
	command := cachedRuntimeInstallCommand(target, base, "alice", "1.2.3")
	command = strings.ReplaceAll(command, ":/usr/local/bin:/usr/bin:/bin", ":"+dir+":/usr/bin:/bin")
	cmd := exec.Command("sh", "-c", command)
	cmd.Env = []string{"HOME=" + dir, "PATH=" + dir + ":/usr/bin:/bin"}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install command: %v: %s", err, out)
	}
	args, err := os.ReadFile(filepath.Join(dir, "npm-args"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "--registry\n"+base+"/registry.npmjs.org/\n") || !strings.Contains(string(args), "@openai/codex@1.2.3\n") {
		t.Fatalf("wrong registry or package arguments: %s", args)
	}
}

func TestRuntimeDownloadKimiCDNCapability(t *testing.T) {
	t.Setenv("MULTICA_COMPUTER_SERVER_URL", "https://multica.invalid")
	_, binding := operationBinding(t)
	op, err := testHandler.beginBindingOperation(context.Background(), testUserID, binding, "runtime_install", "kimi", "latest")
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `UPDATE computer_operation SET state='running' WHERE id=$1`, op)
	client := &http.Client{Transport: runtimeCacheTestTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/plain"}}, Body: io.NopCloser(strings.NewReader("curl https://cdn.kimi.com/kimi-code/latest"))}, nil
	})}
	h := &Handler{DB: testHandler.DB, runtimeDownloads: computer.NewRuntimeDownloadCache(t.TempDir(), client)}
	router := chi.NewRouter()
	router.Get("/api/runtime-downloads/{operation}/{expiry}/{signature}/{source}/*", h.RuntimeDownload)
	base := runtimeDownloadBase("", op, time.Now())
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/cdn.kimi.com/kimi-code/install.sh", 200},
		{"/cdn.kimi.com/private/install.sh", 403},
		{"/cdn.kimi.com/kimi-code/%2e%2e/private", 403},
		{"/registry.npmjs.org/tool", 403},
	} {
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, httptest.NewRequest("GET", base+tc.path, nil))
		if rr.Code != tc.status {
			t.Fatalf("%s: got %d want %d", tc.path, rr.Code, tc.status)
		}
		if tc.status == 200 && !strings.Contains(rr.Body.String(), base+"/cdn.kimi.com/kimi-code/latest") {
			t.Fatal("CDN URL bypassed cache")
		}
	}
	dbfx.Exec(t, `UPDATE computer_operation SET runtime_id='codex' WHERE id=$1`, op)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest("GET", base+"/cdn.kimi.com/kimi-code/install.sh", nil))
	if rr.Code != 403 {
		t.Fatal("accepted Kimi CDN for different runtime")
	}
}

func TestRuntimeDownloadGrokPublicSourcesAndRanges(t *testing.T) {
	t.Setenv("MULTICA_COMPUTER_SERVER_URL", "https://multica.invalid")
	_, binding := operationBinding(t)
	op, err := testHandler.beginBindingOperation(context.Background(), testUserID, binding, "runtime_install", "grok", "latest")
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `UPDATE computer_operation SET state='running' WHERE id=$1`, op)
	body := "BINARY https://x.ai/cli/ unchanged"
	calls := 0
	client := &http.Client{Transport: runtimeCacheTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/octet-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	h := &Handler{DB: testHandler.DB, runtimeDownloads: computer.NewRuntimeDownloadCache(t.TempDir(), client)}
	router := chi.NewRouter()
	router.Get("/api/runtime-downloads/{operation}/{expiry}/{signature}/{source}/*", h.RuntimeDownload)
	router.Head("/api/runtime-downloads/{operation}/{expiry}/{signature}/{source}/*", h.RuntimeDownload)
	base := runtimeDownloadBase("", op, time.Now())
	for _, source := range []string{"/x.ai/cli", "/storage.googleapis.com/grok-build-public-artifacts/cli"} {
		path := base + source + "/grok-1.0.41-linux-x86_64.gz"
		for _, method := range []string{"HEAD", "GET"} {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(method, path, nil)
			if method == "GET" {
				req.Header.Set("Range", "bytes=0-5")
			}
			router.ServeHTTP(rr, req)
			if method == "HEAD" {
				if rr.Code != 200 || rr.Header().Get("Content-Length") != strconv.Itoa(len(body)) || rr.Body.Len() != 0 {
					t.Fatalf("invalid artifact HEAD: %d %v", rr.Code, rr.Header())
				}
			} else if rr.Code != 206 || rr.Body.String() != "BINARY" {
				t.Fatalf("corrupted range: %d %q", rr.Code, rr.Body.String())
			}
		}
	}
	if calls != 2 {
		t.Fatalf("range requests bypassed cache: %d upstream requests", calls)
	}
	for _, source := range []string{"/storage.googleapis.com/unrelated-bucket/cli/file", "/registry.npmjs.org/tool"} {
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, httptest.NewRequest("GET", base+source, nil))
		if rr.Code != 403 {
			t.Fatalf("accepted unrelated source %s", source)
		}
	}
	dbfx.Exec(t, `UPDATE computer_operation SET runtime_id='codex' WHERE id=$1`, op)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest("GET", base+"/storage.googleapis.com/grok-build-public-artifacts/cli/stable", nil))
	if rr.Code != 403 {
		t.Fatal("accepted Grok source for another runtime")
	}
}
