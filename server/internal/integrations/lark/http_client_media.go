package lark

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
)

// Outbound media over Lark's Open Platform: two upload endpoints that mint
// keys, and two send endpoints that take a key.
//
// Lark refuses to accept binary content inline in a message. An outbound file
// is always two round-trips — POST the bytes to /im/v1/images or /im/v1/files
// for an image_key / file_key, then POST a message whose content envelope
// carries only that key. The two namespaces are not interchangeable: a
// file_key in an image message is rejected, so the kind decision (see
// feishuSendsAsImage) has to be made before the upload, not after.

// multipartUpload is a fully-buffered multipart body.
//
// It is buffered rather than streamed on purpose: doAuthedMultipart replays
// the request once when Lark rejects the tenant_access_token, and a body
// consumed by the first attempt cannot be sent again. An io.Reader source
// would make the refresh-and-retry path silently upload zero bytes, which
// Lark answers with a business error that looks nothing like a token problem.
type multipartUpload struct {
	// Fields are plain form fields in order (image_type, file_type, file_name).
	Fields [][2]string
	// FileField is the multipart field name Lark expects for the bytes:
	// "image" on the images endpoint, "file" on the files endpoint.
	FileField string
	Filename  string
	Data      []byte
}

// encode renders the body once and returns the Content-Type carrying the
// generated boundary.
func (u multipartUpload) encode() (string, []byte, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, f := range u.Fields {
		if err := w.WriteField(f[0], f[1]); err != nil {
			return "", nil, fmt.Errorf("write field %s: %w", f[0], err)
		}
	}
	part, err := w.CreateFormFile(u.FileField, u.Filename)
	if err != nil {
		return "", nil, fmt.Errorf("create file part: %w", err)
	}
	if _, err := part.Write(u.Data); err != nil {
		return "", nil, fmt.Errorf("write file part: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", nil, fmt.Errorf("close multipart writer: %w", err)
	}
	return w.FormDataContentType(), buf.Bytes(), nil
}

// doAuthedMultipart is doAuthedJSON's multipart twin, with the same
// refresh-and-retry-once semantics and for the same reason: a token rejection
// happens during authentication, before Lark processes the request, so nothing
// was created and the retry cannot mint two keys. Only token errors are
// retried — a business rejection is definitive and a transport failure is
// ambiguous, and a second upload of ambiguous bytes is not an improvement.
func (c *httpAPIClient) doAuthedMultipart(ctx context.Context, creds InstallationCredentials, path string, up multipartUpload, out any) error {
	contentType, payload, err := up.encode()
	if err != nil {
		return fmt.Errorf("encode multipart: %w", err)
	}
	token, err := c.tenantAccessToken(ctx, creds)
	if err != nil {
		return err
	}
	err = c.doMultipart(ctx, c.resolveBaseURL(creds), path, token, contentType, payload, out)
	if !isTokenError(larkErrorCode(err)) {
		return err
	}
	c.cfg.Logger.Warn("lark http client: tenant_access_token rejected on upload; refreshing and retrying once",
		"app_id", creds.AppID, "path", path, "err", err)
	c.invalidateToken(creds.AppID)
	fresh, refreshErr := c.tenantAccessToken(ctx, creds)
	if refreshErr != nil {
		return fmt.Errorf("%w (token refresh failed: %v)", err, refreshErr)
	}
	return c.doMultipart(ctx, c.resolveBaseURL(creds), path, fresh, contentType, payload, out)
}

// doMultipart mirrors doJSON's error handling — the non-2xx branch parses
// Lark's {code, msg} out of the body so token classification keeps working —
// and differs from it only in the request body and Content-Type.
func (c *httpAPIClient) doMultipart(ctx context.Context, baseURL, path, token, contentType string, payload []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()
	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		code, msg := parseLarkErrorBody(rawBody)
		return &larkAPIStatusError{
			StatusCode: resp.StatusCode,
			Code:       code,
			Msg:        msg,
			Raw:        truncate(string(rawBody), 512),
		}
	}
	if out != nil && len(rawBody) > 0 {
		if err := json.Unmarshal(rawBody, out); err != nil {
			return fmt.Errorf("decode body: %w (raw=%s)", err, truncate(string(rawBody), 256))
		}
	}
	return nil
}

