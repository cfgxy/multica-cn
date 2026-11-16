package agent

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"strings"
)

type claudeTranscriptProvenance struct {
	StartOffset int64
	EndOffset   int64
	SHA256      string
	Compactions []CompactProvenanceEvent
}

// transcriptRunProvenance scans only the bytes written by this execution. It
// stores hashes and offsets so operators can prove a boundary without exposing
// transcript or compact-summary text.
func transcriptRunProvenance(path string, startOffset int64) (claudeTranscriptProvenance, bool) {
	fh, err := os.Open(path)
	if err != nil {
		return claudeTranscriptProvenance{}, false
	}
	defer fh.Close()
	st, err := fh.Stat()
	if err != nil {
		return claudeTranscriptProvenance{}, false
	}
	if startOffset < 0 || startOffset > st.Size() {
		startOffset = 0
	}
	if _, err := fh.Seek(startOffset, io.SeekStart); err != nil {
		return claudeTranscriptProvenance{}, false
	}

	hash := sha256.New()
	result := claudeTranscriptProvenance{StartOffset: startOffset}
	scanner := bufio.NewScanner(fh)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var offset = startOffset
	for scanner.Scan() {
		line := scanner.Bytes()
		lineEnd := offset + int64(len(line))
		if lineEnd < st.Size() {
			lineEnd++
		}
		_, _ = hash.Write(line)
		if lineEnd > offset+int64(len(line)) {
			_, _ = hash.Write([]byte{'\n'})
		}
		var raw map[string]json.RawMessage
		if json.Unmarshal(line, &raw) == nil && rawBool(raw, "isCompactSummary") {
			if len(result.Compactions) < MaxProvenanceCompactEvents {
				event := CompactProvenanceEvent{
					Offset:     offset,
					EndOffset:  lineEnd,
					LineSHA256: sha256Hex(line),
					Source:     compactSummarySource(raw),
				}
				if summary := rawString(raw, "summary"); summary != "" {
					event.SummarySHA256 = sha256Hex([]byte(summary))
				}
				result.Compactions = append(result.Compactions, event)
			}
		}
		offset = lineEnd
	}
	if err := scanner.Err(); err != nil {
		return claudeTranscriptProvenance{}, false
	}
	result.EndOffset = offset
	result.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return result, true
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func rawBool(fields map[string]json.RawMessage, key string) bool {
	var value bool
	return json.Unmarshal(fields[key], &value) == nil && value
}

func rawString(fields map[string]json.RawMessage, key string) string {
	var value string
	if json.Unmarshal(fields[key], &value) != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func compactSummarySource(fields map[string]json.RawMessage) string {
	for _, key := range []string{"summarySource", "summary_source", "source"} {
		if value := rawString(fields, key); value != "" {
			return value
		}
	}
	return "transcript"
}
