package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ---------------------------------------------------------------------------
// One-shot dictation transcription (RUYI-710, stage 1).
//
// POST /api/workspaces/{id}/voice-transcriptions: audio → text for clients
// whose on-device speech recognition is unavailable or failed. multipart/
// form-data with a single file part named "audio"; the answer is
// {"text": "..."} — an empty string is a valid answer (silence transcribes
// to nothing, it is not an error).
//
// The endpoint mirrors what the Live gateway owns, in a synchronous shape:
// the workspace's settings.voice.asr resolves through the D2 chain
// discipline (workspace level only — there is deliberately no agent-level
// ASR override), the decrypted key rides only the server→provider hop, and
// provider-call failures classify onto the pool row's probe badge exactly
// like a gateway dial failure (401/403 → invalid, everything else →
// unreachable; neither blocks resolution — RUYI-619 semantics).
//
// Upload constraint contract, each rejection with a stable code so mobile
// can branch without parsing prose:
//   - a file part named "audio" is required — 400 VOICE_AUDIO_REQUIRED
//   - the container must be wav / webm / mp4(m4a), sniffed from magic
//     bytes (client-declared headers are never trusted) —
//     415 VOICE_AUDIO_UNSUPPORTED_FORMAT
//   - audio bytes ≤ VoiceASRMaxAudioBytes (default 20 MiB, the tightest
//     provider inline limit) — 413 VOICE_AUDIO_TOO_LARGE
//   - container-declared duration ≤ VoiceASRMaxAudioDuration (default
//     5m; bytes alone cannot bound compressed audio — 20 MiB of opus is
//     nearly two hours) — 413 VOICE_AUDIO_TOO_LONG
//   - a whitelisted container whose duration cannot be read is corrupt —
//     400 VOICE_AUDIO_MALFORMED
//
// Audio never touches disk: the multipart body is consumed through
// MultipartReader (ParseMultipartForm would spool large parts to temporary
// files) into a buffer capped at max+1 bytes, so a hostile body cannot
// balloon memory either. The provider call carries the base64 payload
// inline; nothing is persisted and no transcription record exists.
// ---------------------------------------------------------------------------

const (
	// defaultVoiceASRModel is the generateContent model used when the ASR
	// chain ref carries no model of its own (same normalization rules as
	// the Live chain: trimmed, "models/" prefix stripped).
	defaultVoiceASRModel = "gemini-2.5-flash"

	// DefaultVoiceASRBaseURL is the provider origin for the one-shot
	// generateContent call. Wired from MULTICA_GEMINI_ASR_BASE_URL so
	// air-gapped deployments and tests can aim it at a stub.
	DefaultVoiceASRBaseURL = "https://generativelanguage.googleapis.com"

	// defaultVoiceASRTimeout bounds one synchronous transcription call.
	defaultVoiceASRTimeout = 60 * time.Second

	// defaultVoiceASRMaxAudioBytes bounds the in-memory audio buffer.
	defaultVoiceASRMaxAudioBytes = int64(20) << 20

	// defaultVoiceASRMaxAudioDuration bounds the container-declared length.
	defaultVoiceASRMaxAudioDuration = 5 * time.Minute

	// voiceASRMultipartOverhead is the slack over the audio cap when
	// bounding the whole multipart body (framing + small metadata fields).
	voiceASRMultipartOverhead = int64(64) << 10
)

// Stable rejection codes of the upload constraint contract.
const (
	voiceAudioRequiredCode          = "VOICE_AUDIO_REQUIRED"
	voiceAudioMalformedCode         = "VOICE_AUDIO_MALFORMED"
	voiceAudioUnsupportedFormatCode = "VOICE_AUDIO_UNSUPPORTED_FORMAT"
	voiceAudioTooLargeCode          = "VOICE_AUDIO_TOO_LARGE"
	voiceAudioTooLongCode           = "VOICE_AUDIO_TOO_LONG"
)

// errVoiceASRResponseUnusable marks a 2xx provider answer that violates the
// generateContent contract — a provider fault, not a key or reachability
// verdict, so the probe badge stays untouched.
var errVoiceASRResponseUnusable = errors.New("voice provider returned an unusable transcription response")