// UploadImage posts image bytes to /open-apis/im/v1/images with
// image_type=message and returns the image_key.
//
// image_type=message (rather than avatar) is what makes the key usable in a
// chat message; an avatar key is rejected by the send endpoint.
func (c *httpAPIClient) UploadImage(ctx context.Context, p UploadImageParams) (string, error) {
	if len(p.Data) == 0 {
		return "", errors.New("lark http client: missing image bytes")
	}
	up := multipartUpload{
		Fields:    [][2]string{{"image_type", "message"}},
		FileField: "image",
		Filename:  firstNonEmpty(p.Filename, "image"),
		Data:      p.Data,
	}
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			ImageKey string `json:"image_key"`
		} `json:"data"`
	}
	if err := c.doAuthedMultipart(ctx, p.InstallationID, "/open-apis/im/v1/images", up, &resp); err != nil {
		return "", fmt.Errorf("lark http client: upload image: %w", err)
	}
	if resp.Code != 0 || resp.Data.ImageKey == "" {
		if isTokenError(resp.Code) {
			c.invalidateToken(p.InstallationID.AppID)
		}
		return "", &APIError{Op: "upload image", Code: resp.Code, Msg: resp.Msg}
	}
	return resp.Data.ImageKey, nil
}

// UploadFile posts arbitrary bytes to /open-apis/im/v1/files and returns the
// file_key. file_name is what the recipient sees in the chat and what their
// download is named, so it is sent as a form field as well as the part
// filename.
func (c *httpAPIClient) UploadFile(ctx context.Context, p UploadFileParams) (string, error) {
	if len(p.Data) == 0 {
		return "", errors.New("lark http client: missing file bytes")
	}
	name := firstNonEmpty(p.Filename, "attachment")
	up := multipartUpload{
		Fields: [][2]string{
			{"file_type", firstNonEmpty(p.FileType, feishuFileTypeStream)},
			{"file_name", name},
		},
		FileField: "file",
		Filename:  name,
		Data:      p.Data,
	}
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			FileKey string `json:"file_key"`
		} `json:"data"`
	}
	if err := c.doAuthedMultipart(ctx, p.InstallationID, "/open-apis/im/v1/files", up, &resp); err != nil {
		return "", fmt.Errorf("lark http client: upload file: %w", err)
	}
	if resp.Code != 0 || resp.Data.FileKey == "" {
		if isTokenError(resp.Code) {
			c.invalidateToken(p.InstallationID.AppID)
		}
		return "", &APIError{Op: "upload file", Code: resp.Code, Msg: resp.Msg}
	}
	return resp.Data.FileKey, nil
}

// SendImageMessage posts an uploaded image into a chat (msg_type=image).
func (c *httpAPIClient) SendImageMessage(ctx context.Context, p SendImageParams) (string, error) {
	if p.ChatID == "" {
		return "", errors.New("lark http client: missing chat_id")
	}
	if p.ImageKey == "" {
		return "", errors.New("lark http client: missing image_key")
	}
	return c.sendMediaMessage(ctx, p.InstallationID, p.ChatID, "image",
		map[string]string{"image_key": p.ImageKey}, p.ReplyTarget, "send image message")
}

// SendFileMessage posts an uploaded file into a chat (msg_type=file).
func (c *httpAPIClient) SendFileMessage(ctx context.Context, p SendFileParams) (string, error) {
	if p.ChatID == "" {
		return "", errors.New("lark http client: missing chat_id")
	}
	if p.FileKey == "" {
		return "", errors.New("lark http client: missing file_key")
	}
	return c.sendMediaMessage(ctx, p.InstallationID, p.ChatID, "file",
		map[string]string{"file_key": p.FileKey}, p.ReplyTarget, "send file message")
}

// sendMediaMessage is the shared tail of the two media sends: both go through
// outboundMessageRequest, so a file threads into the same 话题 the words did.
func (c *httpAPIClient) sendMediaMessage(ctx context.Context, creds InstallationCredentials, chatID ChatID, msgType string, content map[string]string, target ReplyTarget, op string) (string, error) {
	contentBytes, err := json.Marshal(content)
	if err != nil {
		return "", fmt.Errorf("lark http client: encode %s content: %w", msgType, err)
	}
	path, body := outboundMessageRequest(chatID, msgType, string(contentBytes), target)
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	if err := c.doAuthedJSON(ctx, creds, http.MethodPost, path, body, &resp); err != nil {
		return "", fmt.Errorf("lark http client: %s: %w", op, err)
	}
	if resp.Code != 0 || resp.Data.MessageID == "" {
		if isTokenError(resp.Code) {
			c.invalidateToken(creds.AppID)
		}
		return "", &APIError{Op: op, Code: resp.Code, Msg: resp.Msg}
	}
	return resp.Data.MessageID, nil
}
