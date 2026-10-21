package daemon

import (
	"container/heap"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/fileaccess"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (d *Daemon) handleFileOperation(ctx context.Context, rt Runtime, op protocol.DaemonFileOperationDispatch) {
	result, err := executeFileOperation(ctx, op)
	receipt := map[string]any{"lease_epoch": op.LeaseEpoch, "policy_version": op.PolicyVersion}
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

func executeFileOperation(ctx context.Context, op protocol.DaemonFileOperationDispatch) (any, error) {
	var policy fileaccess.DenyPolicy
	if len(op.Policy) == 0 || json.Unmarshal(op.Policy, &policy) != nil || policy.Version != op.PolicyVersion || fileaccess.ValidateDenyPolicy(policy) != nil {
		return nil, fileaccess.ErrorPermissionDenied
	}
	var expectedModTime *time.Time
	if op.ExpectedModTime != "" {
		parsed, err := time.Parse(time.RFC3339Nano, op.ExpectedModTime)
		if err != nil {
			return nil, fileaccess.ErrorRangeInvalid
		}
		expectedModTime = &parsed
	}
	req := fileaccess.Request{TargetID: op.TargetID, Kind: fileaccess.Kind(op.Kind), Path: op.Path, Offset: op.Offset, Length: op.Length, Limit: op.Limit, Cursor: op.Cursor, ExpectedSize: op.ExpectedSize, ExpectedModTime: expectedModTime}
	if err := fileaccess.ValidateRequestWithPolicy(req, policy); err != nil {
		return nil, err
	}
	if alias, ok := fileaccess.SSHTargetAlias(op.TargetID); ok {
		requested, err := fileaccess.NormalizePathWithPolicy(op.Path, policy)
		if err != nil {
			return nil, err
		}
		if !fileaccess.PathWithinRootsWithPolicy(op.Roots, requested, policy) {
			return nil, fileaccess.ErrorPathNotAllowed
		}
		return executeSSHFileOperation(ctx, alias, op, req, requested, policy)
	}
	canonical, err := fileaccess.ResolveRootPathWithPolicy(op.Roots, op.Path, policy)
	if err != nil {
		return nil, err
	}
	switch req.Kind {
	case fileaccess.KindList:
		dir, err := fileaccess.OpenDirectoryBeneathWithPolicy(op.Roots, canonical, policy)
		if err != nil {
			return nil, err
		}
		defer dir.Close()
		cursor, err := fileaccess.DecodeCursor(op.Cursor)
		if err != nil {
			return nil, err
		}
		limit := normalizedListLimit(op.Limit)
		entries, hasMore, err := readDirectoryPage(dir, op.Roots, canonical, cursor, limit, policy)
		if err != nil {
			return nil, fileaccess.ErrorPathNotAllowed
		}
		out := fileaccess.ListResult{TargetID: op.TargetID, Path: canonical, Entries: entries}
		if hasMore && len(entries) > 0 {
			out.Next = fileaccess.EncodeCursor(entries[len(entries)-1].Name)
		}
		return out, nil
	case fileaccess.KindMetadata:
		f, err := fileaccess.OpenBeneathWithPolicy(op.Roots, canonical, os.O_RDONLY, policy)
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
		f, err := fileaccess.OpenBeneathWithPolicy(op.Roots, canonical, os.O_RDONLY, policy)
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

func executeSSHFileOperation(ctx context.Context, alias string, op protocol.DaemonFileOperationDispatch, req fileaccess.Request, requested string, policy fileaccess.DenyPolicy) (any, error) {
	if _, ok := fileaccess.SSHTargetAlias("ssh:" + alias); !ok {
		return nil, fileaccess.ErrorPermissionDenied
	}
	if !sshAliasConfigured(alias) {
		return nil, fileaccess.ErrorPermissionDenied
	}
	operationCtx, cancel := context.WithTimeout(ctx, sshFileOperationTimeout)
	defer cancel()
	session, err := openSSHFileSession(operationCtx, alias)
	if err != nil {
		return nil, err
	}
	defer session.Close()

	roots, err := resolveSSHRoots(session, op.Roots)
	if err != nil {
		return nil, err
	}
	resolved, err := session.Realpath(requested)
	if err != nil {
		return nil, err
	}
	if policy.Denies(resolved) {
		return nil, fileaccess.ErrorSensitivePathDenied
	}
	if !fileaccess.PathWithinRootsWithPolicy(roots, resolved, policy) {
		return nil, fileaccess.ErrorSymlinkEscapeDenied
	}

	switch req.Kind {
	case fileaccess.KindList:
		cursor, err := fileaccess.DecodeCursor(op.Cursor)
		if err != nil {
			return nil, err
		}
		entries, hasMore, err := session.List(resolved, roots, cursor, normalizedListLimit(op.Limit), policy)
		if err != nil {
			return nil, err
		}
		out := fileaccess.ListResult{TargetID: op.TargetID, Path: resolved, Entries: entries}
		if hasMore && len(entries) > 0 {
			out.Next = fileaccess.EncodeCursor(entries[len(entries)-1].Name)
		}
		return out, nil
	case fileaccess.KindMetadata:
		attrs, err := session.Stat(resolved)
		if err != nil {
			return nil, err
		}
		return sshMetadataFor(op.TargetID, resolved, attrs)
	case fileaccess.KindRead:
		attrs, err := session.Stat(resolved)
		if err != nil {
			return nil, err
		}
		meta, err := sshMetadataFor(op.TargetID, resolved, attrs)
		if err != nil {
			return nil, err
		}
		if (op.ExpectedSize != nil && *op.ExpectedSize != meta.Size) || (req.ExpectedModTime != nil && !req.ExpectedModTime.Equal(meta.ModTime)) {
			return nil, fileaccess.ErrorFileChanged
		}
		if op.Offset > meta.Size {
			return nil, fileaccess.ErrorRangeInvalid
		}
		data, eof, err := session.Read(resolved, uint64(op.Offset), uint32(op.Length))
		if err != nil {
			return nil, err
		}
		return fileaccess.ReadResult{TargetID: op.TargetID, Path: resolved, Offset: op.Offset, Length: len(data), EOF: eof || op.Offset+int64(len(data)) >= meta.Size, Encoding: "base64", Data: base64.StdEncoding.EncodeToString(data)}, nil
	default:
		return nil, fileaccess.ErrorPermissionDenied
	}
}

func resolveSSHRoots(session sshFileSession, roots []string) ([]string, error) {
	resolved := make([]string, 0, len(roots))
	for _, root := range roots {
		path, err := session.Realpath(root)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, path)
	}
	return resolved, nil
}

type sshFileSession interface {
	Close() error
	Realpath(string) (string, error)
	Stat(string) (sshFileAttributes, error)
	Read(string, uint64, uint32) ([]byte, bool, error)
	List(string, []string, string, int, fileaccess.DenyPolicy) ([]fileaccess.Entry, bool, error)
}

var openSSHFileSession func(context.Context, string) (sshFileSession, error) = func(ctx context.Context, alias string) (sshFileSession, error) {
	return newSFTPFileSession(ctx, alias)
}

var sshAliasConfigured = daemonSSHConfigDeclaresAlias

// daemonSSHConfigDeclaresAlias only authorizes literal Host entries. OpenSSH
// otherwise treats an arbitrary token as a hostname, so shape validation or
// ssh -G alone would allow an unregistered remote connection.
func daemonSSHConfigDeclaresAlias(alias string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	return sshConfigDeclaresAlias(alias, []string{
		filepath.Join(home, ".ssh", "config"),
		"/etc/ssh/ssh_config",
	})
}

func sshConfigDeclaresAlias(alias string, configFiles []string) bool {
	seen := make(map[string]struct{})
	var visit func(string, int) bool
	visit = func(configFile string, depth int) bool {
		if depth > 8 || len(seen) >= 64 {
			return false
		}
		resolved, err := filepath.Abs(configFile)
		if err != nil {
			return false
		}
		if _, ok := seen[resolved]; ok {
			return false
		}
		seen[resolved] = struct{}{}
		content, err := os.ReadFile(resolved)
		if err != nil {
			return false
		}
		for _, line := range strings.Split(string(content), "\n") {
			key, values := sshConfigDirective(line)
			switch key {
			case "host":
				for _, value := range values {
					if value == alias {
						return true
					}
				}
			case "include":
				for _, pattern := range values {
					if strings.HasPrefix(pattern, "~/") {
						home, err := os.UserHomeDir()
						if err != nil {
							continue
						}
						pattern = filepath.Join(home, strings.TrimPrefix(pattern, "~/"))
					} else if !filepath.IsAbs(pattern) {
						pattern = filepath.Join(filepath.Dir(resolved), pattern)
					}
					matches, _ := filepath.Glob(pattern)
					for _, match := range matches {
						if visit(match, depth+1) {
							return true
						}
					}
				}
			}
		}
		return false
	}
	for _, configFile := range configFiles {
		if visit(configFile, 0) {
			return true
		}
	}
	return false
}

func sshConfigDirective(line string) (string, []string) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", nil
	}
	line = strings.Replace(line, "=", " ", 1)
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return "", nil
	}
	key := strings.ToLower(parts[0])
	if key != "host" && key != "include" {
		return "", nil
	}
	values := make([]string, 0, len(parts)-1)
	for _, value := range parts[1:] {
		if strings.HasPrefix(value, "#") {
			break
		}
		value = strings.Trim(value, "\"'")
		if value != "" && !strings.HasPrefix(value, "!") {
			values = append(values, value)
		}
	}
	return key, values
}

type sshFileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
}

func (info sshFileInfo) Name() string       { return info.name }
func (info sshFileInfo) Size() int64        { return info.size }
func (info sshFileInfo) Mode() os.FileMode  { return info.mode }
func (info sshFileInfo) ModTime() time.Time { return info.modTime }
func (info sshFileInfo) IsDir() bool        { return info.mode.IsDir() }
func (info sshFileInfo) Sys() any           { return nil }

func sshMetadataFor(targetID, path string, attrs sshFileAttributes) (fileaccess.Metadata, error) {
	if attrs.size > uint64(fileaccess.MaxFileBytes) {
		return fileaccess.Metadata{}, fileaccess.ErrorFileTooLarge
	}
	return fileaccess.MetadataFor(targetID, path, sshFileInfo{
		name: filepath.Base(path), size: int64(attrs.size), mode: attrs.mode, modTime: attrs.modTime,
	})
}

const (
	sshFileOperationTimeout = 15 * time.Second

	sshFXPInit     = 1
	sshFXPVersion  = 2
	sshFXPOpen     = 3
	sshFXPClose    = 4
	sshFXPRead     = 5
	sshFXPStat     = 17
	sshFXPOpendir  = 11
	sshFXPReaddir  = 12
	sshFXPRealpath = 16
	sshFXPStatus   = 101
	sshFXPHandle   = 102
	sshFXPData     = 103
	sshFXPName     = 104
	sshFXPAttrs    = 105

	sshFXOK         = 0
	sshFXEOF        = 1
	sshFXNoSuchFile = 2
	sshFXPermission = 3

	sshFXFSize        = 0x00000001
	sshFXFPermissions = 0x00000004
	sshFXFACModTime   = 0x00000008
	sshFXFOpenRead    = 0x00000001
	maxSFTPPacket     = 1024 * 1024
)

