package daemon

import (
	"container/heap"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/multica-ai/multica/server/pkg/fileaccess"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (d *Daemon) handleFileOperation(ctx context.Context, rt Runtime, op protocol.DaemonFileOperationDispatch) {
	result, err := executeFileOperation(op)
	receipt := map[string]any{"lease_epoch": op.LeaseEpoch}
	if err != nil {
		receipt["status"] = "rejected"
		receipt["error"] = errorClass(err)
	} else {
		encoded, marshalErr := json.Marshal(result)
		if marshalErr != nil || len(encoded) > 96*1024 {
			receipt["status"] = "rejected"
			receipt["error"] = fileaccess.ErrorFileTooLarge.Error()
		} else {
			receipt["status"] = "succeeded"
			receipt["result"] = result
		}
	}
	if reportErr := d.client.ReportFileOperationResult(ctx, rt.ID, op.OperationID, receipt); reportErr != nil {
		d.logger.Warn("file operation receipt delivery failed", "runtime_id", rt.ID, "operation_id", op.OperationID, "error", reportErr)
	}
}

func executeFileOperation(op protocol.DaemonFileOperationDispatch) (any, error) {
	var expectedModTime *time.Time
	if op.ExpectedModTime != "" {
		parsed, err := time.Parse(time.RFC3339Nano, op.ExpectedModTime)
		if err != nil {
			return nil, fileaccess.ErrorRangeInvalid
		}
		expectedModTime = &parsed
	}
	req := fileaccess.Request{TargetID: op.TargetID, Kind: fileaccess.Kind(op.Kind), Path: op.Path, Offset: op.Offset, Length: op.Length, Limit: op.Limit, Cursor: op.Cursor, ExpectedSize: op.ExpectedSize, ExpectedModTime: expectedModTime}
	if err := fileaccess.ValidateRequest(req); err != nil {
		return nil, err
	}
	canonical, err := fileaccess.ResolveRootPath(op.Roots, op.Path)
	if err != nil {
		return nil, err
	}
	switch req.Kind {
	case fileaccess.KindList:
		dir, err := fileaccess.OpenDirectoryBeneath(op.Roots, canonical)
		if err != nil {
			return nil, err
		}
		defer dir.Close()
		cursor, err := fileaccess.DecodeCursor(op.Cursor)
		if err != nil {
			return nil, err
		}
		limit := normalizedListLimit(op.Limit)
		entries, hasMore, err := readDirectoryPage(dir, op.Roots, canonical, cursor, limit)
		if err != nil {
			return nil, fileaccess.ErrorPathNotAllowed
		}
		out := fileaccess.ListResult{TargetID: op.TargetID, Path: canonical, Entries: entries}
		if hasMore && len(entries) > 0 {
			out.Next = fileaccess.EncodeCursor(entries[len(entries)-1].Name)
		}
		return out, nil
	case fileaccess.KindMetadata:
		f, err := fileaccess.OpenBeneath(op.Roots, canonical, os.O_RDONLY)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return nil, fileaccess.ErrorNotFound
		}
		return fileaccess.MetadataFor(op.TargetID, canonical, info)
	case fileaccess.KindRead:
		f, err := fileaccess.OpenBeneath(op.Roots, canonical, os.O_RDONLY)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return nil, fileaccess.ErrorNotFound
		}
		meta, err := fileaccess.MetadataFor(op.TargetID, canonical, info)
		if err != nil {
			return nil, err
		}
		if (op.ExpectedSize != nil && *op.ExpectedSize != meta.Size) || (expectedModTime != nil && !expectedModTime.Equal(meta.ModTime)) {
			return nil, fileaccess.ErrorFileChanged
		}
		if op.Offset > meta.Size {
			return nil, fileaccess.ErrorRangeInvalid
		}
		buf := make([]byte, op.Length)
		n, readErr := f.ReadAt(buf, op.Offset)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, fileaccess.ErrorPathNotAllowed
		}
		buf = buf[:n]
		return fileaccess.ReadResult{TargetID: op.TargetID, Path: canonical, Offset: op.Offset, Length: n, EOF: op.Offset+int64(n) >= meta.Size, Encoding: "base64", Data: base64.StdEncoding.EncodeToString(buf)}, nil
	default:
		return nil, fileaccess.ErrorPermissionDenied
	}
}

const directoryReadBatchSize = 128

type directoryEntryHeap []fileaccess.Entry

func (h directoryEntryHeap) Len() int           { return len(h) }
func (h directoryEntryHeap) Less(i, j int) bool { return h[i].Name > h[j].Name }
func (h directoryEntryHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *directoryEntryHeap) Push(value any)    { *h = append(*h, value.(fileaccess.Entry)) }
func (h *directoryEntryHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

// readDirectoryPage scans the full iterator while retaining only limit+1 valid entries after the cursor.
func readDirectoryPage(dir *os.File, roots []string, path, cursor string, limit int) ([]fileaccess.Entry, bool, error) {
	candidates := make(directoryEntryHeap, 0, limit+1)
	heap.Init(&candidates)
	for {
		batch, err := dir.ReadDir(directoryReadBatchSize)
		for _, ent := range batch {
			name := ent.Name()
			if name <= cursor {
				continue
			}
			child := filepath.Join(path, name)
			if _, resolveErr := fileaccess.ResolveRootPath(roots, child); resolveErr != nil {
				continue
			}
			info, infoErr := ent.Info()
			if infoErr != nil {
				continue
			}
			entry := fileaccess.Entry{Name: name, Path: child, IsDir: info.IsDir(), Size: info.Size(), ModTime: info.ModTime().UTC()}
			if len(candidates) < limit+1 {
				heap.Push(&candidates, entry)
			} else if name < candidates[0].Name {
				heap.Pop(&candidates)
				heap.Push(&candidates, entry)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, false, err
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Name < candidates[j].Name })
	hasMore := len(candidates) > limit
	if hasMore {
		candidates = candidates[:limit]
	}
	return []fileaccess.Entry(candidates), hasMore, nil
}

func normalizedListLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	return min(limit, fileaccess.MaxListEntries)
}
func errorClass(err error) string {
	for _, class := range []error{fileaccess.ErrorHostNotRegistered, fileaccess.ErrorTargetDisabled, fileaccess.ErrorTargetUnreachable, fileaccess.ErrorPermissionDenied, fileaccess.ErrorPathNotAllowed, fileaccess.ErrorSensitivePathDenied, fileaccess.ErrorSymlinkEscapeDenied, fileaccess.ErrorNotFound, fileaccess.ErrorNotRegularFile, fileaccess.ErrorRangeInvalid, fileaccess.ErrorFileChanged, fileaccess.ErrorFileTooLarge, fileaccess.ErrorUnsupportedResolution} {
		if errors.Is(err, class) {
			return class.Error()
		}
	}
	return "internal_error"
}
