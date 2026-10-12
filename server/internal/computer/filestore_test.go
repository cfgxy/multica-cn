package computer

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileStorePersistsAndBlocks(t *testing.T) {
	dir := privateTempDir(t)
	a := &FileStore{Dir: dir}
	b := &FileStore{Dir: dir}
	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = a.WithKey("gpu-pc", "zhang", func(Attempt) error {
			close(held)
			<-release
			return nil
		})
		close(done)
	}()
	select {
	case <-held:
	case <-time.After(time.Second):
		t.Fatal("lock was not taken")
	}
	blocked := make(chan struct{})
	go func() {
		_ = b.WithKey("gpu-pc", "zhang", func(view Attempt) error {
			_, _, err := view.Reserve(5)
			return err
		})
		close(blocked)
	}()
	select {
	case <-blocked:
		t.Fatal("second process passed the file lock")
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("first lock did not release")
	}
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("second store did not enter after unlock")
	}
	err := b.WithKey("gpu-pc", "zhang", func(view Attempt) error {
		n, allowed, err := view.Reserve(5)
		if err != nil || !allowed || n != 2 {
			t.Fatalf("persisted count n=%d allowed=%v err=%v", n, allowed, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestInfraErrorsDoNotPersistFailures(t *testing.T) {
	cases := []struct {
		name   string
		remote *fakeRemote
	}{
		{"create", &fakeRemote{createErr: errString("useradd failed")}},
		{"write", &fakeRemote{exists: true, password: "secret", writeErr: errString("disk full")}},
		{"install", &fakeRemote{exists: true, password: "secret", installErr: errString("systemctl failed")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := privateTempDir(t)
			store := &FileStore{Dir: dir}
			if _, err := Apply(tc.remote, store, req("secret")); err == nil {
				t.Fatal("expected error")
			}
			body, err := os.ReadFile(filepath.Join(dir, "gpu-pc", "zhang.attempts"))
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != "0" {
				t.Fatalf("count = %q, want 0", body)
			}
		})
	}
}

func TestComputerIDRejectsDotSegments(t *testing.T) {
	dir := privateTempDir(t)
	store := &FileStore{Dir: dir}
	for _, id := range []string{".", ".."} {
		if err := store.WithKey(id, "zhang", func(Attempt) error { return nil }); err == nil {
			t.Fatalf("accepted %q", id)
		}
	}
	parent := filepath.Dir(dir)
	matches, _ := filepath.Glob(filepath.Join(parent, "zhang.*"))
	if len(matches) != 0 {
		t.Fatalf("count escaped the store dir: %v", matches)
	}
}

func TestCorruptCountFailsClosed(t *testing.T) {
	dir := privateTempDir(t)
	if err := os.MkdirAll(filepath.Join(dir, "gpu-pc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gpu-pc", "zhang.attempts"), []byte("-1"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &FileStore{Dir: dir}
	err := store.WithKey("gpu-pc", "zhang", func(view Attempt) error {
		_, allowed, err := view.Reserve(5)
		if err == nil && allowed {
			t.Fatal("corrupt count was treated as usable")
		}
		return err
	})
	if err == nil {
		t.Fatal("expected corrupt count error")
	}
}

func TestComputerIDsDoNotCollapse(t *testing.T) {
	dir := privateTempDir(t)
	store := &FileStore{Dir: dir}
	err := store.WithKey("gpu/pc", "zhang", func(Attempt) error { return nil })
	if err == nil {
		t.Fatal("slash computer id was accepted")
	}
	if err := store.WithKey("gpupc", "zhang", func(view Attempt) error {
		_, _, err := view.Reserve(5)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "gpu/pc")); !os.IsNotExist(err) {
		t.Fatalf("collapsed directory exists: %v", err)
	}
}

func privateTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}
