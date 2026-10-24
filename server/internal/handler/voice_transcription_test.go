package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
)

// RUYI-710 stage 1: the one-shot dictation transcription endpoint. Providers
// are always in-process stubs; only fake key values are used throughout — no
// real provider endpoint is ever contacted, audio stays in memory, and every
// rejection carries its stable code.

const voiceASRTestAPIKey = "fake-asr-key-710-not-a-real-credential"

// ---------------------------------------------------------------------------
// Container fixture builders (synthetic minimal containers).
// ---------------------------------------------------------------------------

// buildWavBytes renders a minimal PCM wav: fmt chunk (byteRate as declared)
// plus a data chunk of dataSize zero bytes.
func buildWavBytes(byteRate uint32, dataSize int) []byte {
	var buf bytes.Buffer
	buf.WriteString("RIFF")
	chunkSizes := uint32(4 + 8 + 16 + 8 + dataSize)
	buf.Write(leU32(chunkSizes))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	buf.Write(leU32(16))
	buf.Write(leU16(1)) // PCM
	buf.Write(leU16(1)) // mono
	buf.Write(leU32(byteRate))
	buf.Write(leU32(byteRate)) // sampleRate alias for the fixture
	buf.Write(leU16(2))        // block align
	buf.Write(leU16(16))       // bits
	buf.WriteString("data")
	buf.Write(leU32(uint32(dataSize)))
	buf.Write(bytes.Repeat([]byte{0}, dataSize))
	return buf.Bytes()
}

func leU16(v uint16) []byte { b := make([]byte, 2); binary.LittleEndian.PutUint16(b, v); return b }
func leU32(v uint32) []byte { b := make([]byte, 4); binary.LittleEndian.PutUint32(b, v); return b }

// ebmlSizeVint encodes n as a minimal EBML size vint.
func ebmlSizeVint(n uint64) []byte {
	length := 1
	for ; length < 8; length++ {
		if n < 1<<uint(7*length)-1 {
			break
		}
	}
	out := make([]byte, length)
	for i := 0; i < length; i++ {
		out[length-1-i] = byte(n >> (8 * i))
	}
	out[0] |= 0x80 >> (length - 1)
	return out
}

// ebmlElem wraps body in one EBML element.
func ebmlElem(id []byte, body []byte) []byte {
	out := append([]byte{}, id...)
	out = append(out, ebmlSizeVint(uint64(len(body)))...)
	return append(out, body...)
}

