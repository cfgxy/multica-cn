//go:build linux || darwin

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

	"golang.org/x/sys/unix"
)

// repairTaskContextMarkerFile keeps the marker directory open while it reads
// and replaces the marker. Descriptor-relative operations prevent a .multica
// symlink replacement from redirecting the repair outside the task workdir.
func repairTaskContextMarkerFile(markerPath string, repair func([]byte) ([]byte, error)) error {
	dirFD, err := openNoFollowDirectory(filepath.Dir(markerPath))
	if err != nil {
		return fmt.Errorf("open task marker directory: %w", err)
	}
	defer unix.Close(dirFD) //nolint:errcheck -- descriptor close cannot affect the completed repair

	markerName := filepath.Base(markerPath)
	markerFD, err := unix.Openat(dirFD, markerName, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("open task context marker: %w", err)
	}
	markerFile := os.NewFile(uintptr(markerFD), markerPath)
	defer markerFile.Close() //nolint:errcheck -- read errors are handled below

	var original unix.Stat_t
	if err := unix.Fstat(markerFD, &original); err != nil {
		return fmt.Errorf("stat task context marker: %w", err)
	}
	if original.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("task context marker %s is not a regular file", markerPath)
	}
	if original.Size > maxTaskContextMarkerBytes {
		return fmt.Errorf("task context marker %s exceeds %d byte repair limit", markerPath, maxTaskContextMarkerBytes)
	}
	data, err := readTaskContextMarker(markerFile, markerPath)
	if err != nil {
		return err
	}
	repaired, err := repair(data)
	if err != nil {
		return err
	}

	tmp, tmpName, err := createTaskMarkerTempFile(dirFD)
	if err != nil {
		return err
	}
	var replacement unix.Stat_t
	if err := unix.Fstat(int(tmp.Fd()), &replacement); err != nil {
		tmp.Close()                      //nolint:errcheck -- no further use after a stat failure
		unix.Unlinkat(dirFD, tmpName, 0) //nolint:errcheck -- best-effort cleanup of a verified private entry
		return fmt.Errorf("stat repaired task marker: %w", err)
	}
	cleanupTmp := true
	defer func() {
		tmp.Close() //nolint:errcheck -- cleanup after a failed replace
		if cleanupTmp {
			var current unix.Stat_t
			if err := unix.Fstatat(dirFD, tmpName, &current, unix.AT_SYMLINK_NOFOLLOW); err == nil && sameTaskMarker(current, replacement) {
				unix.Unlinkat(dirFD, tmpName, 0) //nolint:errcheck -- cleanup after a failed replace
			}
		}
	}()
	if _, err := tmp.Write(repaired); err != nil {
		return fmt.Errorf("write temp task marker: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		return fmt.Errorf("chmod temp task marker: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp task marker: %w", err)
	}

	// Exchange preserves the existing entry at tmpName. An unexpected entry is
	// exchanged back or retained, never overwritten or deleted.
	if err := exchangeTaskContextMarker(dirFD, tmpName, markerName); err != nil {
		return fmt.Errorf("replace task context marker: %w", err)
	}
	var displaced unix.Stat_t
	if err := unix.Fstatat(dirFD, tmpName, &displaced, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		restored, restoreErr := restoreTaskContextMarker(dirFD, tmpName, markerName, replacement, fmt.Errorf("stat displaced task context marker: %w", err))
		cleanupTmp = restored
		return restoreErr
	}
	if !sameTaskMarker(displaced, original) {
		restored, restoreErr := restoreTaskContextMarker(dirFD, tmpName, markerName, replacement, fmt.Errorf("task context marker %s changed during repair; refusing to replace", markerPath))
		cleanupTmp = restored
		return restoreErr
	}
	currentData, err := readTaskContextMarker(markerFile, markerPath)
	if err != nil {
		restored, restoreErr := restoreTaskContextMarker(dirFD, tmpName, markerName, replacement, err)
		cleanupTmp = restored
		return restoreErr
	}
	if !bytes.Equal(data, currentData) {
		restored, restoreErr := restoreTaskContextMarker(dirFD, tmpName, markerName, replacement, fmt.Errorf("task context marker %s changed during repair; refusing to replace", markerPath))
		cleanupTmp = restored
		return restoreErr
	}
	var installed unix.Stat_t
	if err := unix.Fstatat(dirFD, markerName, &installed, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		cleanupTmp = false
		return fmt.Errorf("stat repaired task context marker: %w", err)
	}
	if !sameTaskMarker(installed, replacement) {
		cleanupTmp = false
		return fmt.Errorf("task context marker %s changed after repair exchange; refusing to report success", markerPath)
	}
	return nil
}

func readTaskContextMarker(f *os.File, markerPath string) ([]byte, error) {
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

func sameTaskMarker(a, b unix.Stat_t) bool {
	return a.Mode&unix.S_IFMT == unix.S_IFREG && b.Mode&unix.S_IFMT == unix.S_IFREG && a.Dev == b.Dev && a.Ino == b.Ino
}

func restoreTaskContextMarker(dirFD int, tmpName, markerName string, replacement unix.Stat_t, repairErr error) (bool, error) {
	var installed unix.Stat_t
	if err := unix.Fstatat(dirFD, markerName, &installed, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return false, fmt.Errorf("%w (stat repaired marker before restore: %v)", repairErr, err)
	}
	if !sameTaskMarker(installed, replacement) {
		return false, fmt.Errorf("%w (marker changed again; preserved both entries)", repairErr)
	}
	if err := exchangeTaskContextMarker(dirFD, tmpName, markerName); err != nil {
		return false, fmt.Errorf("%w (restore original marker: %v)", repairErr, err)
	}
	return true, repairErr
}

func createTaskMarkerTempFile(dirFD int) (*os.File, string, error) {
	for range 10 {
		entropy := make([]byte, 12)
		if _, err := rand.Read(entropy); err != nil {
			return nil, "", fmt.Errorf("generate task marker temp name: %w", err)
		}
		name := ".daemon_task_context-" + hex.EncodeToString(entropy) + ".json.tmp"
		fd, err := unix.Openat(dirFD, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o644)
		if err == nil {
			return os.NewFile(uintptr(fd), name), name, nil
		}
		if err != unix.EEXIST {
			return nil, "", fmt.Errorf("create temp task marker: %w", err)
		}
	}
	return nil, "", fmt.Errorf("create temp task marker: too many name collisions")
}

func openNoFollowDirectory(path string) (int, error) {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return -1, fmt.Errorf("task marker directory %s is not absolute", path)
	}
	flags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW
	fd, err := unix.Open(string(filepath.Separator), flags, 0)
	if err != nil {
		return -1, err
	}
	for _, component := range strings.Split(strings.TrimPrefix(clean, string(filepath.Separator)), string(filepath.Separator)) {
		if component == "" || component == "." {
			continue
		}
		next, err := unix.Openat(fd, component, flags, 0)
		unix.Close(fd) //nolint:errcheck -- the next descriptor is independent
		if err != nil {
			return -1, err
		}
		fd = next
	}
	return fd, nil
}
