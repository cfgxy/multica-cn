//go:build windows

package execenv

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var replaceFileW = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReplaceFileW")

// repairTaskContextMarkerFile holds handles to every marker-directory ancestor
// without FILE_SHARE_DELETE, rejects reparse points, and uses ReplaceFileW with
// a backup. The backup lets a detected last-moment replacement be restored
// instead of silently overwriting concurrent marker content.
func repairTaskContextMarkerFile(markerPath string, repair func([]byte) ([]byte, error)) error {
	dirHandles, err := openWindowsMarkerDirectories(markerPath)
	if err != nil {
		return err
	}
	defer closeWindowsHandles(dirHandles)

	markerFile, originalInfo, err := openWindowsTaskMarker(markerPath)
	if err != nil {
		return err
	}
	defer markerFile.Close() //nolint:errcheck -- the file is closed explicitly before replacement

	if originalInfo.FileSizeHigh != 0 || originalInfo.FileSizeLow > maxTaskContextMarkerBytes {
		return fmt.Errorf("task context marker %s exceeds %d byte repair limit", markerPath, maxTaskContextMarkerBytes)
	}
	data, err := readWindowsTaskContextMarker(markerFile, markerPath)
	if err != nil {
		return err
	}
	repaired, err := repair(data)
	if err != nil {
		return err
	}

	currentInfo, err := windowsTaskMarkerInfo(markerFile)
	if err != nil {
		return err
	}
	if !sameWindowsTaskMarker(originalInfo, currentInfo) {
		return fmt.Errorf("task context marker %s changed during repair; refusing to replace", markerPath)
	}
	currentData, err := readWindowsTaskContextMarker(markerFile, markerPath)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, currentData) {
		return fmt.Errorf("task context marker %s changed during repair; refusing to replace", markerPath)
	}

	tmp, err := os.CreateTemp(filepath.Dir(markerPath), ".daemon_task_context-*.json.tmp")
	if err != nil {
		return fmt.Errorf("create temp task marker: %w", err)
	}
	tmpPath := tmp.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			removeWindowsMarkerIfMatches(tmpPath, repaired)
		}
	}()
	if _, err := tmp.Write(repaired); err != nil {
		tmp.Close() //nolint:errcheck -- no further use after a write failure
		return fmt.Errorf("write temp task marker: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close() //nolint:errcheck -- no further use after a sync failure
		return fmt.Errorf("sync temp task marker: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close() //nolint:errcheck -- no further use after a chmod failure
		return fmt.Errorf("chmod temp task marker: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp task marker: %w", err)
	}
	tmpData, err := readWindowsTaskContextMarkerPath(tmpPath)
	if err != nil {
		return err
	}
	if !bytes.Equal(tmpData, repaired) {
		return fmt.Errorf("temp task context marker %s changed before replacement", tmpPath)
	}

	backupPath, err := nextWindowsTaskMarkerBackupPath(filepath.Dir(markerPath))
	if err != nil {
		return err
	}
	if err := markerFile.Close(); err != nil {
		return fmt.Errorf("close task context marker before replacement: %w", err)
	}
	if err := replaceWindowsTaskMarker(markerPath, tmpPath, backupPath); err != nil {
		return err
	}
	cleanupTmp = false

	backupData, backupErr := readWindowsTaskContextMarkerPath(backupPath)
	if backupErr != nil || !bytes.Equal(backupData, data) {
		return restoreWindowsTaskMarker(markerPath, backupPath, repaired, fmt.Errorf("task context marker %s changed during repair; refusing to replace", markerPath))
	}
	installedData, installedErr := readWindowsTaskContextMarkerPath(markerPath)
	if installedErr != nil || !bytes.Equal(installedData, repaired) {
		return restoreWindowsTaskMarker(markerPath, backupPath, repaired, fmt.Errorf("task context marker %s changed after replacement; refusing to report success", markerPath))
	}
	removeWindowsMarkerIfMatches(backupPath, data)
	return nil
}

func openWindowsMarkerDirectories(markerPath string) ([]windows.Handle, error) {
	dir, err := filepath.Abs(filepath.Clean(filepath.Dir(markerPath)))
	if err != nil {
		return nil, fmt.Errorf("resolve task marker directory: %w", err)
	}
	volume := filepath.VolumeName(dir)
	if volume == "" {
		return nil, fmt.Errorf("task marker directory %s has no volume", dir)
	}
	root := volume + string(filepath.Separator)
	relative, err := filepath.Rel(root, dir)
	if err != nil {
		return nil, fmt.Errorf("relativize task marker directory: %w", err)
	}
	current := root
	handles := make([]windows.Handle, 0, len(strings.Split(relative, string(filepath.Separator))))
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		path, err := windows.UTF16PtrFromString(current)
		if err != nil {
			closeWindowsHandles(handles)
			return nil, fmt.Errorf("encode task marker directory %s: %w", current, err)
		}
		handle, err := windows.CreateFile(path, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if err != nil {
			closeWindowsHandles(handles)
			return nil, fmt.Errorf("open task marker directory %s: %w", current, err)
		}
		var info windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
			windows.CloseHandle(handle) //nolint:errcheck -- no further use after stat failure
			closeWindowsHandles(handles)
			return nil, fmt.Errorf("stat task marker directory %s: %w", current, err)
		}
		if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
			windows.CloseHandle(handle) //nolint:errcheck -- reject the unsafe directory entry
			closeWindowsHandles(handles)
			return nil, fmt.Errorf("task marker directory %s is not a non-reparse directory", current)
		}
		handles = append(handles, handle)
	}
	return handles, nil
}