// errAudioDurationUnknown marks a whitelisted container whose declared
// length cannot be read: corrupt or truncated.
var errAudioDurationUnknown = errors.New("audio container duration could not be determined")

// voiceASRRequestError is a request-shape rejection with a stable code.
type voiceASRRequestError struct {
	status int
	code   string
	detail string
}

func (e *voiceASRRequestError) write(w http.ResponseWriter) {
	writeErrorCode(w, e.status, e.code, e.detail)
}

func (e *voiceASRRequestError) Error() string { return e.code + ": " + e.detail }

// asrServableVoiceProviderType maps a pool entry's provider_type to the
// one-shot transcription dialect the server speaks today. Mirrors
// gatewayServableVoiceProviderType: the pool may hold every whitelisted
// type, but ASR speaks only the Gemini generateContent dialect — widening
// (e.g. an OpenAI-compatible audio/transcriptions hop) is additive and
// safe before this catches up.
func asrServableVoiceProviderType(providerType string) bool {
	return providerType == "gemini"
}

// voiceASRMaxAudioBytes / voiceASRMaxAudioDuration resolve the upload
// constraint budget; zero fields mean the production defaults.
func (h *Handler) voiceASRMaxAudioBytes() int64 {
	if h.VoiceASRMaxAudioBytes > 0 {
		return h.VoiceASRMaxAudioBytes
	}
	return defaultVoiceASRMaxAudioBytes
}

func (h *Handler) voiceASRMaxAudioDuration() time.Duration {
	if h.VoiceASRMaxAudioDuration > 0 {
		return h.VoiceASRMaxAudioDuration
	}
	return defaultVoiceASRMaxAudioDuration
}

// voiceASRBaseURL resolves the injectable transcription provider origin.
func (h *Handler) voiceASRBaseURL() string {
	if h.VoiceASRBaseURL != "" {
		return h.VoiceASRBaseURL
	}
	return DefaultVoiceASRBaseURL
}

// resolveWorkspaceASRTarget is the D2 chain discipline applied to the
// workspace ASR level: settings.voice.asr → pool row → credential. Missing
// and invalid end in rejections the client renders as degraded states; the
// provider hop owns the unreachable verdict (a network conclusion is the
// call's business, never the entry's — RUYI-619).
func (h *Handler) resolveWorkspaceASRTarget(ctx context.Context, workspaceID pgtype.UUID) (*voiceTarget, *voiceGateRejection, error) {
	ws, err := h.Queries.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	voice := parseWorkspaceVoiceSettings(ws.Settings)
	if voice == nil || voice.ASR == nil || strings.TrimSpace(voice.ASR.ProviderID) == "" {
		return nil, &voiceGateRejection{"voice_unconfigured", "no ASR provider is configured for this workspace"}, nil
	}
	ref := voice.ASR
	providerUUID, err := util.ParseUUID(ref.ProviderID)
	if err != nil {
		// A malformed reference cannot name a real pool row; it resolves as
		// missing (read path tolerant, write path strict — same as the D2
		// chain).
		return nil, &voiceGateRejection{"voice_provider_missing", "the configured ASR provider no longer exists"}, nil
	}
	provider, err := h.Queries.GetVoiceProvider(ctx, providerUUID)
	if err != nil || provider.WorkspaceID != workspaceID {
		// Cross-workspace references are an application-integrity violation
		// (no FK, house rule); they resolve as missing, never as usable.
		return nil, &voiceGateRejection{"voice_provider_missing", "the configured ASR provider no longer exists"}, nil
	}
	if !provider.Enabled {
		return nil, &voiceGateRejection{"voice_provider_disabled", "the configured ASR provider is disabled"}, nil
	}
	if !asrServableVoiceProviderType(provider.ProviderType) {
		return nil, &voiceGateRejection{"capability_mismatch", "this ASR provider's type is not supported by the server yet"}, nil
	}

	keyName, ok := voiceProviderCredentialKey(provider)
	if !ok {
		return nil, &voiceGateRejection{"credential_missing", "this ASR provider has no API key configured"}, nil
	}
	credential, err := h.Queries.GetRuntimeCredential(ctx, db.GetRuntimeCredentialParams{
		RuntimeInstanceID: provider.ID,
		CredentialKey:     keyName,
	})
	if err != nil {
		return nil, &voiceGateRejection{"credential_missing", "this ASR provider's API key is missing"}, nil
	}
	if lastVoiceProviderProbeInvalid(provider) {
		return nil, &voiceGateRejection{"credential_invalid", "this ASR provider's API key failed its last connectivity check"}, nil
	}
	if h.RuntimeCredentialBox == nil {
		// Same fail-closed contract as the credential PUT: without the
		// encryption key the server must not fall back to plaintext hops.
		return nil, nil, errVoiceCredentialEncryptionMissing
	}
	plaintext, err := h.RuntimeCredentialBox.Open(credential.SecretEncrypted)
	if err != nil {
		return nil, &voiceGateRejection{"credential_invalid", "this ASR provider's stored API key cannot be decrypted"}, nil
	}

	model := normalizeVoiceModel(ref.Model)
	if model == "" {
		model = defaultVoiceASRModel
	}
	return &voiceTarget{
		provider:     &provider,
		apiKey:       string(plaintext),
		model:        model,
		configSource: "workspace_asr",
	}, nil, nil
}

