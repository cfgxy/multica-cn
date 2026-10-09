package lark

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// ShareLinkClient is the Drive/Docs read surface behind the share-link
// resolver (RUYI-572). It is deliberately a narrow interface asserted at
// the use site (the messageResourceStreamer pattern), not part of
// APIClient: the stub client and most test fakes have nothing to fetch,
// and a missing implementation simply degrades to the share-link note.
type ShareLinkClient interface {
	// DownloadDriveFile fetches one cloud-drive file's bytes via
	// GET /open-apis/drive/v1/files/{file_token}/download. The filename
	// comes from the response's content-disposition.
	DownloadDriveFile(ctx context.Context, creds InstallationCredentials, fileToken string) (DownloadedResource, error)
	// GetWikiNode routes a wiki token to its underlying object via
	// GET /open-apis/wiki/v2/spaces/get_node?token=...
	GetWikiNode(ctx context.Context, creds InstallationCredentials, wikiToken string) (WikiNode, error)
	// GetDocxRawContent reads a docx document's plain-text content via
	// GET /open-apis/docx/v1/documents/{document_id}/raw_content.
	GetDocxRawContent(ctx context.Context, creds InstallationCredentials, documentID string) (string, error)
}

// WikiNode is the routing payload of get_node: a wiki link's token never
// addresses the document directly — obj_type/obj_token name the real
// object (docx, file, sheet, bitable, mindnote, …) the resolver then
// routes on. Only docx and file are resolvable this iteration.
type WikiNode struct {
	Title    string
	ObjType  string
	ObjToken string
}

// DownloadDriveFile shares DownloadMessageResource's transport contract:
// refresh-and-retry once on token rejection (a rejected token never
// serves bytes, so the replay cannot duplicate anything), JSON-looking
// business errors detected before the body is treated as file bytes, and
// the same byte cap / content-disposition parsing as message resources.
func (c *httpAPIClient) DownloadDriveFile(ctx context.Context, creds InstallationCredentials, fileToken string) (DownloadedResource, error) {
	if fileToken == "" {
		return DownloadedResource{}, errors.New("lark http client: missing file_token")
	}
	path := "/open-apis/drive/v1/files/" + url.PathEscape(fileToken) + "/download"
	for attempt := 0; ; attempt++ {
		token, err := c.tenantAccessToken(ctx, creds)
		if err != nil {
			return DownloadedResource{}, err
		}
		stream, err := c.downloadMessageResourceStreamOnce(ctx, creds, path, token)
		if err == nil {
			defer stream.Body.Close()
			rawBody, readErr := io.ReadAll(stream.Body)
			if readErr != nil {
				return DownloadedResource{}, fmt.Errorf("lark http client: download drive file: read body: %w", readErr)
			}
			sizeBytes := stream.SizeBytes
			if sizeBytes == 0 {
				sizeBytes = int64(len(rawBody))
			}
			return DownloadedResource{
				Data:        rawBody,
				ContentType: stream.ContentType,
				Filename:    stream.Filename,
				SizeBytes:   sizeBytes,
			}, nil
		}
		if attempt > 0 || !isTokenError(larkErrorCode(err)) {
			return DownloadedResource{}, err
		}
		c.cfg.Logger.Warn("lark http client: tenant_access_token rejected on drive file download; refreshing and retrying once",
			"app_id", creds.AppID, "err", err)
		c.invalidateToken(creds.AppID)
	}
}

// GetWikiNode resolves a wiki token to its underlying object.
func (c *httpAPIClient) GetWikiNode(ctx context.Context, creds InstallationCredentials, wikiToken string) (WikiNode, error) {
	if wikiToken == "" {
		return WikiNode{}, errors.New("lark http client: missing wiki token")
	}
	q := url.Values{}
	q.Set("token", wikiToken)
	path := "/open-apis/wiki/v2/spaces/get_node?" + q.Encode()

	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Node struct {
				Title    string `json:"title"`
				ObjType  string `json:"obj_type"`
				ObjToken string `json:"obj_token"`
			} `json:"node"`
		} `json:"data"`
	}
	if err := c.doAuthedJSON(ctx, creds, http.MethodGet, path, nil, &resp); err != nil {
		return WikiNode{}, fmt.Errorf("lark http client: wiki get_node: %w", err)
	}
	if resp.Code != 0 {
		if isTokenError(resp.Code) {
			c.invalidateToken(creds.AppID)
		}
		// Structured *APIError, not a text-wrapped error: the capability
		// probe classifies these codes (99991672 → missing) through
		// larkErrorCodeMsg.
		return WikiNode{}, &APIError{Op: "wiki get_node", Code: resp.Code, Msg: resp.Msg}
	}
	return WikiNode{
		Title:    resp.Data.Node.Title,
		ObjType:  resp.Data.Node.ObjType,
		ObjToken: resp.Data.Node.ObjToken,
	}, nil
}

// GetDocxRawContent reads a docx document as plain text — the closest
// match to the context-injection semantics (ADR 005 §3: 纯文本直出).
func (c *httpAPIClient) GetDocxRawContent(ctx context.Context, creds InstallationCredentials, documentID string) (string, error) {
	if documentID == "" {
		return "", errors.New("lark http client: missing document_id")
	}
	path := "/open-apis/docx/v1/documents/" + url.PathEscape(documentID) + "/raw_content"

	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := c.doAuthedJSON(ctx, creds, http.MethodGet, path, nil, &resp); err != nil {
		return "", fmt.Errorf("lark http client: docx raw_content: %w", err)
	}
	if resp.Code != 0 {
		if isTokenError(resp.Code) {
			c.invalidateToken(creds.AppID)
		}
		// Structured *APIError for probe classification, same shape as
		// get_node above.
		return "", &APIError{Op: "docx raw_content", Code: resp.Code, Msg: resp.Msg}
	}
	return resp.Data.Content, nil
}
