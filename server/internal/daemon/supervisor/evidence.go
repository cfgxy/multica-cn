package supervisor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type evidenceCursor struct {
	Out int64 `json:"o"`
	Err int64 `json:"e"`
}

// WorkerEvidence resolves task identity through this daemon's owned manifests;
// callers never provide a path or a supervisor run directory.
func (s *Supervisor) WorkerEvidence(ctx context.Context, req protocol.WorkerEvidenceRequest) protocol.WorkerEvidenceResponse {
	out := protocol.WorkerEvidenceResponse{ProcessLiveness: "unknown"}
	if s == nil || s.mgr == nil || req.RunID == "" || req.TailBytes < 1 || req.TailBytes > 64<<10 || (req.Stream != "stdout" && req.Stream != "stderr" && req.Stream != "both") {
		out.Unavailable = "evidence_unavailable"
		return out
	}
	ids, err := s.mgr.ListRunIDs()
	if err != nil {
		out.Unavailable = "evidence_unavailable"
		return out
	}
	var man *Manifest
	for _, id := range ids {
		candidate, e := s.mgr.ReadManifest(id)
		if e == nil && candidate.TaskID == req.RunID && candidate.DaemonID == s.daemonID {
			man = candidate
			break
		}
	}
	if man == nil {
		out.Unavailable = "daemon_unreachable"
		return out
	}
	cur := evidenceCursor{}
	if req.Cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(req.Cursor)
		if e != nil || json.Unmarshal(raw, &cur) != nil || cur.Out < 0 || cur.Err < 0 {
			out.Unavailable = "evidence_unavailable"
			return out
		}
	}
	read := func(path string, offset int64) (string, int64, time.Time, error) {
		dir := s.mgr.Dir(man.RunID)
		clean := filepath.Clean(path)
		rel, e := filepath.Rel(dir, clean)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return "", offset, time.Time{}, errors.New("invalid log")
		}
		f, e := os.Open(clean)
		if e != nil {
			return "", offset, time.Time{}, e
		}
		defer f.Close()
		info, e := f.Stat()
		if e != nil {
			return "", offset, time.Time{}, e
		}
		if req.Cursor == "" && info.Size() > int64(req.TailBytes) {
			offset = info.Size() - int64(req.TailBytes)
		}
		if offset > info.Size() {
			offset = info.Size()
		}
		if _, e = f.Seek(offset, io.SeekStart); e != nil {
			return "", offset, time.Time{}, e
		}
		b, e := io.ReadAll(io.LimitReader(f, int64(req.TailBytes)))
		if e != nil {
			return "", offset, time.Time{}, e
		}
		return string(b), offset + int64(len(b)), info.ModTime().UTC(), nil
	}
	var observed time.Time
	if req.Stream == "stdout" || req.Stream == "both" {
		out.StdoutTail, cur.Out, observed, err = read(stdoutLogPath(s.mgr.Dir(man.RunID)), cur.Out)
		if err != nil {
			out.Unavailable = "evidence_unavailable"
			return out
		}
	}
	if req.Stream == "stderr" || req.Stream == "both" {
		var at time.Time
		out.StderrTail, cur.Err, at, err = read(stderrLogPath(s.mgr.Dir(man.RunID)), cur.Err)
		if err != nil {
			out.Unavailable = "evidence_unavailable"
			return out
		}
		if at.After(observed) {
			observed = at
		}
	}
	if !observed.IsZero() {
		out.ObservedAt = &observed
	}
	encoded, _ := json.Marshal(cur)
	out.NextCursor = base64.RawURLEncoding.EncodeToString(encoded)
	if man.State == StateExited {
		out.ProcessLiveness = "dead"
	} else if man.State == StateRunning {
		if active, e := s.unitActive(ctx, man.Unit); e == nil && active {
			out.ProcessLiveness = "alive"
		} else if e == nil {
			out.ProcessLiveness = "dead"
		}
	}
	out.Available = true
	return out
}
