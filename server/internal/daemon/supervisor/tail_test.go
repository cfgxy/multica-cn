package supervisor

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testRunID() string { return "0123abcd-1234-5678-9abc-def012345678" }

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	mgr, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return mgr
}

func writeLog(t *testing.T, mgr *Manager, name string, content string) string {
	t.Helper()
	path := filepath.Join(mgr.Dir(testRunID()), name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func collectTail(t *testing.T, tail *resumableTail, wantErr error) string {
	t.Helper()
	var sb strings.Builder
	buf := make([]byte, 512)
	for {
		n, err := tail.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			if wantErr != nil && !errors.Is(err, wantErr) {
				t.Fatalf("tail Read err = %v, want %v", err, wantErr)
			}
			if wantErr == nil {
				t.Fatalf("tail Read unexpected err = %v", err)
			}
			return sb.String()
		}
	}
}

func TestTailStreamsAndHonorsExitEOF(t *testing.T) {
	mgr := newTestManager(t)
	id := testRunID()
	log := writeLog(t, mgr, "out.log", "alpha\nbeta\n")

	var exited *WorkerExit
	tail := newResumableTail(mgr, id, log, true, func() *WorkerExit { return exited })
	defer tail.Close()

	// Data available, no exit yet: stream what exists. Reading with no exit
	// and no new data would block, so feed exactly what is on disk.
	var sb strings.Builder
	buf := make([]byte, 512)
	for sb.Len() < len("alpha\nbeta\n") {
		n, err := tail.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			t.Fatalf("premature tail error: %v", err)
		}
	}
	if sb.String() != "alpha\nbeta\n" {
		t.Fatalf("streamed %q", sb.String())
	}

	// Exit proven and drained → EOF.
	exited = &WorkerExit{Code: 0}
	collectTail(t, tail, io.EOF)
}

func TestTailResumeDedupStraddledLines(t *testing.T) {
	mgr := newTestManager(t)
	id := testRunID()
	log := writeLog(t, mgr, "out.log", "line-1\nline-2\nline-3\n")

	// Crash model: line-1 was handed out, only its offset+hash persisted.
	handed := len("line-1\n")
	rs := readState{StdoutOffset: int64(handed), StdoutHashes: []uint64{hashLine([]byte("line-1"))}}
	if err := mgr.StoreReadState(id, rs); err != nil {
		t.Fatal(err)
	}

	exited := &WorkerExit{Code: 0}
	tail := newResumableTail(mgr, id, log, true, func() *WorkerExit { return exited })
	defer tail.Close()
	if got := collectTail(t, tail, io.EOF); got != "line-2\nline-3\n" {
		t.Fatalf("resume delivered %q, want line-2/3 without line-1", got)
	}
}

func TestTailExitFlushesPartialLine(t *testing.T) {
	mgr := newTestManager(t)
	id := testRunID()
	log := writeLog(t, mgr, "out.log", "complete\npartial-without-newline")
	exited := &WorkerExit{Code: 1}
	tail := newResumableTail(mgr, id, log, true, func() *WorkerExit { return exited })
	defer tail.Close()
	if got := collectTail(t, tail, io.EOF); got != "complete\npartial-without-newline" {
		t.Fatalf("got %q, want complete + flushed partial", got)
	}
}

func TestTailCloseUnblocksParkedRead(t *testing.T) {
	mgr := newTestManager(t)
	id := testRunID()
	log := filepath.Join(mgr.Dir(id), "out.log")
	tail := newResumableTail(mgr, id, log, true, func() *WorkerExit { return nil })

	done := make(chan error, 1)
	go func() {
		_, err := tail.Read(make([]byte, 64))
		done <- err
	}()
	time.Sleep(50 * time.Millisecond) // let it park in the poll loop
	if err := tail.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, errTailClosed) {
			t.Fatalf("parked Read err = %v, want errTailClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not unblock the parked Read")
	}
}

func TestSplitLinesHandlesCRLFAndPartial(t *testing.T) {
	lines, partial := splitLines([]byte("a\r\nb\nc"))
	if len(lines) != 2 || string(lines[0]) != "a" || string(lines[1]) != "b" {
		t.Fatalf("lines = %q", lines)
	}
	if string(partial) != "c" {
		t.Fatalf("partial = %q", partial)
	}
}

func TestMatchDedupPrefixFindsLongestSuffix(t *testing.T) {
	ring := []uint64{hashLine([]byte("x")), hashLine([]byte("a")), hashLine([]byte("b"))}
	lines := [][]byte{[]byte("a"), []byte("b"), []byte("c")}
	if got := matchDedupPrefix(lines, ring); got != 2 {
		t.Fatalf("match = %d, want 2 (a,b are the ring suffix)", got)
	}
	if got := matchDedupPrefix([][]byte{[]byte("zzz")}, ring); got != 0 {
		t.Fatalf("no-match case = %d", got)
	}
	if got := matchDedupPrefix([][]byte{[]byte("b")}, ring); got != 1 {
		t.Fatalf("single-suffix case = %d", got)
	}
}
