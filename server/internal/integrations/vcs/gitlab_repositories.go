package vcs

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

var (
	ErrInvalidPage = errors.New("invalid GitLab pagination")
	ErrPageLimit   = errors.New("GitLab offset pagination limit reached")
	ErrRateLimited = errors.New("GitLab rate limited repository browsing")
	ErrUpstream    = errors.New("GitLab repository browsing unavailable")
)

// 完整项目载荷比 simple=true 的受限字段集大约四倍，上限同比例放宽以保持等效余量；
// 它的作用是给响应体一个硬界，不是贴合某一种载荷形态。
const gitLabProjectsResponseLimit = 8 << 20

type GitLabRepository struct {
	ID          int64   `json:"id"`
	FullName    string  `json:"full_name"`
	CloneURL    string  `json:"clone_url"`
	Archived    bool    `json:"archived"`
	Private     bool    `json:"private"`
	Description *string `json:"description"`
}

type GitLabRepositoryPage struct {
	Repositories []GitLabRepository `json:"repositories"`
	NextPage     int                `json:"next_page"`
}

var gitLabSCPURL = regexp.MustCompile(`^git@[^@:/\s]+:[^\s?#]+$`)

func validGitLabCloneURL(raw string) bool {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\r\n\t\\") {
		return false
	}
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return false
	}
	for _, char := range decoded {
		if char < 32 || char == 127 {
			return false
		}
	}
	if gitLabSCPURL.MatchString(raw) {
		return !strings.HasPrefix(strings.SplitN(raw, ":", 2)[1], "/")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Path == "" || u.Path == "/" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "ssh" {
		return false
	}
	if u.User == nil {
		return true
	}
	_, password := u.User.Password()
	return u.Scheme == "ssh" && u.User.Username() == "git" && !password
}

// ListGitLabRepositories returns a bounded, credential-free projection of one projects page.
func ListGitLabRepositories(ctx context.Context, instanceURL, token string, page, perPage int) (GitLabRepositoryPage, error) {
	empty := GitLabRepositoryPage{Repositories: []GitLabRepository{}}
	if page < 1 || perPage < 1 || perPage > 100 {
		return empty, ErrInvalidPage
	}
	if int64(page) > 50000/int64(perPage) {
		return empty, ErrPageLimit
	}
	base, err := url.Parse(NormalizeInstanceURL(instanceURL))
	if err != nil || base.Host == "" || base.User != nil || (base.Scheme != "http" && base.Scheme != "https") || base.RawQuery != "" || base.Fragment != "" || token == "" {
		return empty, ErrUpstream
	}
	endpoint := strings.TrimRight(base.String(), "/") + "/api/v4/projects"
	u, err := url.Parse(endpoint)
	if err != nil {
		return empty, ErrUpstream
	}
	q := u.Query()
	// 不带 simple=true：该模式下 GitLab 只返回受限字段集，archived 与 visibility 都不在其中
	// （GitLab CE 16.10.7 实测），而这两个字段正是归档标识与私有标识的唯一来源。
	q.Set("order_by", "id")
	q.Set("sort", "asc")
	q.Set("page", strconv.Itoa(page))
	q.Set("per_page", strconv.Itoa(perPage))
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return empty, ErrUpstream
	}
	req.Header.Set("PRIVATE-TOKEN", token)
	req.Header.Set("Accept", "application/json")
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) >= 5 || next.URL.Scheme != u.Scheme || !strings.EqualFold(next.URL.Host, u.Host) || next.URL.User != nil {
				return ErrUpstream
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return empty, ErrUpstream
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return empty, ErrUnauthorized
	case http.StatusTooManyRequests:
		return empty, ErrRateLimited
	}
	if resp.StatusCode != http.StatusOK {
		return empty, ErrUpstream
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, gitLabProjectsResponseLimit+1))
	if err != nil || len(body) > gitLabProjectsResponseLimit {
		return empty, ErrUpstream
	}
	var projects []struct {
		ID          int64   `json:"id"`
		Path        string  `json:"path_with_namespace"`
		SSHURL      string  `json:"ssh_url_to_repo"`
		HTTPURL     string  `json:"http_url_to_repo"`
		Visibility  string  `json:"visibility"`
		Archived    *bool   `json:"archived"`
		Description *string `json:"description"`
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '[' || json.Unmarshal(body, &projects) != nil || len(projects) > perPage {
		return empty, ErrUpstream
	}
	// Keep the accumulator separate from the error return value so no failure path leaks a partial page.
	result := GitLabRepositoryPage{Repositories: []GitLabRepository{}}
	for _, project := range projects {
		// 身份字段仍是硬要求：缺了就无法标识项目，属于结构性畸形。
		// archived / visibility 是展示语义，上游按版本或权限裁剪时降级取值，不牵连整页。
		if project.ID <= 0 || project.Path == "" || strings.Contains(project.Path, token) {
			return empty, ErrUpstream
		}
		// 归档标识未知时按未归档处理：归档项目在选择列表里是禁选的，误标会把可用项目锁死。
		archived := project.Archived != nil && *project.Archived
		// 可见性未知或不在已知枚举内时按私有处理：绝不把可能私有的项目展示成公开。
		private := project.Visibility != "public"
		cloneURL := project.SSHURL
		if !validGitLabCloneURL(cloneURL) {
			cloneURL = project.HTTPURL
		}
		if !validGitLabCloneURL(cloneURL) || strings.Contains(cloneURL, token) {
			cloneURL = ""
		}
		description := project.Description
		if description != nil && strings.Contains(*description, token) {
			description = nil
		}
		result.Repositories = append(result.Repositories, GitLabRepository{
			ID: project.ID, FullName: project.Path, CloneURL: cloneURL,
			Archived: archived, Private: private, Description: description,
		})
	}
	next := resp.Header.Get("X-Next-Page")
	if next != "" {
		nextPage, err := strconv.Atoi(next)
		if err != nil || nextPage <= page {
			return empty, ErrUpstream
		}
		if int64(nextPage) > 50000/int64(perPage) {
			return empty, ErrPageLimit
		}
		result.NextPage = nextPage
	}
	return result, nil
}