type sshFileAttributes struct {
	size    uint64
	mode    os.FileMode
	modTime time.Time
}

type sftpFileSession struct {
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    io.ReadCloser
	nextID uint32
}

func newSFTPFileSession(ctx context.Context, alias string) (*sftpFileSession, error) {
	cmd := exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "-s", alias, "sftp")
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, fileaccess.ErrorTargetUnreachable
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fileaccess.ErrorTargetUnreachable
	}
	if err := cmd.Start(); err != nil {
		return nil, fileaccess.ErrorTargetUnreachable
	}
	session := &sftpFileSession{cmd: cmd, in: in, out: out, nextID: 1}
	if err := session.writePacket(sshFXPInit, uint32Bytes(3)); err != nil {
		_ = session.Close()
		return nil, fileaccess.ErrorTargetUnreachable
	}
	kind, payload, err := session.readPacket()
	if err != nil || kind != sshFXPVersion || len(payload) < 4 || binary.BigEndian.Uint32(payload[:4]) < 3 {
		_ = session.Close()
		return nil, fileaccess.ErrorTargetUnreachable
	}
	return session, nil
}

func (s *sftpFileSession) Close() error {
	if s.in != nil {
		_ = s.in.Close()
	}
	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}
	return s.cmd.Wait()
}