// CreateVoiceTranscription handles POST /api/workspaces/{id}/voice-transcriptions
// (member gate on the route): validate the upload, resolve the workspace's
// ASR chain, call the provider, return the text.
func (h *Handler) CreateVoiceTranscription(w http.ResponseWriter, r *http.Request) {
	wsUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "workspace id")
	if !ok {
		return
	}

	// Request shape first: cheap, provider-independent feedback before any
	// database work, so a bad upload never costs a chain resolution.
	audio, container, reqErr := h.readTranscriptionAudio(w, r)
	if reqErr != nil {
		reqErr.write(w)
		return
	}

	target, rejection, serverErr := h.resolveWorkspaceASRTarget(r.Context(), wsUUID)
	if serverErr != nil {
		if errors.Is(serverErr, errVoiceCredentialEncryptionMissing) {
			writeError(w, http.StatusServiceUnavailable, "runtime credential encryption is not configured on this server (MULTICA_RUNTIME_CREDENTIAL_SECRET_KEY missing)")
			return
		}
		slog.Error("voice transcription gate failed", "workspace_id", uuidToString(wsUUID), "error", serverErr)
		writeError(w, http.StatusServiceUnavailable, "voice transcription unavailable due to a server configuration problem")
		return
	}
	if rejection != nil {
		voiceUnavailable(w, rejection.reason, rejection.detail)
		return
	}

	text, resp, err := h.transcribeWithProvider(r.Context(), target, container, audio)
	if err != nil {
		// Provider-hop failures classify exactly like a gateway dial
		// failure: 401/403 is the provider rejecting the key (invalid
		// badge), everything else could not evaluate it (unreachable
		// badge); an unusable 2xx contract violation says nothing about
		// the key, so the badge stays untouched. The key itself is never
		// logged and never echoed — the client answer is a stable code.
		if !errors.Is(err, errVoiceASRResponseUnusable) {
			h.recordDialFailureBadge(r.Context(), target, resp)
		}
		slog.Warn("voice transcription provider call failed",
			"workspace_id", uuidToString(wsUUID),
			"voice_provider_id", uuidToString(target.provider.ID),
			"model", target.model,
			"audio_bytes", len(audio),
			"http_status", httpResponseStatus(resp),
			"error", err)
		writeErrorCode(w, http.StatusBadGateway, "VOICE_PROVIDER_UNREACHABLE", "could not reach the voice provider")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}

