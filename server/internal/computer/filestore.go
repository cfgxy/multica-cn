package computer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// FileStore persists the attempt count and holds an exclusive flock for the
// whole WithKey call. Two processes on one bastion share the lock and the
// counter. This is not a lock across separate bastion hosts.
type FileStore struct {
	Dir string
}

type fileAttempt struct {
	n        int
	dir      string
	username string
}

// WithKey locks computer+username until fn returns, then writes the count.
func (s *FileStore) WithKey(computerID, username string, fn func(Attempt) error) error {
	if err := validateUsername(username); err != nil {
		return err
	}
	if err := validateComputerID(computerID); err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return err
	}
	if info, err := os.Lstat(s.Dir); err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("state directory must be private and not a symlink")
	}
	dir := filepath.Join(s.Dir, computerID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("invalid account state directory")
	}
	lockPath := filepath.Join(dir, username+".lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			return fmt.Errorf("account operation already running")
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	n, err := readCount(dir, username)
	if err != nil {
		return err
	}
	view := &fileAttempt{n: n, dir: dir, username: username}
	return fn(view)
}

func (a *fileAttempt) Reserve(limit int) (int, bool, error) {
	if a.n >= limit {
		return a.n, false, nil
	}
	a.n++
	if err := a.flush(); err != nil {
		return 0, false, err
	}
	return a.n, true, nil
}

func (a *fileAttempt) Refund() error {
	if a.n > 0 {
		a.n--
	}
	return a.flush()
}

func (a *fileAttempt) Reset() error {
	a.n = 0
	return a.flush()
}

func readCount(dir, username string) (int, error) {
	path := filepath.Join(dir, username+".attempts")
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return 0, fmt.Errorf("invalid attempt file")
		}
		if time.Since(info.ModTime()) > 15*time.Minute {
			return 0, nil
		}
	}
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		return 0, fmt.Errorf("attempt count is empty")
	}
	n, err := strconv.Atoi(text)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("attempt count is corrupt")
	}
	return n, nil
}

func (a *fileAttempt) flush() error {
	final := filepath.Join(a.dir, a.username+".attempts")
	tmp, err := os.CreateTemp(a.dir, a.username+".attempts.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(strconv.Itoa(a.n)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, final); err != nil {
		return err
	}
	d, err := os.Open(a.dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

var computerIDRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func validateComputerID(id string) error {
	if id == "." || id == ".." || !computerIDRE.MatchString(id) {
		return fmt.Errorf("computer id must match %s and must not be . or ..", computerIDRE.String())
	}
	return nil
}
