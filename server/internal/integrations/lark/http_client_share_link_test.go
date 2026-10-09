package lark

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// RUYI-572: the three Drive/Docs read endpoints behind the share-link
// resolver — /drive/v1/files/:token/download (binary + content-disposition),
// /wiki/v2/spaces/get_node (token → obj routing) and
// /docx/v1/documents/:id/raw_content (plain text).

func TestHTTPClient_DownloadDriveFile(t *testing.T) {
	fake := newLarkFake(t)
	fake.stubToken("tenant-token-1", 3600)
	fake.mux.HandleFunc("/open-apis/drive/v1/files/tok123/download", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			fake.t.Errorf("drive download: want GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="spec.xlsx"; filename*=UTF-8''spec.xlsx`)
		_, _ = w.Write([]byte("XLSX-BYTES"))
	})

	c := newTestClient(fake, func() time.Time { return time.Unix(100, 0) })
	got, err := c.DownloadDriveFile(context.Background(), testCreds(), "tok123")
	if err != nil {
		t.Fatalf("DownloadDriveFile: %v", err)
	}
	if string(got.Data) != "XLSX-BYTES" {
		t.Errorf("Data = %q", got.Data)
	}
	if got.Filename != "spec.xlsx" {
		t.Errorf("Filename = %q, want spec.xlsx", got.Filename)
	}
	if got.SizeBytes != int64(len("XLSX-BYTES")) {
		t.Errorf("SizeBytes = %d", got.SizeBytes)
	}
	if got.ContentType != "application/octet-stream" {
		t.Errorf("ContentType = %q", got.ContentType)
	}
}

func TestHTTPClient_DownloadDriveFile_BusinessError(t *testing.T) {
	fake := newLarkFake(t)
	fake.stubToken("tenant-token-1", 3600)
	fake.mux.HandleFunc("/open-apis/drive/v1/files/tok404/download", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"code": 1061004, "msg": "file not exist"})
	})

	c := newTestClient(fake, func() time.Time { return time.Unix(100, 0) })
	_, err := c.DownloadDriveFile(context.Background(), testCreds(), "tok404")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 1061004 {
		t.Fatalf("want APIError code 1061004, got %v", err)
	}
}

func TestHTTPClient_DownloadDriveFile_EmptyToken(t *testing.T) {
	c := NewHTTPAPIClient(HTTPClientConfig{}).(*httpAPIClient)
	if _, err := c.DownloadDriveFile(context.Background(), testCreds(), ""); err == nil {
		t.Fatal("empty token must fail before any network call")
	}
}

func TestHTTPClient_GetWikiNode(t *testing.T) {
	fake := newLarkFake(t)
	fake.stubToken("tenant-token-1", 3600)
	fake.mux.HandleFunc("/open-apis/wiki/v2/spaces/get_node", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			fake.t.Errorf("get_node: want GET, got %s", r.Method)
		}
		if q := r.URL.Query().Get("token"); q != "wikcn123" {
			fake.t.Errorf("get_node token query = %q, want wikcn123", q)
		}
		writeJSON(w, map[string]any{
			"code": 0,
			"data": map[string]any{
				"node": map[string]any{
					"title":    "需求规格",
					"obj_type": "docx",
					"obj_token": "doxcnABC",
				},
			},
		})
	})

	c := newTestClient(fake, func() time.Time { return time.Unix(100, 0) })
	node, err := c.GetWikiNode(context.Background(), testCreds(), "wikcn123")
	if err != nil {
		t.Fatalf("GetWikiNode: %v", err)
	}
	if node.Title != "需求规格" || node.ObjType != "docx" || node.ObjToken != "doxcnABC" {
		t.Fatalf("node = %+v", node)
	}
}

func TestHTTPClient_GetWikiNode_BusinessError(t *testing.T) {
	fake := newLarkFake(t)
	fake.stubToken("tenant-token-1", 3600)
	fake.mux.HandleFunc("/open-apis/wiki/v2/spaces/get_node", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"code": 99991672, "msg": "no permission"})
	})

	c := newTestClient(fake, func() time.Time { return time.Unix(100, 0) })
	_, err := c.GetWikiNode(context.Background(), testCreds(), "wikcn404")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 99991672 {
		t.Fatalf("want APIError code 99991672, got %v", err)
	}
}

func TestHTTPClient_GetWikiNode_EmptyToken(t *testing.T) {
	c := NewHTTPAPIClient(HTTPClientConfig{}).(*httpAPIClient)
	if _, err := c.GetWikiNode(context.Background(), testCreds(), ""); err == nil {
		t.Fatal("empty token must fail before any network call")
	}
}

func TestHTTPClient_GetDocxRawContent(t *testing.T) {
	fake := newLarkFake(t)
	fake.stubToken("tenant-token-1", 3600)
	var gotPath string
	fake.mux.HandleFunc("/open-apis/docx/v1/documents/doxcnABC/raw_content", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			fake.t.Errorf("raw_content: want GET, got %s", r.Method)
		}
		gotPath = r.URL.Path
		writeJSON(w, map[string]any{
			"code": 0,
			"data": map[string]any{"content": "第一段正文\n第二段正文"},
		})
	})

	c := newTestClient(fake, func() time.Time { return time.Unix(100, 0) })
	content, err := c.GetDocxRawContent(context.Background(), testCreds(), "doxcnABC")
	if err != nil {
		t.Fatalf("GetDocxRawContent: %v", err)
	}
	if content != "第一段正文\n第二段正文" {
		t.Fatalf("content = %q", content)
	}
	if gotPath != "/open-apis/docx/v1/documents/doxcnABC/raw_content" {
		t.Fatalf("request path = %q", gotPath)
	}
}

func TestHTTPClient_GetDocxRawContent_BusinessError(t *testing.T) {
	fake := newLarkFake(t)
	fake.stubToken("tenant-token-1", 3600)
	fake.mux.HandleFunc("/open-apis/docx/v1/documents/doxcn404/raw_content", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"code": 99991672, "msg": "no permission"})
	})

	c := newTestClient(fake, func() time.Time { return time.Unix(100, 0) })
	_, err := c.GetDocxRawContent(context.Background(), testCreds(), "doxcn404")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 99991672 {
		t.Fatalf("want APIError code 99991672, got %v", err)
	}
}

func TestHTTPClient_GetDocxRawContent_EmptyDocumentID(t *testing.T) {
	c := NewHTTPAPIClient(HTTPClientConfig{}).(*httpAPIClient)
	if _, err := c.GetDocxRawContent(context.Background(), testCreds(), ""); err == nil {
		t.Fatal("empty document id must fail before any network call")
	}
}

// The resolver consumes the three methods through the narrow
// ShareLinkClient interface (type-asserted, like messageResourceStreamer)
// — the real HTTP client must satisfy it.
func TestHTTPClient_ImplementsShareLinkClient(t *testing.T) {
	var _ ShareLinkClient = NewHTTPAPIClient(HTTPClientConfig{}).(*httpAPIClient)
}
