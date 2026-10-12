package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"testing"
	"time"
)

// buildGateWavBytes assembles a minimal valid PCM WAV so the transcription
// route's shape check passes and the request actually reaches the resolution
// chain (the 409 below is the chain's answer, not the shape check's).
func buildGateWavBytes(byteRate uint32, dataSize int) []byte {
	buf := bytes.NewBuffer(nil)
	buf.WriteString("RIFF")
	binary.Write(buf, binary.LittleEndian, uint32(36+dataSize))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(buf, binary.LittleEndian, uint32(16))
	binary.Write(buf, binary.LittleEndian, uint16(1)) // PCM
	binary.Write(buf, binary.LittleEndian, uint16(1)) // mono
	binary.Write(buf, binary.LittleEndian, byteRate)  // sample rate == byte rate (1 byte/frame)
	binary.Write(buf, binary.LittleEndian, byteRate)
	binary.Write(buf, binary.LittleEndian, uint16(1)) // block align
	binary.Write(buf, binary.LittleEndian, uint16(8)) // bits per sample
	buf.WriteString("data")
	binary.Write(buf, binary.LittleEndian, uint32(dataSize))
	buf.Write(make([]byte, dataSize))
	return buf.Bytes()
}

// gateMultipartBody wraps the wav in a multipart/form-data body with an
// "audio" part, the exact shape the endpoint parses.
func gateMultipartBody(t *testing.T, wav []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("audio", "note.wav")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(wav); err != nil {
		t.Fatalf("write audio part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return &buf, writer.FormDataContentType()
}

// TestVoiceTranscriptionRouteGate locks in the route-level gate on the
// transcription endpoint (RUYI-710): a valid JWT from a non-member must be
// rejected before the handler (404, exactly like any workspace the user
// cannot see), unauthenticated calls are 401, and a member's well-formed
// upload passes the gate and reaches the resolution chain — which answers
// 409 VOICE_UNAVAILABLE:voice_unconfigured because the fixture workspace
// carries no voice settings. Reaching that 409 (and a 400 for a malformed
// body with the same credentials) is the positive proof the gate lets
// members through instead of bouncing them.
func TestVoiceTranscriptionRouteGate(t *testing.T) {
	if testPool == nil {
		t.Skip("no database connection")
	}

	// A plain member of the fixture workspace.
	var memberUserID string
	if err := testPool.QueryRow(context.Background(), `
INSERT INTO "user" (name, email) VALUES ('Transcription gate member', $1) RETURNING id
`, "transcription-gate-member-"+time.Now().Format("150405.000000000")+"@example.test").Scan(&memberUserID); err != nil {
		t.Fatalf("create member user: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')
`, testWorkspaceID, memberUserID); err != nil {
		t.Fatalf("create workspace member: %v", err)
	}
	memberToken, err := generateTestJWT(memberUserID, "transcription-gate-member@example.test", "Transcription gate member")
	if err != nil {
		t.Fatalf("generate member token: %v", err)
	}

	// An authenticated user holding no membership in any workspace.
	var outsiderUserID string
	if err := testPool.QueryRow(context.Background(), `
INSERT INTO "user" (name, email) VALUES ('Transcription gate outsider', $1) RETURNING id
`, "transcription-gate-outsider-"+time.Now().Format("150405.000000000")+"@example.test").Scan(&outsiderUserID); err != nil {
		t.Fatalf("create outsider user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM member WHERE workspace_id = $1 AND user_id = $2`, testWorkspaceID, memberUserID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, memberUserID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, outsiderUserID)
	})
	outsiderToken, err := generateTestJWT(outsiderUserID, "transcription-gate-outsider@example.test", "Transcription gate outsider")
	if err != nil {
		t.Fatalf("generate outsider token: %v", err)
	}

	endpoint := testServer.URL + "/api/workspaces/" + testWorkspaceID + "/voice-transcriptions"
	wav := buildGateWavBytes(8000, 1000) // 0.125s, far below the duration cap
	// Fresh body per call: a bytes.Buffer is consumed by the first request,
	// and a reused one silently arrives empty (VOICE_AUDIO_REQUIRED 400).
	freshUpload := func(t *testing.T) (*bytes.Buffer, string) { return gateMultipartBody(t, wav) }

	tests := []struct {
		name       string
		token      string
		body       func(t *testing.T) (*bytes.Buffer, string)
		wantStatus int
		wantCode   string
	}{
		{
			name:       "unauthenticated upload is rejected",
			token:      "",
			body:       freshUpload,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "non-member upload is rejected by the route gate",
			token:      outsiderToken,
			body:       freshUpload,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "member upload passes the gate and hits the unconfigured chain",
			token:      memberToken,
			body:       freshUpload,
			wantStatus: http.StatusConflict,
			wantCode:   "VOICE_UNAVAILABLE:voice_unconfigured",
		},
		{
			name:       "owner upload passes the gate identically",
			token:      testToken,
			body:       freshUpload,
			wantStatus: http.StatusConflict,
			wantCode:   "VOICE_UNAVAILABLE:voice_unconfigured",
		},
		{
			name:  "member malformed body clears the gate and fails the shape check",
			token: memberToken,
			body: func(t *testing.T) (*bytes.Buffer, string) {
				return bytes.NewBufferString("this is not multipart"), "text/plain"
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "VOICE_AUDIO_MALFORMED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf, contentType := tt.body(t)
			req, err := http.NewRequest(http.MethodPost, endpoint, buf)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			req.Header.Set("Content-Type", contentType)
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			if tt.wantCode == "" {
				return
			}
			var answer struct {
				Code string `json:"code"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if answer.Code != tt.wantCode {
				t.Fatalf("code = %q, want %q", answer.Code, tt.wantCode)
			}
		})
	}
}