// readTranscriptionAudio streams the multipart body into a size-bounded
// in-memory buffer and validates the container constraints. Returns the
// audio bytes and the sniffed container, or a coded rejection.
func (h *Handler) readTranscriptionAudio(w http.ResponseWriter, r *http.Request) ([]byte, audioContainer, *voiceASRRequestError) {
	maxAudio := h.voiceASRMaxAudioBytes()
	maxDur := h.voiceASRMaxAudioDuration()

	mediaType := r.Header.Get("Content-Type")
	if !strings.HasPrefix(strings.ToLower(mediaType), "multipart/form-data") {
		return nil, audioContainer{}, &voiceASRRequestError{http.StatusBadRequest, voiceAudioMalformedCode, "expected multipart/form-data with an audio file part"}
	}
	// Bound the whole body: audio cap plus multipart framing slack. Every
	// read below fails once the body exceeds it — oversized uploads are
	// rejected without ever being buffered whole.
	r.Body = http.MaxBytesReader(w, r.Body, maxAudio+voiceASRMultipartOverhead)

	mr, err := r.MultipartReader()
	if err != nil {
		return nil, audioContainer{}, &voiceASRRequestError{http.StatusBadRequest, voiceAudioMalformedCode, "malformed multipart body"}
	}
	var audio []byte
	var bodyTooLarge bool
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			bodyTooLarge = bodyTooLarge || isMaxBytesError(err)
			break
		}
		if part.FormName() != "audio" {
			// Metadata fields are ignored, but still bounded by the body cap.
			_, _ = io.Copy(io.Discard, part)
			continue
		}
		buf := &bytes.Buffer{}
		_, err = io.Copy(buf, io.LimitReader(part, maxAudio+1))
		if err != nil {
			bodyTooLarge = bodyTooLarge || isMaxBytesError(err)
			break
		}
		if int64(buf.Len()) > maxAudio {
			return nil, audioContainer{}, &voiceASRRequestError{http.StatusRequestEntityTooLarge, voiceAudioTooLargeCode, "audio exceeds the maximum upload size"}
		}
		audio = buf.Bytes()
	}
	if bodyTooLarge {
		return nil, audioContainer{}, &voiceASRRequestError{http.StatusRequestEntityTooLarge, voiceAudioTooLargeCode, "upload exceeds the maximum size"}
	}
	if len(audio) == 0 {
		return nil, audioContainer{}, &voiceASRRequestError{http.StatusBadRequest, voiceAudioRequiredCode, `an audio file part named "audio" is required`}
	}
	container, ok := sniffAudioContainer(audio)
	if !ok {
		return nil, audioContainer{}, &voiceASRRequestError{http.StatusUnsupportedMediaType, voiceAudioUnsupportedFormatCode, "audio must be wav, webm, or mp4 (m4a)"}
	}
	duration, err := container.duration(audio)
	if err != nil {
		return nil, audioContainer{}, &voiceASRRequestError{http.StatusBadRequest, voiceAudioMalformedCode, "audio container is malformed or truncated"}
	}
	if duration > maxDur {
		return nil, audioContainer{}, &voiceASRRequestError{http.StatusRequestEntityTooLarge, voiceAudioTooLongCode, fmt.Sprintf("audio exceeds the maximum duration of %s", maxDur)}
	}
	return audio, container, nil
}

// isMaxBytesError reports whether err is the MaxBytesReader budget trip.
func isMaxBytesError(err error) bool {
	var maxErr *http.MaxBytesError
	return errors.As(err, &maxErr)
}

