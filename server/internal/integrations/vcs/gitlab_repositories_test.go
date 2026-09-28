package vcs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestListGitLabRepositories(t *testing.T) {
	const token = "private-test-token"
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("request escaped the GitLab origin")
	}))
	defer other.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/projects" || r.Header.Get("PRIVATE-TOKEN") != token || r.URL.Query().Get("simple") != "true" || r.URL.Query().Get("order_by") != "id" || r.URL.Query().Get("sort") != "asc" {
			t.Errorf("unexpected projects request: %s", r.URL.Path)
		}
		switch r.URL.Query().Get("page") {
		case "1":
			w.Header().Set("X-Next-Page", "2")
			fmt.Fprint(w, `[{"id":1,"path_with_namespace":"alpha/app","ssh_url_to_repo":"git@git.test:alpha/app.git","visibility":"private","archived":false,"description":"first"},{"id":2,"path_with_namespace":"beta/app","http_url_to_repo":"https://git.test/beta/app.git","visibility":"public","archived":true}]`)
		case "2":
			fmt.Fprint(w, `[]`)
		case "401":
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, token)
		case "429":
			w.WriteHeader(http.StatusTooManyRequests)
		case "500":
			w.WriteHeader(http.StatusInternalServerError)
		case "3":
			fmt.Fprint(w, `{"not":"an array"}`)
		case "4":
			fmt.Fprint(w, strings.Repeat(" ", 2<<20))
		case "5":
			http.Redirect(w, r, other.URL, http.StatusFound)
		case "6":
			<-r.Context().Done()
		case "7":
			fmt.Fprint(w, `[{"id":7,"path_with_namespace":"team/fallback","ssh_url_to_repo":"git@git.test:/unsafe.git","http_url_to_repo":"https://git.test/team/fallback.git","visibility":"private","archived":false},{"id":8,"path_with_namespace":"team/unsafe","ssh_url_to_repo":"https://user:secret@git.test/a.git","http_url_to_repo":"https://git.test/a.git?token=secret","visibility":"private","archived":false}]`)
		case "8":
			fmt.Fprint(w, `[{"id":9,"path_with_namespace":"team/archived","visibility":"private"}]`)
		case "10":
			// 首条项目合法、第二条缺少 archived，校验失败必须丢弃已累加的结果。
			w.Header().Set("X-Next-Page", "11")
			fmt.Fprint(w, `[{"id":10,"path_with_namespace":"team/valid","ssh_url_to_repo":"git@git.test:team/valid.git","visibility":"private","archived":false},{"id":11,"path_with_namespace":"team/broken","visibility":"private"}]`)
		case "9":
			next := *r.URL
			query := next.Query()
			query.Set("page", "2")
			next.RawQuery = query.Encode()
			http.Redirect(w, r, next.String(), http.StatusTemporaryRedirect)
		}
	}))
	defer server.Close()

	page, err := ListGitLabRepositories(context.Background(), server.URL, token, 1, 20)
	if err != nil || page.NextPage != 2 || len(page.Repositories) != 2 || page.Repositories[0].FullName != "alpha/app" || !page.Repositories[0].Private || page.Repositories[1].FullName != "beta/app" || !page.Repositories[1].Archived {
		t.Fatalf("first page: %+v, %v", page, err)
	}
	page, err = ListGitLabRepositories(context.Background(), server.URL, token, 2, 20)
	if err != nil || len(page.Repositories) != 0 || page.NextPage != 0 {
		t.Fatalf("empty page: %+v, %v", page, err)
	}
	for _, tc := range []struct {
		page int
		want error
	}{
		{401, ErrUnauthorized}, {429, ErrRateLimited}, {500, ErrUpstream},
		{0, ErrInvalidPage}, {501, ErrPageLimit}, {int(^uint(0) >> 1), ErrPageLimit},
	} {
		_, err := ListGitLabRepositories(context.Background(), server.URL, token, tc.page, 100)
		if !errors.Is(err, tc.want) || strings.Contains(fmt.Sprint(err), token) {
			t.Errorf("page %d: got %v, want %v", tc.page, err, tc.want)
		}
	}
	for _, query := range []int{3, 4, 5} {
		_, err := ListGitLabRepositories(context.Background(), server.URL, token, query, 20)
		if !errors.Is(err, ErrUpstream) {
			t.Errorf("%d: got %v, want upstream error", query, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = ListGitLabRepositories(ctx, server.URL, token, 6, 20)
	if !errors.Is(err, ErrUpstream) {
		t.Errorf("timeout: got %v", err)
	}
	page, err = ListGitLabRepositories(context.Background(), server.URL, token, 7, 20)
	if err != nil || len(page.Repositories) != 2 || page.Repositories[0].CloneURL != "https://git.test/team/fallback.git" || page.Repositories[1].CloneURL != "" {
		t.Errorf("unsafe URL fallback: %+v, %v", page, err)
	}
	_, err = ListGitLabRepositories(context.Background(), server.URL, token, 8, 20)
	if !errors.Is(err, ErrUpstream) {
		t.Errorf("missing archived field accepted: %v", err)
	}
	page, err = ListGitLabRepositories(context.Background(), server.URL, token, 9, 20)
	if err != nil || len(page.Repositories) != 0 {
		t.Errorf("same-origin redirect: %+v, %v", page, err)
	}
	page, err = ListGitLabRepositories(context.Background(), server.URL, token, 10, 20)
	if !errors.Is(err, ErrUpstream) || len(page.Repositories) != 0 || page.NextPage != 0 {
		t.Errorf("partial page leaked on validation failure: %+v, %v", page, err)
	}
}

func TestGitLabCloneURL(t *testing.T) {
	for _, url := range []string{"https://user:secret@git.test/a.git", "https://git.test/a.git?token=secret", "ssh://user:secret@git.test/a.git", "file:///etc/passwd", "git@git.test:/", "https://git.test/a.git#secret", "https://git.test/a%0a.git"} {
		if validGitLabCloneURL(url) {
			t.Errorf("accepted unsafe clone URL")
		}
	}
	for _, url := range []string{"git@git.test:group/repo.git", "ssh://git@git.test/group/repo.git", "https://git.test/group/repo.git"} {
		if !validGitLabCloneURL(url) {
			t.Errorf("rejected safe clone URL: %s", url)
		}
	}
}