func openWindowsTaskMarker(markerPath string) (*os.File, windows.ByHandleFileInformation, error) {
	path, err := windows.UTF16PtrFromString(markerPath)
	if err != nil {
		return nil, windows.ByHandleFileInformation{}, fmt.Errorf("encode task context marker %s: %w", markerPath, err)
	}
	handle, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, windows.ByHandleFileInformation{}, fmt.Errorf("open task context marker %s: %w", markerPath, err)
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		windows.CloseHandle(handle) //nolint:errcheck -- no further use after stat failure
		return nil, windows.ByHandleFileInformation{}, fmt.Errorf("stat task context marker %s: %w", markerPath, err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		windows.CloseHandle(handle) //nolint:errcheck -- reject unsafe or non-file marker entry
		return nil, windows.ByHandleFileInformation{}, fmt.Errorf("task context marker %s is not a regular non-reparse file", markerPath)
	}
	return os.NewFile(uintptr(handle), markerPath), info, nil
}

func windowsTaskMarkerInfo(f *os.File) (windows.ByHandleFileInformation, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return info, fmt.Errorf("stat task context marker: %w", err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return info, fmt.Errorf("task context marker is not a regular non-reparse file")
	}
	return info, nil
}

func sameWindowsTaskMarker(a, b windows.ByHandleFileInformation) bool {
	return a.VolumeSerialNumber == b.VolumeSerialNumber && a.FileIndexHigh == b.FileIndexHigh && a.FileIndexLow == b.FileIndexLow
}

func readWindowsTaskContextMarker(f *os.File, markerPath string) ([]byte, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek task context marker %s: %w", markerPath, err)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxTaskContextMarkerBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read task context marker %s: %w", markerPath, err)
	}
	if len(data) > maxTaskContextMarkerBytes {
		return nil, fmt.Errorf("task context marker %s exceeds %d byte repair limit", markerPath, maxTaskContextMarkerBytes)
	}
	return data, nil
}

func readWindowsTaskContextMarkerPath(path string) ([]byte, error) {
	f, _, err := openWindowsTaskMarker(path)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck -- read errors are returned directly
	return readWindowsTaskContextMarker(f, path)
}

func nextWindowsTaskMarkerBackupPath(dir string) (string, error) {
	for range 10 {
		entropy := make([]byte, 12)
		if _, err := rand.Read(entropy); err != nil {
			return "", fmt.Errorf("generate task marker backup name: %w", err)
		}
		path := filepath.Join(dir, ".daemon_task_context-"+hex.EncodeToString(entropy)+".json.bak")
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			return path, nil
		} else if err != nil {
			return "", fmt.Errorf("stat task marker backup %s: %w", path, err)
		}
	}
	return "", fmt.Errorf("create task marker backup: too many name collisions")
}

func replaceWindowsTaskMarker(replacedPath, replacementPath, backupPath string) error {
	if err := replaceFileW.Find(); err != nil {
		return fmt.Errorf("resolve ReplaceFileW: %w", err)
	}
	replaced, err := windows.UTF16PtrFromString(replacedPath)
	if err != nil {
		return fmt.Errorf("encode replaced task marker: %w", err)
	}
	replacement, err := windows.UTF16PtrFromString(replacementPath)
	if err != nil {
		return fmt.Errorf("encode replacement task marker: %w", err)
	}
	var backup *uint16
	if backupPath != "" {
		backup, err = windows.UTF16PtrFromString(backupPath)
		if err != nil {
			return fmt.Errorf("encode task marker backup: %w", err)
		}
	}
	r1, _, callErr := replaceFileW.Call(uintptr(unsafe.Pointer(replaced)), uintptr(unsafe.Pointer(replacement)), uintptr(unsafe.Pointer(backup)), 0, 0, 0)
	if r1 == 0 {
		return fmt.Errorf("replace task context marker: %w", callErr)
	}
	return nil
}

func restoreWindowsTaskMarker(markerPath, backupPath string, expectedReplacement []byte, repairErr error) error {
	current, err := readWindowsTaskContextMarkerPath(markerPath)
	if err != nil {
		return fmt.Errorf("%w (read repaired marker before restore: %v)", repairErr, err)
	}
	if !bytes.Equal(current, expectedReplacement) {
		return fmt.Errorf("%w (marker changed again; preserved backup)", repairErr)
	}
	if err := replaceWindowsTaskMarker(markerPath, backupPath, ""); err != nil {
		return fmt.Errorf("%w (restore original marker: %v)", repairErr, err)
	}
	return repairErr
}

func removeWindowsMarkerIfMatches(path string, expected []byte) {
	data, err := readWindowsTaskContextMarkerPath(path)
	if err == nil && bytes.Equal(data, expected) {
		os.Remove(path) //nolint:errcheck -- best-effort cleanup of a verified private entry
	}
}

func closeWindowsHandles(handles []windows.Handle) {
	for i := len(handles) - 1; i >= 0; i-- {
		windows.CloseHandle(handles[i]) //nolint:errcheck -- cleanup cannot alter repair result
	}
}
