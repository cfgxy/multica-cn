package fileaccess

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		want  string
		class error
	}{
		{name: "canonical absolute path", path: "/srv/data/./report.txt", want: "/srv/data/report.txt"},
		{name: "relative path denied", path: "report.txt", class: ErrorPathNotAllowed},
		{name: "parent traversal denied", path: "/srv/data/../secret", class: ErrorPathNotAllowed},
		{name: "sensitive file denied", path: "/srv/data/.env", class: ErrorSensitivePathDenied},
		{name: "ssh key denied", path: "/srv/data/id_rsa", class: ErrorSensitivePathDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizePath(tt.path)
			if tt.class != nil {
				if !errors.Is(err, tt.class) {
					t.Fatalf("NormalizePath(%q) error = %v, want class %q", tt.path, err, tt.class)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("NormalizePath(%q) = %q, %v; want %q", tt.path, got, err, tt.want)
			}
		})
	}
}

func TestResolveTargetIDByHost(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		targets []Target
		wantID  string
		wantErr bool
	}{
		{name: "registered target id", host: "target-a", targets: []Target{{ID: "target-a", RuntimeID: "runtime-a"}}, wantID: "target-a"},
		{name: "registered runtime id", host: "runtime-a", targets: []Target{{ID: "target-a", RuntimeID: "runtime-a"}}, wantID: "target-a"},
		{name: "missing host", targets: []Target{{ID: "target-a", RuntimeID: "runtime-a"}}, wantErr: true},
		{name: "unknown host", host: "unknown", wantErr: true},
		{name: "ambiguous runtime id", host: "runtime-a", targets: []Target{{ID: "target-a", RuntimeID: "runtime-a"}, {ID: "target-b", RuntimeID: "runtime-a"}}, wantErr: true},
		{name: "host matches different target and runtime", host: "shared-id", targets: []Target{{ID: "shared-id", RuntimeID: "runtime-a"}, {ID: "target-b", RuntimeID: "shared-id"}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveTargetIDByHost(tt.host, tt.targets)
			if tt.wantErr {
				if !errors.Is(err, ErrorHostNotRegistered) {
					t.Fatalf("ResolveTargetIDByHost error = %v, want %q", err, ErrorHostNotRegistered)
				}
				return
			}
			if err != nil || got != tt.wantID {
				t.Fatalf("ResolveTargetIDByHost(%q) = %q, %v; want %q", tt.host, got, err, tt.wantID)
			}
		})
	}
}

func TestResolveRoot(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "inside")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "private.txt")
	if err := os.WriteFile(outsideFile, []byte("hidden"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}

	if _, err := ResolveRootPath([]string{root}, filepath.Join(root, "inside")); err != nil {
		t.Fatalf("resolve inside path: %v", err)
	}
	if _, err := ResolveRootPath([]string{root}, filepath.Join(root, "escape")); !errors.Is(err, ErrorSymlinkEscapeDenied) {
		t.Fatalf("resolve symlink escape error = %v, want %q", err, ErrorSymlinkEscapeDenied)
	}
	if _, err := ResolveRootPath([]string{root}, outsideFile); !errors.Is(err, ErrorPathNotAllowed) {
		t.Fatalf("resolve outside path error = %v, want %q", err, ErrorPathNotAllowed)
	}
	if _, err := OpenBeneath([]string{root}, filepath.Join(root, "escape", "private.txt"), os.O_RDONLY); !errors.Is(err, ErrorSymlinkEscapeDenied) {
		t.Fatalf("open symlink escape error = %v, want %q", err, ErrorSymlinkEscapeDenied)
	}
}

func TestHasTargetAccess(t *testing.T) {
	target := Target{ID: "node-a", Enabled: true, WorkspaceIDs: []string{"workspace-a"}, MemberIDs: []string{"member-a"}}
	if err := HasTargetAccess(target, "workspace-a", "member-a"); err != nil {
		t.Fatalf("authorized target access: %v", err)
	}
	if err := HasTargetAccess(target, "workspace-b", "member-a"); err != ErrorPermissionDenied {
		t.Fatalf("wrong workspace error = %v, want %q", err, ErrorPermissionDenied)
	}
	if err := HasTargetAccess(target, "workspace-a", "member-b"); err != ErrorPermissionDenied {
		t.Fatalf("wrong member error = %v, want %q", err, ErrorPermissionDenied)
	}
	target.Enabled = false
	if err := HasTargetAccess(target, "workspace-a", "member-a"); err != ErrorTargetDisabled {
		t.Fatalf("disabled target error = %v, want %q", err, ErrorTargetDisabled)
	}
}

func TestPathWithinRoots(t *testing.T) {
	if !PathWithinRoots([]string{"/srv/data"}, "/srv/data/reports/a.txt") {
		t.Fatal("path inside root was rejected")
	}
	if PathWithinRoots([]string{"/srv/data"}, "/srv/database/secret.txt") {
		t.Fatal("path sharing a string prefix but outside root was accepted")
	}
}