func (s *sftpFileSession) Realpath(path string) (string, error) {
	kind, payload, err := s.request(sshFXPRealpath, stringBytes(path))
	if err != nil {
		return "", err
	}
	if kind == sshFXPStatus {
		return "", parseSFTPStatus(payload)
	}
	if kind != sshFXPName {
		return "", fileaccess.ErrorTargetUnreachable
	}
	_, names, err := parseSFTPNames(payload)
	if err != nil || len(names) == 0 {
		return "", fileaccess.ErrorTargetUnreachable
	}
	return fileaccess.NormalizePathUnfiltered(names[0].name)
}

func (s *sftpFileSession) Stat(path string) (sshFileAttributes, error) {
	kind, payload, err := s.request(sshFXPStat, stringBytes(path))
	if err != nil {
		return sshFileAttributes{}, err
	}
	if kind == sshFXPStatus {
		return sshFileAttributes{}, parseSFTPStatus(payload)
	}
	if kind != sshFXPAttrs {
		return sshFileAttributes{}, fileaccess.ErrorTargetUnreachable
	}
	attrs, _, err := parseSFTPAttributes(payload)
	return attrs, err
}

func (s *sftpFileSession) Read(path string, offset uint64, length uint32) ([]byte, bool, error) {
	body := append(stringBytes(path), uint32Bytes(sshFXFOpenRead)...)
	body = append(body, uint32Bytes(0)...)
	kind, payload, err := s.request(sshFXPOpen, body)
	if err != nil {
		return nil, false, err
	}
	if kind == sshFXPStatus {
		return nil, false, parseSFTPStatus(payload)
	}
	if kind != sshFXPHandle {
		return nil, false, fileaccess.ErrorTargetUnreachable
	}
	handle, _, err := parseString(payload)
	if err != nil {
		return nil, false, fileaccess.ErrorTargetUnreachable
	}
	defer s.closeHandle(handle)

	body = append(stringBytes(string(handle)), uint64Bytes(offset)...)
	body = append(body, uint32Bytes(length)...)
	kind, payload, err = s.request(sshFXPRead, body)
	if err != nil {
		return nil, false, err
	}
	if kind == sshFXPStatus {
		err := parseSFTPStatus(payload)
		return nil, errors.Is(err, errSFTPEOF), err
	}
	if kind != sshFXPData {
		return nil, false, fileaccess.ErrorTargetUnreachable
	}
	data, rest, err := parseString(payload)
	if err != nil || len(rest) != 0 || len(data) > int(length) {
		return nil, false, fileaccess.ErrorTargetUnreachable
	}
	return data, len(data) < int(length), nil
}

