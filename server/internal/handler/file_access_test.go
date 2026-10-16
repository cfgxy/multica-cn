package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/fileaccess"
)

func TestResolveFileAccessTargetByHost(t *testing.T) {
	runtimeID := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	target := db.DaemonFileAccessTarget{ID: "target-a", RuntimeID: runtimeID}
	runtimeHost := uuidToString(runtimeID)
	tests := []struct {
		name    string
		host    string
		targets []db.DaemonFileAccessTarget
		wantID  string
		wantErr bool
	}{
		{name: "target id", host: target.ID, targets: []db.DaemonFileAccessTarget{target}, wantID: target.ID},
		{name: "runtime id with one target", host: runtimeHost, targets: []db.DaemonFileAccessTarget{target}, wantID: target.ID},
		{name: "missing host", targets: []db.DaemonFileAccessTarget{target}, wantErr: true},
		{name: "unknown host", host: "unknown", wantErr: true},
		{name: "ambiguous runtime id", host: runtimeHost, targets: []db.DaemonFileAccessTarget{target, {ID: "target-b", RuntimeID: runtimeID}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveFileAccessTargetByHost(tt.host, tt.targets)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected host resolution to fail")
				}
				if got.ID != "" {
					t.Fatalf("failed resolution returned target %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != tt.wantID {
				t.Fatalf("resolved target id = %q, want %q", got.ID, tt.wantID)
			}
		})
	}
}

func TestListRuntimeFilesRejectsMissingHost(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/runtimes/runtime-path/files/list", strings.NewReader(`{"path":"/tmp"}`))
	response := httptest.NewRecorder()
	(&Handler{}).ListRuntimeFiles(response, req)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), fileaccess.ErrorHostNotRegistered.Error()) {
		t.Fatalf("response = %q, want missing-host error", response.Body.String())
	}
}
