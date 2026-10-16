package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/fileaccess"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestExecuteFileOperation_ReadChunk(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(path, []byte("abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := executeFileOperation(protocol.DaemonFileOperationDispatch{
		TargetID: "fixture", Kind: string(fileaccess.KindRead), Path: path, Offset: 2, Length: 3, Roots: []string{root},
	})
	if err != nil {
		t.Fatal(err)
	}
	result := got.(fileaccess.ReadResult)
	if result.Data != "Y2Rl" || result.Length != 3 || result.EOF {
		t.Fatalf("read result = %+v", result)
	}
}

func TestExecuteFileOperation_RejectsPathOutsideTarget(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	path := filepath.Join(outside, "private.txt")
	if err := os.WriteFile(path, []byte("hidden"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := executeFileOperation(protocol.DaemonFileOperationDispatch{
		TargetID: "fixture", Kind: string(fileaccess.KindMetadata), Path: path, Roots: []string{root},
	})
	if err != fileaccess.ErrorPathNotAllowed {
		t.Fatalf("error = %v, want %q", err, fileaccess.ErrorPathNotAllowed)
	}
}

func TestExecuteFileOperation_RejectsChunkOverLimit(t *testing.T) {
	root := t.TempDir()
	_, err := executeFileOperation(protocol.DaemonFileOperationDispatch{
		TargetID: "fixture", Kind: string(fileaccess.KindRead), Path: filepath.Join(root, "file"), Length: fileaccess.MaxChunkBytes + 1, Roots: []string{root},
	})
	if err != fileaccess.ErrorRangeInvalid {
		t.Fatalf("error = %v, want %q", err, fileaccess.ErrorRangeInvalid)
	}
}

func TestExecuteFileOperation_RejectsChangedFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantSize := int64(6)
	_, err := executeFileOperation(protocol.DaemonFileOperationDispatch{
		TargetID: "fixture", Kind: string(fileaccess.KindRead), Path: path, Length: 3,
		ExpectedSize: &wantSize, Roots: []string{root},
	})
	if err != fileaccess.ErrorFileChanged {
		t.Fatalf("error = %v, want %q", err, fileaccess.ErrorFileChanged)
	}
	oldTime := time.Now().Add(-time.Hour)
	_, err = executeFileOperation(protocol.DaemonFileOperationDispatch{
		TargetID: "fixture", Kind: string(fileaccess.KindRead), Path: path, Length: 3,
		ExpectedModTime: oldTime.UTC().Format(time.RFC3339Nano), Roots: []string{root},
	})
	if err != fileaccess.ErrorFileChanged {
		t.Fatalf("mtime error = %v, want %q", err, fileaccess.ErrorFileChanged)
	}
}
