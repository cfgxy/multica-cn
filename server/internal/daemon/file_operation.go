package daemon

import (
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
		entries, err := dir.ReadDir(fileaccess.MaxListEntries + 1)
		if err != nil {
			return nil, fileaccess.ErrorPathNotAllowed
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		cursor, err := fileaccess.DecodeCursor(op.Cursor)
		if err != nil {
			return nil, err
		}
		out := fileaccess.ListResult{TargetID: op.TargetID, Path: canonical, Entries: []fileaccess.Entry{}}
		for _, ent := range entries {
			if ent.Name() <= cursor {
				continue
			}
			if len(out.Entries) >= normalizedListLimit(op.Limit) {
				out.Next = fileaccess.EncodeCursor(out.Entries[len(out.Entries)-1].Name)
				break
			}
			info, err := ent.Info()
			if err != nil {
				continue
			}
			child := filepath.Join(canonical, ent.Name())
			if _, err := fileaccess.ResolveRootPath(op.Roots, child); err != nil {
				continue
			}
			out.Entries = append(out.Entries, fileaccess.Entry{Name: ent.Name(), Path: child, IsDir: info.IsDir(), Size: info.Size(), ModTime: info.ModTime().UTC()})
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
