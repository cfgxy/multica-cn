package supervisor

import (
	"bytes"
	"errors"
	"io"
	"os"
	"sync"
	"time"
)

// errTailClosed is returned once Close has been called.
var errTailClosed = errors.New("supervisor: tail closed")

// resumableTail streams a run's append-only log from the persisted read
// cursor. Delivery ordering gives exactly the dedup semantics RUYI-349 asks
// for: data is handed to the consumer FIRST and the cursor is persisted
// AFTER, so a crash in between re-delivers the straddled lines — and the
// persisted line-hash ring lets the next open of the same run skip exactly
// those. EOF comes only when the worker's exit is proven (manifest exit
// record) and the log is fully drained.
type resumableTail struct {
	mgr      *Manager
	runID    string
	path     string // manifest stdout_log / stderr_log
	isStdout bool
	exit     func() *WorkerExit // proven exit; non-nil result ends the stream once drained
	poll     time.Duration

	mu       sync.Mutex
	stopOnce sync.Once
	stop     chan struct{} // closed without mu — a parked Read HOLDS mu while polling
	pending  bytes.Buffer  // decoded-but-not-yet-handed-out data
	staged   readState     // cursor+ring to persist once pending is handed out
	stagedOK bool
}

func newResumableTail(mgr *Manager, runID, path string, isStdout bool, exit func() *WorkerExit) *resumableTail {
	return &resumableTail{mgr: mgr, runID: runID, path: path, isStdout: isStdout, exit: exit, poll: 50 * time.Millisecond, stop: make(chan struct{})}
}

// Close stops the poll loop; a blocked Read unblocks within one poll interval.
// It must not take mu: the parked reader holds it for the whole poll.
func (t *resumableTail) Close() error {
	t.stopOnce.Do(func() { close(t.stop) })
	return nil
}

func (t *resumableTail) stopped() bool {
	select {
	case <-t.stop:
		return true
	default:
		return false
	}
}

func (t *resumableTail) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for {
		if t.stopped() {
			return 0, errTailClosed
		}
		if t.pending.Len() == 0 {
			if t.stagedOK && t.stagedEOF() {
				// Everything staged is handed out and the exit is proven.
				return 0, io.EOF
			}
			if err := t.fill(); err != nil {
				return 0, err
			}
			continue
		}
		n, _ := t.pending.Read(p)
		if t.pending.Len() == 0 && t.stagedOK {
			// Hand-out completed: persist the cursor. A crash before this
			// point re-delivers the same lines, and the persisted hash ring
			// skips them on the next open — the dedup contract.
			if err := t.mgr.StoreReadState(t.runID, t.staged); err != nil {
				return n, err
			}
			t.stagedOK = false
		}
		return n, nil
	}
}

func (t *resumableTail) stagedEOF() bool {
	return t.exitProven() && t.pending.Len() == 0
}

func (t *resumableTail) exitProven() bool {
	return t.exit != nil && t.exit() != nil
}

// fill appends newly available data to pending, staging the cursor/ring that
// will be persisted once it is handed out. It returns only when pending holds
// data or the stream is over (proven EOF / closed / hard error).
func (t *resumableTail) fill() error {
	rs, err := t.mgr.LoadReadState(t.runID)
	if err != nil {
		return err
	}
	offset := rs.StdoutOffset
	ring := append([]uint64(nil), rs.StdoutHashes...)
	if !t.isStdout {
		offset = rs.StderrOffset
		ring = append([]uint64(nil), rs.StderrHashes...)
	}

	for {
		if t.stopped() {
			return errTailClosed
		}
		data, err := readFrom(t.path, offset)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}

		var lines [][]byte
		var partial []byte
		if len(data) > 0 {
			lines, partial = splitLines(data)
			// Dedup: a crash between hand-out and cursor persist leaves the
			// straddled lines as a suffix of the persisted hash ring.
			if skip := matchDedupPrefix(lines, ring); skip > 0 {
				lines = lines[skip:]
			}
			if drop := len(lines) - maxDedupRing; drop > 0 {
				lines = lines[drop:]
			}
			for _, line := range lines {
				ring = append(ring, hashLine(line))
				t.pending.Write(line)
				t.pending.WriteByte('\n')
			}
			complete := int64(len(data) - len(partial))
			offset += complete
			if len(partial) > 0 && t.exitProven() {
				// Exited mid-line: the fragment is final, deliver it as-is.
				t.pending.Write(partial)
				offset += int64(len(partial))
				partial = nil
			}
		}

		if t.pending.Len() > 0 {
			t.staged = t.stagedState(rs, offset, ring)
			t.stagedOK = true
			return nil
		}
		if len(partial) == 0 && t.exitProven() {
			// Drained and proven over.
			t.staged = t.stagedState(rs, offset, ring)
			t.stagedOK = true
			return nil
		}
		select {
		case <-t.stop:
			return errTailClosed
		case <-time.After(t.poll):
		}
	}
}

// stagedState merges this stream's advanced cursor with the OTHER stream's
// cursor as of this fill's load. Persisting the whole readState per hand-out
// means the two streams share one file: writing this stream's offset over
// both fields would rewind (or skip) the sibling stream on its next open.
func (t *resumableTail) stagedState(rs readState, offset int64, ring []uint64) readState {
	if t.isStdout {
		return readState{StdoutOffset: offset, StdoutHashes: ring, StderrOffset: rs.StderrOffset, StderrHashes: rs.StderrHashes}
	}
	return readState{StdoutOffset: rs.StdoutOffset, StdoutHashes: rs.StdoutHashes, StderrOffset: offset, StderrHashes: ring}
}

// readFrom reads the file from offset. nil data with nil error means "nothing
// new".
func readFrom(path string, offset int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() <= offset {
		return nil, nil
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}

// splitLines splits complete newline-terminated lines (each WITHOUT its
// newline) and returns the trailing partial fragment separately.
func splitLines(data []byte) (lines [][]byte, partial []byte) {
	for len(data) > 0 {
		idx := bytes.IndexByte(data, '\n')
		if idx < 0 {
			return lines, data
		}
		line := data[:idx]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		lines = append(lines, append([]byte(nil), line...))
		data = data[idx+1:]
	}
	return lines, nil
}

// matchDedupPrefix returns how many of lines form the tail of ring (in
// order) — those were handed out before the crash and must be skipped.
func matchDedupPrefix(lines [][]byte, ring []uint64) int {
	if len(ring) == 0 || len(lines) == 0 {
		return 0
	}
	maxK := len(lines)
	if maxK > len(ring) {
		maxK = len(ring)
	}
	for k := maxK; k > 0; k-- {
		match := true
		for i := 0; i < k; i++ {
			if hashLine(lines[i]) != ring[len(ring)-k+i] {
				match = false
				break
			}
		}
		if match {
			return k
		}
	}
	return 0
}

// ResumableReader is the exported handle the supervisor hands the session.
type ResumableReader struct {
	t *resumableTail
}

// Read implements io.Reader.
func (r *ResumableReader) Read(p []byte) (int, error) { return r.t.Read(p) }

// Close implements io.Closer; it stops the poll loop and unblocks a parked
// Read within one poll interval.
func (r *ResumableReader) Close() error { return r.t.Close() }

// compile-time guard: the reader must satisfy the interface the session pumps.
var _ io.ReadCloser = (*ResumableReader)(nil)