func (s *sftpFileSession) List(path string, roots []string, cursor string, limit int, policy fileaccess.DenyPolicy) ([]fileaccess.Entry, bool, error) {
	kind, payload, err := s.request(sshFXPOpendir, stringBytes(path))
	if err != nil {
		return nil, false, err
	}
	if kind == sshFXPStatus {
		return nil, false, parseSFTPStatus(payload)
	}
	if kind != sshFXPHandle {
		return nil, false, fileaccess.ErrorTargetUnreachable
	}
	handle, _, err := parseString(payload)
	if err != nil {
		return nil, false, fileaccess.ErrorTargetUnreachable
	}
	defer s.closeHandle(handle)

	candidates := make(directoryEntryHeap, 0, limit+1)
	heap.Init(&candidates)
	for {
		kind, payload, err = s.request(sshFXPReaddir, stringBytes(string(handle)))
		if err != nil {
			return nil, false, err
		}
		if kind == sshFXPStatus {
			if errors.Is(parseSFTPStatus(payload), errSFTPEOF) {
				break
			}
			return nil, false, parseSFTPStatus(payload)
		}
		if kind != sshFXPName {
			return nil, false, fileaccess.ErrorTargetUnreachable
		}
		_, names, err := parseSFTPNames(payload)
		if err != nil {
			return nil, false, fileaccess.ErrorTargetUnreachable
		}
		for _, name := range names {
			if name.name == "." || name.name == ".." || name.name <= cursor || strings.ContainsAny(name.name, "/\\\x00") {
				continue
			}
			child, err := s.Realpath(filepath.Join(path, name.name))
			if err != nil || policy.Denies(child) || !fileaccess.PathWithinRootsWithPolicy(roots, child, policy) {
				continue
			}
			attrs, err := s.Stat(child)
			if err != nil || attrs.size > uint64(^uint64(0)>>1) {
				continue
			}
			entry := fileaccess.Entry{Name: name.name, Path: child, IsDir: attrs.mode.IsDir(), Size: int64(attrs.size), ModTime: attrs.modTime.UTC()}
			if len(candidates) < limit+1 {
				heap.Push(&candidates, entry)
			} else if entry.Name < candidates[0].Name {
				heap.Pop(&candidates)
				heap.Push(&candidates, entry)
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Name < candidates[j].Name })
	hasMore := len(candidates) > limit
	if hasMore {
		candidates = candidates[:limit]
	}
	return []fileaccess.Entry(candidates), hasMore, nil
}

func (s *sftpFileSession) closeHandle(handle []byte) {
	kind, payload, err := s.request(sshFXPClose, stringBytes(string(handle)))
	if err == nil && kind == sshFXPStatus {
		_ = payload
	}
}

func (s *sftpFileSession) request(kind byte, body []byte) (byte, []byte, error) {
	id := s.nextID
	s.nextID++
	if err := s.writePacket(kind, append(uint32Bytes(id), body...)); err != nil {
		return 0, nil, fileaccess.ErrorTargetUnreachable
	}
	responseKind, payload, err := s.readPacket()
	if err != nil || len(payload) < 4 || binary.BigEndian.Uint32(payload[:4]) != id {
		return 0, nil, fileaccess.ErrorTargetUnreachable
	}
	return responseKind, payload[4:], nil
}

func (s *sftpFileSession) writePacket(kind byte, payload []byte) error {
	if len(payload)+1 > maxSFTPPacket {
		return fmt.Errorf("sftp packet too large")
	}
	header := uint32Bytes(uint32(len(payload) + 1))
	if _, err := s.in.Write(header); err != nil {
		return err
	}
	if _, err := s.in.Write(append([]byte{kind}, payload...)); err != nil {
		return err
	}
	return nil
}

func (s *sftpFileSession) readPacket() (byte, []byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(s.out, header[:]); err != nil {
		return 0, nil, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length < 1 || length > maxSFTPPacket {
		return 0, nil, fmt.Errorf("invalid sftp packet length")
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(s.out, payload); err != nil {
		return 0, nil, err
	}
	return payload[0], payload[1:], nil
}

type sftpName struct {
	name string
}

var errSFTPEOF = errors.New("sftp eof")

func parseSFTPStatus(payload []byte) error {
	if len(payload) < 4 {
		return fileaccess.ErrorTargetUnreachable
	}
	switch binary.BigEndian.Uint32(payload[:4]) {
	case sshFXOK:
		return nil
	case sshFXEOF:
		return errSFTPEOF
	case sshFXNoSuchFile:
		return fileaccess.ErrorNotFound
	case sshFXPermission:
		return fileaccess.ErrorPathNotAllowed
	default:
		return fileaccess.ErrorTargetUnreachable
	}
}

func parseSFTPNames(payload []byte) ([]byte, []sftpName, error) {
	if len(payload) < 4 {
		return nil, nil, fmt.Errorf("missing sftp name count")
	}
	count := binary.BigEndian.Uint32(payload[:4])
	rest := payload[4:]
	names := make([]sftpName, 0, count)
	for range count {
		value, next, err := parseString(rest)
		if err != nil {
			return nil, nil, err
		}
		_, rest, err = parseString(next) // longname is not used by the protocol.
		if err != nil {
			return nil, nil, err
		}
		_, nextRest, err := parseSFTPAttributes(rest)
		if err != nil {
			return nil, nil, err
		}
		rest = nextRest
		names = append(names, sftpName{name: string(value)})
	}
	return rest, names, nil
}

func parseSFTPAttributes(payload []byte) (sshFileAttributes, []byte, error) {
	if len(payload) < 4 {
		return sshFileAttributes{}, nil, fmt.Errorf("missing sftp attribute flags")
	}
	flags := binary.BigEndian.Uint32(payload[:4])
	rest := payload[4:]
	attrs := sshFileAttributes{modTime: time.Unix(0, 0).UTC()}
	if flags&sshFXFSize != 0 {
		if len(rest) < 8 {
			return sshFileAttributes{}, nil, fmt.Errorf("missing sftp size")
		}
		attrs.size = binary.BigEndian.Uint64(rest[:8])
		rest = rest[8:]
	}
	if flags&0x00000002 != 0 {
		if len(rest) < 8 {
			return sshFileAttributes{}, nil, fmt.Errorf("missing sftp uid/gid")
		}
		rest = rest[8:]
	}
	if flags&sshFXFPermissions != 0 {
		if len(rest) < 4 {
			return sshFileAttributes{}, nil, fmt.Errorf("missing sftp permissions")
		}
		attrs.mode = fileModeFromSFTPPermissions(binary.BigEndian.Uint32(rest[:4]))
		rest = rest[4:]
	}
	if flags&sshFXFACModTime != 0 {
		if len(rest) < 8 {
			return sshFileAttributes{}, nil, fmt.Errorf("missing sftp times")
		}
		attrs.modTime = time.Unix(int64(binary.BigEndian.Uint32(rest[4:8])), 0).UTC()
		rest = rest[8:]
	}
	return attrs, rest, nil
}

func fileModeFromSFTPPermissions(permissions uint32) os.FileMode {
	mode := os.FileMode(permissions & 0o777)
	switch permissions & 0o170000 {
	case 0o040000:
		mode |= os.ModeDir
	case 0o120000:
		mode |= os.ModeSymlink
	case 0o020000:
		mode |= os.ModeDevice
	case 0o060000:
		mode |= os.ModeDevice | os.ModeCharDevice
	case 0o010000:
		mode |= os.ModeNamedPipe
	case 0o140000:
		mode |= os.ModeSocket
	}
	return mode
}

func stringBytes(value string) []byte { return append(uint32Bytes(uint32(len(value))), value...) }
func uint32Bytes(value uint32) []byte {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, value)
	return buf
}
func uint64Bytes(value uint64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, value)
	return buf
}
func parseString(payload []byte) ([]byte, []byte, error) {
	if len(payload) < 4 {
		return nil, nil, fmt.Errorf("missing sftp string length")
	}
	length := binary.BigEndian.Uint32(payload[:4])
	if uint64(length) > uint64(len(payload)-4) {
		return nil, nil, fmt.Errorf("invalid sftp string length")
	}
	return payload[4 : 4+length], payload[4+length:], nil
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
func readDirectoryPage(dir *os.File, roots []string, path, cursor string, limit int, policy fileaccess.DenyPolicy) ([]fileaccess.Entry, bool, error) {
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
			if _, resolveErr := fileaccess.ResolveRootPathWithPolicy(roots, child, policy); resolveErr != nil {
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
