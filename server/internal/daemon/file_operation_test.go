package daemon

import (
	"fmt"
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

func TestExecuteFileOperation_ListPagesAllEntriesInNameOrder(t *testing.T) {
	root := t.TempDir()
	const entryCount = 450
	for i := entryCount - 1; i >= 0; i-- {
		name := fmt.Sprintf("entry-%04d.txt", i)
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	seen := make(map[string]bool, entryCount)
	cursor := ""
	pageCount := 0
	for {
		got, err := executeFileOperation(protocol.DaemonFileOperationDispatch{
			TargetID: "fixture", Kind: string(fileaccess.KindList), Path: root, Limit: 73, Cursor: cursor, Roots: []string{root},
		})
		if err != nil {
			t.Fatal(err)
		}
		page := got.(fileaccess.ListResult)
		pageCount++
		for i, entry := range page.Entries {
			wantName := fmt.Sprintf("entry-%04d.txt", len(seen))
			if entry.Name != wantName {
				t.Fatalf("page %d entry %d = %q, want %q", pageCount, i, entry.Name, wantName)
			}
			if seen[entry.Name] {
				t.Fatalf("entry %q appeared more than once", entry.Name)
			}
			seen[entry.Name] = true
		}
		if page.Next == "" {
			if pageCount < 2 {
				t.Fatalf("expected multiple pages, got %d", pageCount)
			}
			break
		}
		cursor = page.Next
	}
	if len(seen) != entryCount {
		t.Fatalf("listed %d entries, want %d", len(seen), entryCount)
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
