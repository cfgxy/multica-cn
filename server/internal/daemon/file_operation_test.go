package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/fileaccess"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func policyDispatchFields(t *testing.T) (json.RawMessage, int64) {
	t.Helper()
	policy := fileaccess.DefaultDenyPolicy()
	encoded, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	return encoded, policy.Version
}

func TestExecuteFileOperation_ReadChunk(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(path, []byte("abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := executeFileOperation(context.Background(), protocol.DaemonFileOperationDispatch{
		TargetID: "fixture", Kind: string(fileaccess.KindRead), Path: path, Offset: 2, Length: 3, Roots: []string{root}, Policy: policyJSONForTest(t), PolicyVersion: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := got.(fileaccess.ReadResult)
	if result.Data != "Y2Rl" || result.Length != 3 || result.EOF {
		t.Fatalf("read result = %+v", result)
	}
}

func TestExecuteFileOperationRequiresMatchingPolicyAndFiltersCustomDirectoryEntries(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"visible.txt", "private-config.json"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	policy := fileaccess.DenyPolicy{Version: 7, Rules: []fileaccess.DenyRule{{
		ID: "private", Pattern: "**/private-config.json", Enabled: true,
	}}}
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	base := protocol.DaemonFileOperationDispatch{
		TargetID: "fixture", Kind: string(fileaccess.KindList), Path: root, Roots: []string{root},
		Policy: policyJSON, PolicyVersion: policy.Version,
	}
	got, err := executeFileOperation(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	page := got.(fileaccess.ListResult)
	if len(page.Entries) != 1 || page.Entries[0].Name != "visible.txt" {
		t.Fatalf("entries = %+v", page.Entries)
	}
	base.PolicyVersion++
	if _, err := executeFileOperation(context.Background(), base); err != fileaccess.ErrorPermissionDenied {
		t.Fatalf("mismatched policy version error = %v", err)
	}
	base.Policy = nil
	base.PolicyVersion = policy.Version
	if _, err := executeFileOperation(context.Background(), base); err != fileaccess.ErrorPermissionDenied {
		t.Fatalf("missing policy error = %v", err)
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
		policyJSON, policyVersion := policyDispatchFields(t)
		got, err := executeFileOperation(context.Background(), protocol.DaemonFileOperationDispatch{
			TargetID: "fixture", Kind: string(fileaccess.KindList), Path: root, Limit: 73, Cursor: cursor, Roots: []string{root}, Policy: policyJSON, PolicyVersion: policyVersion,
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
	_, err := executeFileOperation(context.Background(), protocol.DaemonFileOperationDispatch{
		TargetID: "fixture", Kind: string(fileaccess.KindMetadata), Path: path, Roots: []string{root}, Policy: policyJSONForTest(t), PolicyVersion: 1,
	})
	if err != fileaccess.ErrorPathNotAllowed {
		t.Fatalf("error = %v, want %q", err, fileaccess.ErrorPathNotAllowed)
	}
}

func TestExecuteFileOperation_RejectsChunkOverLimit(t *testing.T) {
	root := t.TempDir()
	_, err := executeFileOperation(context.Background(), protocol.DaemonFileOperationDispatch{
		TargetID: "fixture", Kind: string(fileaccess.KindRead), Path: filepath.Join(root, "file"), Length: fileaccess.MaxChunkBytes + 1, Roots: []string{root}, Policy: policyJSONForTest(t), PolicyVersion: 1,
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
	_, err := executeFileOperation(context.Background(), protocol.DaemonFileOperationDispatch{
		TargetID: "fixture", Kind: string(fileaccess.KindRead), Path: path, Length: 3,
		ExpectedSize: &wantSize, Roots: []string{root}, Policy: policyJSONForTest(t), PolicyVersion: 1,
	})
	if err != fileaccess.ErrorFileChanged {
		t.Fatalf("error = %v, want %q", err, fileaccess.ErrorFileChanged)
	}
	oldTime := time.Now().Add(-time.Hour)
	_, err = executeFileOperation(context.Background(), protocol.DaemonFileOperationDispatch{
		TargetID: "fixture", Kind: string(fileaccess.KindRead), Path: path, Length: 3,
		ExpectedModTime: oldTime.UTC().Format(time.RFC3339Nano), Roots: []string{root}, Policy: policyJSONForTest(t), PolicyVersion: 1,
	})
	if err != fileaccess.ErrorFileChanged {
		t.Fatalf("mtime error = %v, want %q", err, fileaccess.ErrorFileChanged)
	}
}

func policyJSONForTest(t *testing.T) json.RawMessage {
	policy, _ := policyDispatchFields(t)
	return policy
}

func TestSSHTargetAliasAcceptsOnlyRegisteredAliasShape(t *testing.T) {
	for _, tc := range []struct {
		target string
		want   string
		ok     bool
	}{
		{target: "ssh:gti-server", want: "gti-server", ok: true},
		{target: "ssh:dev_2", want: "dev_2", ok: true},
		{target: "ssh:gti.example", ok: false},
		{target: "ssh:user@gti-server", ok: false},
		{target: "ssh:-oProxyCommand=x", ok: false},
		{target: "gti-server", ok: false},
	} {
		got, ok := fileaccess.SSHTargetAlias(tc.target)
		if got != tc.want || ok != tc.ok {
			t.Errorf("SSHTargetAlias(%q) = (%q, %v), want (%q, %v)", tc.target, got, ok, tc.want, tc.ok)
		}
	}
}

func TestSSHConfigDeclaresOnlyExactExplicitAlias(t *testing.T) {
	configDir := t.TempDir()
	includedDir := filepath.Join(configDir, "conf.d")
	if err := os.Mkdir(includedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(configDir, "config")
	include := filepath.Join(includedDir, "registered.conf")
	if err := os.WriteFile(config, []byte("Host *.example\n  User ignored\nInclude conf.d/*.conf\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(include, []byte("Host gti-server gti-readonly\n  HostName gti\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !sshConfigDeclaresAlias("gti-server", []string{config}) {
		t.Fatal("explicit included alias was not recognized")
	}
	if sshConfigDeclaresAlias("unregistered", []string{config}) {
		t.Fatal("wildcard Host entry must not authorize an alias")
	}
}

func TestExecuteFileOperationSSHRejectsUnconfiguredAliasBeforeConnect(t *testing.T) {
	previousChecker := sshAliasConfigured
	previousFactory := openSSHFileSession
	sshAliasConfigured = func(string) bool { return false }
	called := false
	openSSHFileSession = func(context.Context, string) (sshFileSession, error) {
		called = true
		return nil, fileaccess.ErrorTargetUnreachable
	}
	t.Cleanup(func() {
		sshAliasConfigured = previousChecker
		openSSHFileSession = previousFactory
	})
	policy, version := policyDispatchFields(t)
	_, err := executeFileOperation(context.Background(), protocol.DaemonFileOperationDispatch{
		TargetID: "ssh:unregistered", Kind: string(fileaccess.KindMetadata), Path: "/configured/file", Roots: []string{"/configured"},
		Policy: policy, PolicyVersion: version,
	})
	if !errors.Is(err, fileaccess.ErrorPermissionDenied) || called {
		t.Fatalf("error = %v, attempted connection = %v", err, called)
	}
}

func TestExecuteFileOperationSSHAppliesOperationDeadline(t *testing.T) {
	previousChecker := sshAliasConfigured
	previousFactory := openSSHFileSession
	sshAliasConfigured = func(string) bool { return true }
	openSSHFileSession = func(ctx context.Context, _ string) (sshFileSession, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > sshFileOperationTimeout || time.Until(deadline) <= 0 {
			t.Fatalf("unexpected SSH operation deadline: %v (present=%v)", deadline, ok)
		}
		return nil, fileaccess.ErrorTargetUnreachable
	}
	t.Cleanup(func() {
		sshAliasConfigured = previousChecker
		openSSHFileSession = previousFactory
	})
	policy, version := policyDispatchFields(t)
	_, err := executeFileOperation(context.Background(), protocol.DaemonFileOperationDispatch{
		TargetID: "ssh:gti-server", Kind: string(fileaccess.KindMetadata), Path: "/configured/file", Roots: []string{"/configured"},
		Policy: policy, PolicyVersion: version,
	})
	if !errors.Is(err, fileaccess.ErrorTargetUnreachable) {
		t.Fatalf("error = %v", err)
	}
}

func TestSFTPFileSessionHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := newSFTPFileSession(ctx, "gti-server")
	if !errors.Is(err, fileaccess.ErrorTargetUnreachable) {
		t.Fatalf("error = %v", err)
	}
}

func TestExecuteFileOperationSSHReadsResolvedFileAndRejectsEscape(t *testing.T) {
	session := &fakeSSHSession{
		realpaths: map[string]string{
			"/configured":           "/srv/approved",
			"/configured/notes.txt": "/srv/approved/notes.txt",
			"/configured/escape":    "/srv/private/escape",
			"/configured/sensitive": "/srv/approved/.env",
		},
		attrs: map[string]sshFileAttributes{
			"/srv/approved/notes.txt": {size: 6, mode: 0o600, modTime: time.Unix(1700000000, 0).UTC()},
		},
		content: map[string][]byte{"/srv/approved/notes.txt": []byte("abcdef")},
	}
	restore := replaceSSHSessionFactory(t, session)
	defer restore()

	policy, version := policyDispatchFields(t)
	got, err := executeFileOperation(context.Background(), protocol.DaemonFileOperationDispatch{
		TargetID: "ssh:gti-server", Kind: string(fileaccess.KindRead), Path: "/configured/notes.txt", Roots: []string{"/configured"},
		Offset: 2, Length: 3, Policy: policy, PolicyVersion: version,
	})
	if err != nil {
		t.Fatal(err)
	}
	read := got.(fileaccess.ReadResult)
	if read.Path != "/srv/approved/notes.txt" || read.Data != base64.StdEncoding.EncodeToString([]byte("cde")) || read.EOF {
		t.Fatalf("read = %+v", read)
	}

	_, err = executeFileOperation(context.Background(), protocol.DaemonFileOperationDispatch{
		TargetID: "ssh:gti-server", Kind: string(fileaccess.KindMetadata), Path: "/configured/escape", Roots: []string{"/configured"},
		Policy: policy, PolicyVersion: version,
	})
	if !errors.Is(err, fileaccess.ErrorSymlinkEscapeDenied) {
		t.Fatalf("escape error = %v", err)
	}
	_, err = executeFileOperation(context.Background(), protocol.DaemonFileOperationDispatch{
		TargetID: "ssh:gti-server", Kind: string(fileaccess.KindMetadata), Path: "/configured/sensitive", Roots: []string{"/configured"},
		Policy: policy, PolicyVersion: version,
	})
	if !errors.Is(err, fileaccess.ErrorSensitivePathDenied) {
		t.Fatalf("sensitive path error = %v", err)
	}
}

func TestExecuteFileOperationSSHListsThroughRegisteredTarget(t *testing.T) {
	session := &fakeSSHSession{
		realpaths: map[string]string{
			"/configured":      "/srv/approved",
			"/configured/docs": "/srv/approved/docs",
		},
		listEntries: []fileaccess.Entry{{Name: "alpha.txt", Path: "/srv/approved/docs/alpha.txt"}},
		listMore:    true,
	}
	restore := replaceSSHSessionFactory(t, session)
	defer restore()
	policy, version := policyDispatchFields(t)
	got, err := executeFileOperation(context.Background(), protocol.DaemonFileOperationDispatch{
		TargetID: "ssh:gti-server", Kind: string(fileaccess.KindList), Path: "/configured/docs", Roots: []string{"/configured"},
		Limit: 1, Policy: policy, PolicyVersion: version,
	})
	if err != nil {
		t.Fatal(err)
	}
	page := got.(fileaccess.ListResult)
	if page.Path != "/srv/approved/docs" || len(page.Entries) != 1 || page.Next != fileaccess.EncodeCursor("alpha.txt") {
		t.Fatalf("page = %+v", page)
	}
}

func TestSFTPAttributeParserMapsPOSIXTypes(t *testing.T) {
	payload := append(uint32Bytes(sshFXFSize|sshFXFPermissions|sshFXFACModTime), uint64Bytes(12)...)
	payload = append(payload, uint32Bytes(0o040755)...)
	payload = append(payload, uint32Bytes(1)...)
	payload = append(payload, uint32Bytes(2)...)
	attrs, rest, err := parseSFTPAttributes(payload)
	if err != nil || len(rest) != 0 {
		t.Fatalf("parseSFTPAttributes error = %v, rest = %x", err, rest)
	}
	if attrs.size != 12 || !attrs.mode.IsDir() || !attrs.modTime.Equal(time.Unix(2, 0).UTC()) {
		t.Fatalf("attrs = %+v", attrs)
	}
}

func TestSFTPFileSessionIntegration(t *testing.T) {
	alias := os.Getenv("MULTICA_SFTP_TEST_ALIAS")
	if alias == "" {
		t.Skip("set MULTICA_SFTP_TEST_ALIAS to run against an authorized non-production target")
	}
	if !daemonSSHConfigDeclaresAlias(alias) {
		t.Fatalf("authorized test alias %q was not found as an explicit SSH Host entry", alias)
	}
	session, err := newSFTPFileSession(context.Background(), alias)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	root, err := session.Realpath("/etc")
	if err != nil {
		t.Fatal(err)
	}
	path, err := session.Realpath("/etc/hostname")
	if err != nil {
		t.Fatal(err)
	}
	attrs, err := session.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sshMetadataFor("ssh:"+alias, path, attrs); err != nil {
		t.Fatal(err)
	}
	data, _, err := session.Read(path, 0, 1024)
	if err != nil || len(data) == 0 {
		t.Fatalf("read error = %v, bytes = %d", err, len(data))
	}
	entries, _, err := session.List(root, []string{root}, "", 5, fileaccess.DefaultDenyPolicy())
	if err != nil || len(entries) == 0 {
		t.Fatalf("list error = %v, entries = %d", err, len(entries))
	}
}

type fakeSSHSession struct {
	realpaths   map[string]string
	attrs       map[string]sshFileAttributes
	content     map[string][]byte
	listEntries []fileaccess.Entry
	listMore    bool
}

func (s *fakeSSHSession) Close() error { return nil }

func (s *fakeSSHSession) Realpath(path string) (string, error) {
	resolved, ok := s.realpaths[path]
	if !ok {
		return "", fileaccess.ErrorNotFound
	}
	return resolved, nil
}

func (s *fakeSSHSession) Stat(path string) (sshFileAttributes, error) {
	attrs, ok := s.attrs[path]
	if !ok {
		return sshFileAttributes{}, fileaccess.ErrorNotFound
	}
	return attrs, nil
}

func (s *fakeSSHSession) Read(path string, offset uint64, length uint32) ([]byte, bool, error) {
	data, ok := s.content[path]
	if !ok || offset > uint64(len(data)) {
		return nil, false, fileaccess.ErrorNotFound
	}
	end := min(int(offset)+int(length), len(data))
	return data[offset:end], end == len(data), nil
}

func (s *fakeSSHSession) List(_ string, _ []string, _ string, _ int, _ fileaccess.DenyPolicy) ([]fileaccess.Entry, bool, error) {
	return s.listEntries, s.listMore, nil
}

func replaceSSHSessionFactory(t *testing.T, session sshFileSession) func() {
	t.Helper()
	previousSessionFactory := openSSHFileSession
	previousAliasChecker := sshAliasConfigured
	openSSHFileSession = func(context.Context, string) (sshFileSession, error) {
		return session, nil
	}
	sshAliasConfigured = func(string) bool { return true }
	return func() {
		openSSHFileSession = previousSessionFactory
		sshAliasConfigured = previousAliasChecker
	}
}