// buildWebmBytes renders a minimal webm: EBML header + Segment(Info(
// TimestampScale, Duration)).
func buildWebmBytes(scale uint64, duration float64, unknownSegmentSize bool) []byte {
	var info bytes.Buffer
	scaleBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(scaleBytes, scale)
	scaleBytes = bytes.TrimLeft(scaleBytes, "\x00")
	if len(scaleBytes) == 0 {
		scaleBytes = []byte{0}
	}
	info.Write(ebmlElem([]byte{0x2A, 0xD7, 0xB1}, scaleBytes))
	durBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(durBytes, math.Float64bits(duration))
	info.Write(ebmlElem([]byte{0x44, 0x89}, durBytes))
	// The parser walks Segment → Info → Duration, so the Info master must
	// wrap the child elements — a bare TimestampScale/Duration pair under
	// Segment is never looked at.
	infoMaster := ebmlElem([]byte{0x15, 0x49, 0xA9, 0x66}, info.Bytes())

	var out bytes.Buffer
	out.Write([]byte{0x1A, 0x45, 0xDF, 0xA3}) // EBML header master id
	out.Write(ebmlSizeVint(0))                // empty header body
	out.Write([]byte{0x18, 0x53, 0x80, 0x67}) // Segment
	if unknownSegmentSize {
		out.Write([]byte{0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	} else {
		out.Write(ebmlSizeVint(uint64(len(infoMaster))))
	}
	out.Write(infoMaster)
	return out.Bytes()
}

// buildMP4Bytes renders a minimal m4a: ftyp + moov(mvhd) with the requested
// mvhd version.
func buildMP4Bytes(version byte, timescale uint32, duration uint64) []byte {
	var mvhd bytes.Buffer
	mvhd.Write([]byte{version, 0, 0, 0}) // version + flags
	if version == 0 {
		mvhd.Write(make([]byte, 8))         // creation + modification
		mvhd.Write(beU32(timescale))        // timescale
		mvhd.Write(beU32(uint32(duration))) // duration
	} else {
		mvhd.Write(make([]byte, 16)) // creation + modification (64-bit)
		mvhd.Write(beU32(timescale)) // timescale
		mvhd.Write(beU64(duration))  // duration
	}
	mvhdBody := mvhd.Bytes()

	mvhdBox := append(beU32(uint32(8+len(mvhdBody))), []byte("mvhd")...)
	mvhdBox = append(mvhdBox, mvhdBody...)
	moovBox := append(beU32(uint32(8+len(mvhdBox))), []byte("moov")...)
	moovBox = append(moovBox, mvhdBox...)

	ftyp := append(beU32(20), []byte("ftyp")...)
	ftyp = append(ftyp, []byte("M4A ")...)
	ftyp = append(ftyp, beU32(0)...)
	ftyp = append(ftyp, []byte("M4A ")...)
	return append(ftyp, moovBox...)
}

func beU32(v uint32) []byte { b := make([]byte, 4); binary.BigEndian.PutUint32(b, v); return b }
func beU64(v uint64) []byte { b := make([]byte, 8); binary.BigEndian.PutUint64(b, v); return b }

// ---------------------------------------------------------------------------
// Sniffing and duration parsing (pure unit).
// ---------------------------------------------------------------------------

func TestSniffAudioContainer(t *testing.T) {
	cases := []struct {
		name     string
		data     []byte
		wantName string
	}{
		{"wav", buildWavBytes(8000, 800), "wav"},
		{"webm", buildWebmBytes(1_000_000, 1000, false), "webm"},
		{"mp4", buildMP4Bytes(0, 1000, 500), "mp4"},
		{"garbage", bytes.Repeat([]byte{0x07}, 64), ""},
		{"riff-but-not-wave", append([]byte("RIFF....AVI "), bytes.Repeat([]byte{0}, 16)...), ""},
		{"truncated-wav-header", []byte("RIFF"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			container, ok := sniffAudioContainer(tc.data)
			if ok != (tc.wantName != "") {
				t.Fatalf("sniff ok = %v, want %v", ok, tc.wantName != "")
			}
			if container.name != tc.wantName {
				t.Fatalf("sniff name = %q, want %q", container.name, tc.wantName)
			}
		})
	}
}

func TestWavAudioDuration(t *testing.T) {
	t.Run("exact division", func(t *testing.T) {
		data := buildWavBytes(8000, 16000)
		d, err := wavAudioDuration(data)
		if err != nil || d != 2*time.Second {
			t.Fatalf("duration = %v (%v), want 2s", d, err)
		}
	})

	t.Run("odd-sized padding chunk before data", func(t *testing.T) {
		// LIST chunk of 3 body bytes carries one RIFF pad byte.
		base := buildWavBytes(8000, 8000)
		list := append([]byte("LIST"), leU32(3)...)
		list = append(list, 'a', 'b', 'c', 0)
		out := append([]byte{}, base[:36]...) // RIFF header + fmt chunk
		out = append(out, list...)
		out = append(out, base[36:]...)
		// The outer RIFF size field is now stale, but the chunk walk never
		// reads it — duration must still come out exact.
		d, err := wavAudioDuration(out)
		if err != nil || d != time.Second {
			t.Fatalf("duration = %v (%v), want 1s", d, err)
		}
	})

	t.Run("data chunk declares more than the buffer holds", func(t *testing.T) {
		data := buildWavBytes(8000, 16000)
		binary.LittleEndian.PutUint32(data[40:44], 1<<30) // data size field
		if _, err := wavAudioDuration(data); err == nil {
			t.Fatal("truncated data chunk must be an error")
		}
	})

	t.Run("missing fmt chunk", func(t *testing.T) {
		data := buildWavBytes(8000, 800)
		copy(data[12:16], "JUNK")
		if _, err := wavAudioDuration(data); err == nil {
			t.Fatal("missing fmt chunk must be an error")
		}
	})

	t.Run("zero byte rate", func(t *testing.T) {
		data := buildWavBytes(0, 800)
		if _, err := wavAudioDuration(data); err == nil {
			t.Fatal("zero byte rate must be an error")
		}
	})
}

func TestWebmAudioDuration(t *testing.T) {
	t.Run("known segment size with default scale", func(t *testing.T) {
		d, err := webmAudioDuration(buildWebmBytes(1_000_000, 1500, false))
		if err != nil || d != 1500*time.Millisecond {
			t.Fatalf("duration = %v (%v), want 1.5s", d, err)
		}
	})

	t.Run("unknown segment size (streaming recorder shape)", func(t *testing.T) {
		d, err := webmAudioDuration(buildWebmBytes(1_000_000, 4000, true))
		if err != nil || d != 4*time.Second {
			t.Fatalf("duration = %v (%v), want 4s", d, err)
		}
	})

	t.Run("custom timestamp scale", func(t *testing.T) {
		d, err := webmAudioDuration(buildWebmBytes(500_000, 4000, false))
		if err != nil || d != 2*time.Second {
			t.Fatalf("duration = %v (%v), want 2s", d, err)
		}
	})

	t.Run("negative duration is corrupt", func(t *testing.T) {
		if _, err := webmAudioDuration(buildWebmBytes(1_000_000, -5, false)); err == nil {
			t.Fatal("negative duration must be an error")
		}
	})

	t.Run("garbage body fails", func(t *testing.T) {
		if _, err := webmAudioDuration([]byte{0x1A, 0x45, 0xDF, 0xA3, 0x99}); err == nil {
			t.Fatal("truncated EBML must be an error")
		}
	})
}

func TestMP4AudioDuration(t *testing.T) {
	t.Run("mvhd version 0", func(t *testing.T) {
		d, err := mp4AudioDuration(buildMP4Bytes(0, 1000, 2500))
		if err != nil || d != 2500*time.Millisecond {
			t.Fatalf("duration = %v (%v), want 2.5s", d, err)
		}
	})

	t.Run("mvhd version 1", func(t *testing.T) {
		d, err := mp4AudioDuration(buildMP4Bytes(1, 48000, 96000))
		if err != nil || d != 2*time.Second {
			t.Fatalf("duration = %v (%v), want 2s", d, err)
		}
	})

	t.Run("missing moov is corrupt", func(t *testing.T) {
		data := buildMP4Bytes(0, 1000, 500)
		if _, err := mp4AudioDuration(data[:16]); err == nil {
			t.Fatal("truncated mp4 must be an error")
		}
	})

	t.Run("64-bit largesize extremes are corrupt, not panic", func(t *testing.T) {
		// moov rewritten in the 64-bit size form: the box header grows to 16
		// bytes and largesize occupies the following 8 bytes.
		malformedMP4 := func(largesize uint64) []byte {
			ftyp := buildMP4Bytes(0, 1000, 500)[:20]
			box := append(beU32(1), []byte("moov")...)
			box = append(box, beU64(largesize)...)
			box = append(box, make([]byte, 8)...) // payload filler
			return append(ftyp, box...)
		}

		cases := []struct {
			name      string
			largesize uint64
		}{
			{"negative when cast to int64", ^uint64(0)}, // 0xFFFFFFFFFFFFFFFF
			{"smaller than the 16-byte header", 8},
			{"overflows int64 in off+size", 1<<63 - 1},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if _, err := mp4AudioDuration(malformedMP4(tc.largesize)); err == nil {
					t.Fatalf("largesize %#x must be rejected as corrupt", tc.largesize)
				}
			})
		}
	})
}

