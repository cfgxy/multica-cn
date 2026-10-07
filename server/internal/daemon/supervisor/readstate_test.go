package supervisor

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// RUYI-529 regression: the two tail pumps persist concurrently. With
// per-stream cursor files each pump owns a single-writer file, so no
// load-merge-store interleaving can lose updates and no shared temp file can
// tear. On the pre-fix combined read.json this shape lost updates and tore
// the file within milliseconds — the corrupt file then killed a pump on its
// next load, which the daemon surfaced as "zcode-acp process exited" false
// failures.
func TestConcurrentStreamCursorStores(t *testing.T) {
	mgr := newTestManager(t)
	id := testRunID()
	const iters = 500
	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	for _, stream := range []string{"stdout", "stderr"} {
		stream := stream
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 1; i <= iters; i++ {
				rs, err := mgr.LoadStreamReadState(id, stream)
				if err != nil {
					errCh <- err
					return
				}
				rs.Offset = int64(i)
				if err := mgr.StoreStreamReadState(id, stream, rs); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent load/store: %v", err)
	}
	for _, stream := range []string{"stdout", "stderr"} {
		rs, err := mgr.LoadStreamReadState(id, stream)
		if err != nil {
			t.Fatalf("final load %s: %v", stream, err)
		}
		if rs.Offset != iters {
			t.Fatalf("%s cursor = %d, want %d (lost update)", stream, rs.Offset, iters)
		}
	}
}

// Runs cursored by the pre-upgrade combined file keep resuming: the first
// per-stream load seeds from legacy read.json, and the per-stream file
// becomes authoritative once written.
func TestStreamReadStateLegacyFallback(t *testing.T) {
	mgr := newTestManager(t)
	id := testRunID()
	legacy := []byte(`{"stdout_offset": 100, "stdout_hashes": [7], "stderr_offset": 55}`)
	if err := os.MkdirAll(mgr.Dir(id), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mgr.Dir(id), "read.json"), legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	so, err := mgr.LoadStreamReadState(id, "stdout")
	if err != nil {
		t.Fatal(err)
	}
	if so.Offset != 100 || len(so.Hashes) != 1 || so.Hashes[0] != 7 {
		t.Fatalf("stdout legacy seed = %+v", so)
	}
	se, err := mgr.LoadStreamReadState(id, "stderr")
	if err != nil {
		t.Fatal(err)
	}
	if se.Offset != 55 {
		t.Fatalf("stderr legacy seed = %+v", se)
	}
	// Once this stream persists, its own file wins.
	if err := mgr.StoreStreamReadState(id, "stdout", streamReadState{Offset: 200}); err != nil {
		t.Fatal(err)
	}
	got, err := mgr.LoadStreamReadState(id, "stdout")
	if err != nil {
		t.Fatal(err)
	}
	if got.Offset != 200 {
		t.Fatalf("per-stream file should win: %+v", got)
	}
}

// A corrupt legacy cursor must not kill the pump: the stream degrades to a
// zero cursor and keeps tailing.
func TestStreamReadStateCorruptLegacyDegrades(t *testing.T) {
	mgr := newTestManager(t)
	id := testRunID()
	if err := os.MkdirAll(mgr.Dir(id), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mgr.Dir(id), "read.json"), []byte("{torn"), 0o600); err != nil {
		t.Fatal(err)
	}
	rs, err := mgr.LoadStreamReadState(id, "stdout")
	if err != nil {
		t.Fatalf("corrupt legacy file must degrade to zero cursor: %v", err)
	}
	if rs.Offset != 0 {
		t.Fatalf("degraded cursor not zero: %+v", rs)
	}
}