// httpResponseStatus is a nil-safe status read for log lines.
func httpResponseStatus(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

// transcribeWithProvider performs the server→provider generateContent hop
// and extracts the transcript text. The key rides the provider-expected
// header only — same discipline as the Live dial and the probe: never the
// URL, never a log line, never echoed to the client.
func (h *Handler) transcribeWithProvider(ctx context.Context, target *voiceTarget, container audioContainer, audio []byte) (string, *http.Response, error) {
	endpoint := h.voiceASRBaseURL() + "/v1beta/models/" + url.PathEscape(target.model) + ":generateContent"
	payload, err := json.Marshal(map[string]any{
		"contents": []map[string]any{{
			"parts": []map[string]any{{
				"inlineData": map[string]string{
					"mimeType": container.mimeType,
					"data":     base64.StdEncoding.EncodeToString(audio),
				},
			}},
		}},
	})
	if err != nil {
		return "", nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, defaultVoiceASRTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("x-goog-api-key", target.apiKey)
	req.Header.Set("Content-Type", "application/json")

	// The probe client's precedence chain (injected test client > configured
	// proxy transport > plain default) is exactly the egress discipline this
	// hop must answer to: the transcription call rides the same path the
	// probe validated the key on.
	resp, err := h.voiceProbeHTTPClient().Do(req)
	if err != nil {
		return "", nil, err
	}
	defer func() {
		_, _ = io.CopyN(io.Discard, resp.Body, 4096)
		resp.Body.Close()
	}()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", resp, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// 401/403 is the provider evaluating and rejecting the key; every
		// other status is undecidable (probe parity). Either way the client
		// learns only the stable unreachable code — the response body is
		// never relayed.
		return "", resp, fmt.Errorf("voice provider answered %d", resp.StatusCode)
	}
	var decoded struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if json.Unmarshal(raw, &decoded) != nil {
		return "", resp, errVoiceASRResponseUnusable
	}
	var sb strings.Builder
	for _, c := range decoded.Candidates {
		for _, p := range c.Content.Parts {
			sb.WriteString(p.Text)
		}
	}
	return sb.String(), resp, nil
}

// ---------------------------------------------------------------------------
// Audio container sniffing and duration reading.
// ---------------------------------------------------------------------------

// audioContainer is a sniffed upload container: the family name and the
// MIME type the provider hop declares for it (sniffed, never client-said).
type audioContainer struct {
	name     string
	mimeType string
}

// duration reads the container-declared presentation length. A whitelisted
// container that cannot yield one is corrupt — callers reject it rather
// than guess.
func (c audioContainer) duration(data []byte) (time.Duration, error) {
	switch c.name {
	case "wav":
		return wavAudioDuration(data)
	case "webm":
		return webmAudioDuration(data)
	case "mp4":
		return mp4AudioDuration(data)
	}
	return 0, errAudioDurationUnknown
}

var ebmlMagic = []byte{0x1A, 0x45, 0xDF, 0xA3}

// sniffAudioContainer identifies the container from magic bytes. Formats
// outside the whitelist (everything real dictation clients record today)
// are rejected with the unsupported-format contract; widening later is
// additive.
func sniffAudioContainer(data []byte) (audioContainer, bool) {
	switch {
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WAVE":
		return audioContainer{name: "wav", mimeType: "audio/wav"}, true
	case len(data) >= 4 && bytes.Equal(data[:4], ebmlMagic):
		return audioContainer{name: "webm", mimeType: "audio/webm"}, true
	case len(data) >= 12 && string(data[4:8]) == "ftyp":
		return audioContainer{name: "mp4", mimeType: "audio/mp4"}, true
	}
	return audioContainer{}, false
}

// wavAudioDuration divides the data-chunk size by the fmt chunk's average
// byte rate. The chunk walk honors RIFF word alignment (odd-sized chunks
// carry one pad byte).
func wavAudioDuration(data []byte) (time.Duration, error) {
	if len(data) < 12 {
		return 0, errAudioDurationUnknown
	}
	var byteRate uint32
	var dataSize int64
	haveFmt, haveData := false, false
	off := int64(12)
	for off+8 <= int64(len(data)) {
		id := string(data[off : off+4])
		size := int64(binary.LittleEndian.Uint32(data[off+4 : off+8]))
		body := off + 8
		switch id {
		case "fmt ":
			if size < 16 || body+size > int64(len(data)) {
				return 0, errAudioDurationUnknown
			}
			byteRate = binary.LittleEndian.Uint32(data[body+8 : body+12])
			haveFmt = true
		case "data":
			if body+size > int64(len(data)) {
				return 0, errAudioDurationUnknown
			}
			dataSize = size
			haveData = true
		}
		off = body + size + (size & 1)
	}
	if !haveFmt || !haveData || byteRate == 0 || dataSize < 0 {
		return 0, errAudioDurationUnknown
	}
	return time.Duration(float64(dataSize) / float64(byteRate) * float64(time.Second)), nil
}

// EBML element ids the webm duration reader needs.
const (
	ebmlIDSegment        = 0x18538067
	ebmlIDInfo           = 0x1549A966
	ebmlIDTimestampScale = 0x2AD7B1
	ebmlIDDuration       = 0x4489
)

// webmAudioDuration reads Segment → Info → Duration (float, in
// TimestampScale nanoseconds — default 1e6). Unknown-size masters (the
// streaming-recorder norm) extend to the enclosing end.
func webmAudioDuration(data []byte) (time.Duration, error) {
	scale := uint64(1_000_000)
	var duration float64
	haveDuration := false

	off := 0
	for off < len(data) {
		id, idLen, ok := ebmlID(data, off)
		if !ok {
			return 0, errAudioDurationUnknown
		}
		size, unknown, sizeLen, ok := ebmlSize(data, off+idLen)
		if !ok {
			return 0, errAudioDurationUnknown
		}
		bodyStart := off + idLen + sizeLen
		bodyEnd := len(data)
		if !unknown {
			if size < 0 || bodyStart+int(size) > len(data) {
				return 0, errAudioDurationUnknown
			}
			bodyEnd = bodyStart + int(size)
		}
		if id == ebmlIDSegment {
			soff := bodyStart
			for soff < bodyEnd {
				sid, sidLen, ok := ebmlID(data, soff)
				if !ok {
					return 0, errAudioDurationUnknown
				}
				ssize, sUnknown, sSizeLen, ok := ebmlSize(data, soff+sidLen)
				if !ok {
					return 0, errAudioDurationUnknown
				}
				sBody := soff + sidLen + sSizeLen
				sEnd := bodyEnd
				if !sUnknown {
					if ssize < 0 || sBody+int(ssize) > bodyEnd {
						return 0, errAudioDurationUnknown
					}
					sEnd = sBody + int(ssize)
				}
				if sid == ebmlIDInfo {
					ioff := sBody
					for ioff < sEnd {
						iid, iidLen, ok := ebmlID(data, ioff)
						if !ok {
							return 0, errAudioDurationUnknown
						}
						isize, iUnknown, iSizeLen, ok := ebmlSize(data, ioff+iidLen)
						if !ok {
							return 0, errAudioDurationUnknown
						}
						iBody := ioff + iidLen + iSizeLen
						iEnd := sEnd
						if !iUnknown {
							if isize < 0 || iBody+int(isize) > sEnd {
								return 0, errAudioDurationUnknown
							}
							iEnd = iBody + int(isize)
						}
						switch iid {
						case ebmlIDTimestampScale:
							if v, ok := ebmlUint(data[iBody:iEnd]); ok {
								scale = v
							}
						case ebmlIDDuration:
							if v, ok := ebmlFloat(data[iBody:iEnd]); ok {
								duration = v
								haveDuration = true
							}
						}
						ioff = iEnd
					}
				}
				soff = sEnd
			}
		}
		off = bodyEnd
	}
	if !haveDuration || duration < 0 {
		return 0, errAudioDurationUnknown
	}
	return time.Duration(duration * float64(scale)), nil
}

// ebmlID reads an element-id vint (marker bit kept).
func ebmlID(data []byte, off int) (id uint32, length int, ok bool) {
	if off >= len(data) || data[off] == 0 {
		return 0, 0, false
	}
	length = ebmlVintLength(data[off])
	if length == 0 || length > 4 || off+length > len(data) {
		return 0, 0, false
	}
	id = uint32(data[off])
	for i := 1; i < length; i++ {
		id = id<<8 | uint32(data[off+i])
	}
	return id, length, true
}

// ebmlSize reads a size vint (marker bit stripped); the all-ones value
// means "unknown size".
func ebmlSize(data []byte, off int) (size int64, unknown bool, length int, ok bool) {
	if off >= len(data) || data[off] == 0 {
		return 0, false, 0, false
	}
	length = ebmlVintLength(data[off])
	if length == 0 || length > 8 || off+length > len(data) {
		return 0, false, 0, false
	}
	value := uint64(data[off]) & ((1 << (8 - length)) - 1)
	for i := 1; i < length; i++ {
		value = value<<8 | uint64(data[off+i])
	}
	if value == (1<<uint(7*length))-1 {
		return 0, true, length, true
	}
	return int64(value), false, length, true
}

func ebmlVintLength(first byte) int {
	for i := 0; i < 8; i++ {
		if first&(1<<(7-i)) != 0 {
			return i + 1
		}
	}
	return 0
}

// ebmlUint reads a big-endian unsigned scalar (≤8 bytes).
func ebmlUint(body []byte) (uint64, bool) {
	if len(body) == 0 || len(body) > 8 {
		return 0, false
	}
	var v uint64
	for _, b := range body {
		v = v<<8 | uint64(b)
	}
	return v, true
}

// ebmlFloat reads an IEEE-754 float of width 4 or 8.
func ebmlFloat(body []byte) (float64, bool) {
	switch len(body) {
	case 4:
		return float64(math.Float32frombits(binary.BigEndian.Uint32(body))), true
	case 8:
		return math.Float64frombits(binary.BigEndian.Uint64(body)), true
	}
	return 0, false
}

// mp4AudioDuration reads moov → mvhd (version 0 or 1): presentation length
// is the mvhd duration in mvhd timescale ticks.
func mp4AudioDuration(data []byte) (time.Duration, error) {
	timescale, duration, ok := findMP4Box(data, "moov", "mvhd")
	if !ok || timescale <= 0 || duration < 0 {
		return 0, errAudioDurationUnknown
	}
	return time.Duration(float64(duration) / float64(timescale) * float64(time.Second)), nil
}

// findMP4Box walks top-level ISO-BMFF boxes for outer, then its children
// for inner, and decodes the mvhd timescale/duration pair. Box sizes are
// 32-bit, 64-bit (size==1), and to-end (size==0) forms.
func findMP4Box(data []byte, outer, inner string) (timescale, duration int64, ok bool) {
	end := int64(len(data))
	off := int64(0)
	for off+8 <= end {
		size := int64(binary.BigEndian.Uint32(data[off : off+4]))
		typ := string(data[off+4 : off+8])
		header := int64(8)
		var ok2 bool
		size, header, ok2 = mp4BoxSize(data, off, size, header)
		if !ok2 {
			return 0, 0, false
		}
		if typ == outer {
			if ts, dur, found := findMP4Child(data[off+header:off+size], inner); found {
				return ts, dur, true
			}
		}
		off += size
	}
	return 0, 0, false
}

func mp4BoxSize(data []byte, off, size, header int64) (int64, int64, bool) {
	end := int64(len(data))
	switch size {
	case 1:
		if off+16 > end {
			return 0, 0, false
		}
		size = int64(binary.BigEndian.Uint64(data[off+8 : off+16]))
		// 16, not 8: the 64-bit form's own header. The cast above can go
		// negative, and size > end-off avoids overflowing off+size.
		if size < 16 || size > end-off {
			return 0, 0, false
		}
		return size, 16, true
	case 0:
		return end - off, 8, true
	}
	if size < 8 || off+size > end {
		return 0, 0, false
	}
	return size, header, true
}

func findMP4Child(body []byte, want string) (timescale, duration int64, ok bool) {
	end := int64(len(body))
	off := int64(0)
	for off+8 <= end {
		size := int64(binary.BigEndian.Uint32(body[off : off+4]))
		typ := string(body[off+4 : off+8])
		header := int64(8)
		var ok2 bool
		size, header, ok2 = mp4BoxSize(body, off, size, header)
		if !ok2 {
			return 0, 0, false
		}
		if typ == want {
			inner := body[off+header : off+size]
			if len(inner) < 4 {
				return 0, 0, false
			}
			switch inner[0] { // version
			case 0:
				if len(inner) < 20 {
					return 0, 0, false
				}
				return int64(binary.BigEndian.Uint32(inner[12:16])), int64(binary.BigEndian.Uint32(inner[16:20])), true
			case 1:
				if len(inner) < 32 {
					return 0, 0, false
				}
				return int64(binary.BigEndian.Uint32(inner[20:24])), int64(binary.BigEndian.Uint64(inner[24:32])), true
			}
			return 0, 0, false
		}
		off += size
	}
	return 0, 0, false
}