// ---------------------------------------------------------------------------
// Upload constraint contract (no database: request shaping only).
// ---------------------------------------------------------------------------

// transcribeUpload drives readTranscriptionAudio with a bare handler so the
// constraint edges run without a database.
func transcribeUpload(t *testing.T, h *Handler, contentType string, body []byte) ([]byte, audioContainer, *voiceASRRequestError) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws/voice-transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	return h.readTranscriptionAudio(rec, req)
}

// multipartBody wraps the given fields into one multipart/form-data body.
func multipartBody(t *testing.T, fields map[string][]byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for name, value := range fields {
		fw, err := mw.CreateFormFile(name, name+".bin")
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := fw.Write(value); err != nil {
			t.Fatalf("write form file: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return buf.Bytes(), mw.FormDataContentType()
}

func TestVoiceTranscriptionUploadConstraints(t *testing.T) {
	const cap = int64(1000)
	h := &Handler{VoiceASRMaxAudioBytes: cap, VoiceASRMaxAudioDuration: 2 * time.Second}

	post := func(t *testing.T, fields map[string][]byte) ([]byte, audioContainer, *voiceASRRequestError) {
		body, contentType := multipartBody(t, fields)
		return transcribeUpload(t, h, contentType, body)
	}

	t.Run("non-multipart request is malformed", func(t *testing.T) {
		_, _, err := transcribeUpload(t, h, "application/json", []byte("{}"))
		if err == nil || err.code != voiceAudioMalformedCode || err.status != http.StatusBadRequest {
			t.Fatalf("err = %+v, want 400 %s", err, voiceAudioMalformedCode)
		}
	})

	t.Run("missing audio part", func(t *testing.T) {
		_, _, err := post(t, map[string][]byte{"other": []byte("x")})
		if err == nil || err.code != voiceAudioRequiredCode || err.status != http.StatusBadRequest {
			t.Fatalf("err = %+v, want 400 %s", err, voiceAudioRequiredCode)
		}
	})

	t.Run("empty audio part", func(t *testing.T) {
		_, _, err := post(t, map[string][]byte{"audio": nil})
		if err == nil || err.code != voiceAudioRequiredCode || err.status != http.StatusBadRequest {
			t.Fatalf("err = %+v, want 400 %s", err, voiceAudioRequiredCode)
		}
	})

	t.Run("exactly the byte cap passes with a valid container", func(t *testing.T) {
		// wav framing is 44 bytes, so dataSize 956 lands the part exactly on
		// the cap; 956 bytes at 1000 B/s = 0.956s ≤ 2s.
		audio, container, err := post(t, map[string][]byte{"audio": buildWavBytes(1000, 956)})
		if err != nil {
			t.Fatalf("boundary upload rejected: %+v", err)
		}
		if int64(len(audio)) != cap {
			t.Fatalf("audio = %d bytes, want exactly the cap %d", len(audio), cap)
		}
		if container.name != "wav" {
			t.Fatalf("container = %q, want wav", container.name)
		}
	})

	t.Run("one byte over the cap is too large", func(t *testing.T) {
		_, _, err := post(t, map[string][]byte{"audio": buildWavBytes(1000, 957)})
		if err == nil || err.code != voiceAudioTooLargeCode || err.status != http.StatusRequestEntityTooLarge {
			t.Fatalf("err = %+v, want 413 %s", err, voiceAudioTooLargeCode)
		}
	})

	t.Run("unknown format is unsupported", func(t *testing.T) {
		_, _, err := post(t, map[string][]byte{"audio": bytes.Repeat([]byte{0x07}, 100)})
		if err == nil || err.code != voiceAudioUnsupportedFormatCode || err.status != http.StatusUnsupportedMediaType {
			t.Fatalf("err = %+v, want 415 %s", err, voiceAudioUnsupportedFormatCode)
		}
	})

	t.Run("duration boundary rides a handler whose byte cap fits the fixture", func(t *testing.T) {
		h2 := &Handler{VoiceASRMaxAudioBytes: 4096, VoiceASRMaxAudioDuration: 2 * time.Second}
		post2 := func(t *testing.T, dataBytes int) *voiceASRRequestError {
			body, contentType := multipartBody(t, map[string][]byte{"audio": buildWavBytes(1000, dataBytes)})
			_, _, err := transcribeUpload(t, h2, contentType, body)
			return err
		}

		t.Run("duration at the cap passes", func(t *testing.T) {
			if err := post2(t, 2000); err != nil {
				t.Fatalf("boundary duration rejected: %+v", err)
			}
		})

		t.Run("duration over the cap is too long", func(t *testing.T) {
			err := post2(t, 2001)
			if err == nil || err.code != voiceAudioTooLongCode || err.status != http.StatusRequestEntityTooLarge {
				t.Fatalf("err = %+v, want 413 %s", err, voiceAudioTooLongCode)
			}
		})
	})

	t.Run("corrupt container is malformed", func(t *testing.T) {
		corrupt := buildWavBytes(1000, 500)
		binary.LittleEndian.PutUint32(corrupt[40:44], 1<<30) // data size ≫ buffer
		_, _, err := post(t, map[string][]byte{"audio": corrupt})
		if err == nil || err.code != voiceAudioMalformedCode || err.status != http.StatusBadRequest {
			t.Fatalf("err = %+v, want 400 %s", err, voiceAudioMalformedCode)
		}
	})

	t.Run("oversized non-audio parts trip the body budget", func(t *testing.T) {
		junk := bytes.Repeat([]byte{0x01}, 100_000)
		_, _, err := post(t, map[string][]byte{"meta": junk, "audio": buildWavBytes(1000, 500)})
		if err == nil || err.code != voiceAudioTooLargeCode || err.status != http.StatusRequestEntityTooLarge {
			t.Fatalf("err = %+v, want 413 %s", err, voiceAudioTooLargeCode)
		}
	})
}

// ---------------------------------------------------------------------------
// DB-backed: chain resolution, provider hop, failure semantics.
// ---------------------------------------------------------------------------

// seedWorkspaceASRSettings points the workspace settings' voice.asr level at
// the given provider ref document.
func seedWorkspaceASRSettings(t *testing.T, wsID, refJSON string) {
	t.Helper()
	dbfx.Exec(t, `UPDATE workspace SET settings = $2::jsonb WHERE id = $1::uuid`, wsID, `{"voice":{"asr":`+refJSON+`}}`)
}

// stubASRProvider is an in-process generateContent peer recording the
// server→provider hop (key header, path, mime, decoded audio) and answering
// a canned payload.
type stubASRProvider struct {
	t *testing.T

	mu       sync.Mutex
	apiKey   string
	path     string
	mimeType string
	audio    []byte
}

func startStubASRProvider(t *testing.T, status int, response string) *stubASRProvider {
	t.Helper()
	s := &stubASRProvider{t: t}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Contents []struct {
				Parts []struct {
					InlineData struct {
						MimeType string `json:"mimeType"`
						Data     string `json:"data"`
					} `json:"inlineData"`
				} `json:"parts"`
			} `json:"contents"`
		}
		_ = json.Unmarshal(body, &payload)
		s.mu.Lock()
		s.apiKey = r.Header.Get("x-goog-api-key")
		s.path = r.URL.Path
		if len(payload.Contents) > 0 && len(payload.Contents[0].Parts) > 0 {
			s.mimeType = payload.Contents[0].Parts[0].InlineData.MimeType
			s.audio, _ = base64.StdEncoding.DecodeString(payload.Contents[0].Parts[0].InlineData.Data)
		}
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	previous := testHandler.VoiceASRBaseURL
	testHandler.VoiceASRBaseURL = srv.URL
	t.Cleanup(func() {
		srv.Close()
		testHandler.VoiceASRBaseURL = previous
	})
	return s
}

func (s *stubASRProvider) snapshot() (apiKey, path, mimeType string, audio []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.apiKey, s.path, s.mimeType, s.audio
}

// postVoiceTranscription drives the handler directly with the workspace id
// in the chi route context (the route gate lives in the router; identity
// checks are covered by the cmd/server gate test).
func postVoiceTranscription(t *testing.T, wsID, contentType string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/"+wsID+"/voice-transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req = withURLParam(req, "id", wsID)
	w := httptest.NewRecorder()
	testHandler.CreateVoiceTranscription(w, req)
	return w
}

// expectTranscriptionRejection asserts a coded rejection body.
func expectTranscriptionRejection(t *testing.T, w *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if w.Code != wantStatus {
		t.Fatalf("status = %d, want %d: %s", w.Code, wantStatus, w.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode rejection: %v (%s)", err, w.Body.String())
	}
	if body["code"] != wantCode {
		t.Fatalf("code = %q, want %q (%s)", body["code"], wantCode, w.Body.String())
	}
}

// seedFullASRWorkspace produces the chain's happy input: a workspace with a
// gemini pool provider carrying a valid credential, referenced by
// settings.voice.asr.
func seedFullASRWorkspace(t *testing.T, name string, providerOver testutil.Cols) (wsID, providerID string) {
	t.Helper()
	box := credentialTestBox(t)
	wsID = dbfx.Workspace(t, name+" ws", handlerTestSlug(name))
	providerID = seedVoiceProviderIn(t, wsID, name, providerOver)
	giveProviderCredential(t, box, providerID, voiceASRTestAPIKey)
	seedWorkspaceASRSettings(t, wsID, `{"provider_id":"`+providerID+`","model":"models/gemini-2.5-flash"}`)
	return wsID, providerID
}

func TestVoiceTranscription_PositivePath(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	wsID, _ := seedFullASRWorkspace(t, "asr-positive", nil)
	stub := startStubASRProvider(t, http.StatusOK,
		`{"candidates":[{"content":{"parts":[{"text":"你好"},{"text":"，世界"}]}}]}`)

	audio := buildWavBytes(8000, 16000) // 2s
	body, contentType := multipartBody(t, map[string][]byte{"audio": audio})
	w := postVoiceTranscription(t, wsID, contentType, body)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	var answer map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode answer: %v (%s)", err, w.Body.String())
	}
	if answer["text"] != "你好，世界" {
		t.Fatalf("text = %q, want the concatenated transcript", answer["text"])
	}
	// The response is exactly {"text"} — no credential or provider metadata
	// ever round-trips to the client.
	if len(answer) != 1 {
		t.Fatalf("answer keys = %v, want only text", answer)
	}

	apiKey, path, mimeType, providerAudio := stub.snapshot()
	if apiKey != voiceASRTestAPIKey {
		t.Fatalf("provider hop key = %q, want the server-held credential", apiKey)
	}
	if path != "/v1beta/models/gemini-2.5-flash:generateContent" {
		t.Fatalf("provider path = %q, want the normalized generateContent endpoint", path)
	}
	if mimeType != "audio/wav" {
		t.Fatalf("provider mime = %q, want the sniffed container mime", mimeType)
	}
	if !bytes.Equal(providerAudio, audio) {
		t.Fatal("provider must receive the exact uploaded bytes")
	}
}

func TestVoiceTranscription_ChainRejections(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	body, contentType := multipartBody(t, map[string][]byte{"audio": buildWavBytes(8000, 8000)})

	t.Run("voice_unconfigured", func(t *testing.T) {
		wsID := dbfx.Workspace(t, "asr-unconf ws", handlerTestSlug("asr-unconf"))
		expectTranscriptionRejection(t, postVoiceTranscription(t, wsID, contentType, body),
			http.StatusConflict, "VOICE_UNAVAILABLE:voice_unconfigured")
	})

	t.Run("voice_provider_missing", func(t *testing.T) {
		wsID := dbfx.Workspace(t, "asr-missing ws", handlerTestSlug("asr-missing"))
		seedWorkspaceASRSettings(t, wsID, `{"provider_id":"00000000-0000-0000-0000-000000000001"}`)
		expectTranscriptionRejection(t, postVoiceTranscription(t, wsID, contentType, body),
			http.StatusConflict, "VOICE_UNAVAILABLE:voice_provider_missing")
	})

	t.Run("cross-workspace provider resolves as missing", func(t *testing.T) {
		otherWS := dbfx.Workspace(t, "asr-foreign ws", handlerTestSlug("asr-foreign"))
		foreignProvider := seedVoiceProviderIn(t, otherWS, "asr-foreign-provider", nil)
		wsID := dbfx.Workspace(t, "asr-scope ws", handlerTestSlug("asr-scope"))
		seedWorkspaceASRSettings(t, wsID, `{"provider_id":"`+foreignProvider+`"}`)
		expectTranscriptionRejection(t, postVoiceTranscription(t, wsID, contentType, body),
			http.StatusConflict, "VOICE_UNAVAILABLE:voice_provider_missing")
	})

	t.Run("voice_provider_disabled", func(t *testing.T) {
		wsID, _ := seedFullASRWorkspace(t, "asr-disabled", testutil.Cols{"enabled": false})
		expectTranscriptionRejection(t, postVoiceTranscription(t, wsID, contentType, body),
			http.StatusConflict, "VOICE_UNAVAILABLE:voice_provider_disabled")
	})

	t.Run("capability_mismatch", func(t *testing.T) {
		wsID, _ := seedFullASRWorkspace(t, "asr-chatgpt", testutil.Cols{"provider_type": "chatgpt"})
		expectTranscriptionRejection(t, postVoiceTranscription(t, wsID, contentType, body),
			http.StatusConflict, "VOICE_UNAVAILABLE:capability_mismatch")
	})

	t.Run("credential_missing", func(t *testing.T) {
		wsID := dbfx.Workspace(t, "asr-nokey ws", handlerTestSlug("asr-nokey"))
		providerID := seedVoiceProviderIn(t, wsID, "asr-nokey", nil)
		seedWorkspaceASRSettings(t, wsID, `{"provider_id":"`+providerID+`"}`)
		expectTranscriptionRejection(t, postVoiceTranscription(t, wsID, contentType, body),
			http.StatusConflict, "VOICE_UNAVAILABLE:credential_missing")
	})

	t.Run("credential_invalid_probe", func(t *testing.T) {
		wsID, _ := seedFullASRWorkspace(t, "asr-badprobe", testutil.Cols{
			"metadata": testutil.Raw(`'{"credential_probe":{"status":"invalid","checked_at":"2026-10-11T00:00:00Z"}}'::jsonb`),
		})
		expectTranscriptionRejection(t, postVoiceTranscription(t, wsID, contentType, body),
			http.StatusConflict, "VOICE_UNAVAILABLE:credential_invalid")
	})

	t.Run("credential_invalid_ciphertext", func(t *testing.T) {
		wsID := dbfx.Workspace(t, "asr-corrupt ws", handlerTestSlug("asr-corrupt"))
		providerID := seedVoiceProviderIn(t, wsID, "asr-corrupt", nil)
		credentialTestBox(t)
		dbfx.Exec(t,
			`INSERT INTO runtime_credential (runtime_instance_id, credential_key, secret_encrypted) VALUES ($1::uuid, 'api_key', '\xdeadbeef'::bytea)`,
			providerID)
		dbfx.Exec(t, `UPDATE voice_provider SET credential_ref = $2::text WHERE id = $1::uuid`, providerID, providerID+":api_key")
		seedWorkspaceASRSettings(t, wsID, `{"provider_id":"`+providerID+`"}`)
		expectTranscriptionRejection(t, postVoiceTranscription(t, wsID, contentType, body),
			http.StatusConflict, "VOICE_UNAVAILABLE:credential_invalid")
	})

	t.Run("malformed provider reference resolves as missing", func(t *testing.T) {
		wsID := dbfx.Workspace(t, "asr-malformed ws", handlerTestSlug("asr-malformed"))
		seedWorkspaceASRSettings(t, wsID, `{"provider_id":"not-a-uuid"}`)
		expectTranscriptionRejection(t, postVoiceTranscription(t, wsID, contentType, body),
			http.StatusConflict, "VOICE_UNAVAILABLE:voice_provider_missing")
	})
}

func TestVoiceTranscription_ProviderFailureSemantics(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	body, contentType := multipartBody(t, map[string][]byte{"audio": buildWavBytes(8000, 8000)})
	probeBadge := func(t *testing.T, providerID string) string {
		t.Helper()
		var badge *string
		if err := testPool.QueryRow(context.Background(),
			`SELECT metadata->'credential_probe'->>'status' FROM voice_provider WHERE id = $1::uuid`,
			providerID).Scan(&badge); err != nil {
			t.Fatalf("load badge: %v", err)
		}
		if badge == nil {
			return ""
		}
		return *badge
	}

	t.Run("unreachable provider answers 502 and records the badge", func(t *testing.T) {
		wsID, providerID := seedFullASRWorkspace(t, "asr-unreachable", nil)
		dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		deadURL := dead.URL
		dead.Close()
		previous := testHandler.VoiceASRBaseURL
		testHandler.VoiceASRBaseURL = deadURL
		t.Cleanup(func() { testHandler.VoiceASRBaseURL = previous })

		w := postVoiceTranscription(t, wsID, contentType, body)
		expectTranscriptionRejection(t, w, http.StatusBadGateway, "VOICE_PROVIDER_UNREACHABLE")
		if strings.Contains(w.Body.String(), voiceASRTestAPIKey) {
			t.Fatal("the provider key must never appear in the client answer")
		}
		if badge := probeBadge(t, providerID); badge != "unreachable" {
			t.Fatalf("badge = %q, want unreachable", badge)
		}
	})

	t.Run("provider key rejection records an invalid badge", func(t *testing.T) {
		wsID, providerID := seedFullASRWorkspace(t, "asr-rejected", nil)
		startStubASRProvider(t, http.StatusUnauthorized, `{"error":"denied"}`)

		w := postVoiceTranscription(t, wsID, contentType, body)
		expectTranscriptionRejection(t, w, http.StatusBadGateway, "VOICE_PROVIDER_UNREACHABLE")
		if strings.Contains(w.Body.String(), voiceASRTestAPIKey) {
			t.Fatal("the provider key must never appear in the client answer")
		}
		if badge := probeBadge(t, providerID); badge != "invalid" {
			t.Fatalf("badge = %q, want invalid", badge)
		}
	})

	t.Run("provider status matrix keeps failures private and classifies the probe badge", func(t *testing.T) {
		cases := []struct {
			name      string
			status    int
			wantBadge string
		}{
			{name: "forbidden key", status: http.StatusForbidden, wantBadge: "invalid"},
			{name: "rate limited", status: http.StatusTooManyRequests, wantBadge: "unreachable"},
			{name: "provider server error", status: http.StatusInternalServerError, wantBadge: "unreachable"},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				wsID, providerID := seedFullASRWorkspace(t, "asr-"+tc.name, nil)
				startStubASRProvider(t, tc.status, `{"error":"provider detail must not reach the client"}`)

				w := postVoiceTranscription(t, wsID, contentType, body)
				expectTranscriptionRejection(t, w, http.StatusBadGateway, "VOICE_PROVIDER_UNREACHABLE")
				if strings.Contains(w.Body.String(), "provider detail") || strings.Contains(w.Body.String(), voiceASRTestAPIKey) {
					t.Fatal("provider response details and API keys must not reach the client")
				}
				if badge := probeBadge(t, providerID); badge != tc.wantBadge {
					t.Fatalf("badge = %q, want %q", badge, tc.wantBadge)
				}
			})
		}
	})

	t.Run("unusable 2xx answer is a provider fault with the badge untouched", func(t *testing.T) {
		wsID, providerID := seedFullASRWorkspace(t, "asr-garbage", nil)
		startStubASRProvider(t, http.StatusOK, `not-json-at-all`)

		w := postVoiceTranscription(t, wsID, contentType, body)
		expectTranscriptionRejection(t, w, http.StatusBadGateway, "VOICE_PROVIDER_UNREACHABLE")
		if badge := probeBadge(t, providerID); badge != "" {
			t.Fatalf("badge = %q, want untouched", badge)
		}
	})

	t.Run("empty transcription is a valid empty answer", func(t *testing.T) {
		wsID, _ := seedFullASRWorkspace(t, "asr-silence", nil)
		startStubASRProvider(t, http.StatusOK, `{"candidates":[]}`)

		w := postVoiceTranscription(t, wsID, contentType, body)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
		}
		if strings.TrimSpace(w.Body.String()) != `{"text":""}` {
			t.Fatalf("body = %s, want the empty transcript answer", w.Body.String())
		}
	})
}
