package handler

// Heartbeat-delivery side of the workspace backpressure settings read
// (RUYI-618, F2 fix): the wire card and the settings-API response resolve
// storage outcomes differently. The API degrades ANY failure to the code
// defaults so the form keeps rendering; the heartbeat must NOT re-arm
// defaults on a transient storage failure — an owner-saved card (in
// particular enabled=false) must survive DB jitter, so a non-ErrNoRows read
// failure delivers no card at all and the daemon keeps its current one.
// These are pure-function tests: no database fixture required.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func savedBackpressureRow() db.WorkspaceBackpressureSetting {
	return db.WorkspaceBackpressureSetting{
		WorkspaceID:           db.WorkspaceBackpressureSetting{}.WorkspaceID, // zero UUID fine for pure resolution
		Enabled:               false,
		MemHighPct:            20,
		MemRecoveryPct:        30,
		SwapHighPct:           70,
		SwapRecoveryPct:       50,
		PsiHighPct:            40,
		PsiRecoveryPct:        10,
		SampleIntervalSeconds: 10,
		WindowSize:            4,
	}
}

func TestBackpressureWireCard_ErrNoRowsDeliversDefaults(t *testing.T) {
	got := backpressureWireCard(nil, pgx.ErrNoRows)
	def := protocol.DefaultDaemonBackpressureConfig()
	if got == nil || *got != *def {
		t.Fatalf("never-saved workspace must deliver the code defaults, got %+v", got)
	}
}

func TestBackpressureWireCard_SavedRowRoundTrips(t *testing.T) {
	row := savedBackpressureRow()
	got := backpressureWireCard(&row, nil)
	if got == nil {
		t.Fatal("saved row must deliver a card")
	}
	if got.Enabled {
		t.Fatal("saved enabled=false must round-trip — the wire card is what keeps a disarmed gate disarmed")
	}
	if got.MemHighPct != 20 || got.MemRecoveryPct != 30 || got.SampleIntervalSeconds != 10 || got.WindowSize != 4 ||
		got.SwapHighPct != 70 || got.SwapRecoveryPct != 50 || got.PSIHighPct != 40 || got.PSIRecoveryPct != 10 {
		t.Fatalf("saved card mismatch: %+v", got)
	}
}

func TestBackpressureWireCard_StorageFailureOmitsCard(t *testing.T) {
	// A transient failure (pool exhaustion, cancel, outage) must deliver nil
	// so the daemon keeps its current card: re-arming the defaults here would
	// briefly override an owner's saved card — worst exactly when the host is
	// under enough load to exhaust the pool, i.e. when backpressure matters.
	for _, err := range []error{errors.New("connection pool exhausted"), context.Canceled} {
		if got := backpressureWireCard(nil, err); got != nil {
			t.Fatalf("storage failure %v must omit the wire card, got %+v", err, got)
		}
	}
}

func TestBackpressureSettingsFromRead_DegradesToDefaults(t *testing.T) {
	// The settings API keeps rendering through any read failure.
	for _, err := range []error{pgx.ErrNoRows, errors.New("connection pool exhausted")} {
		resp := backpressureSettingsFromRead(nil, err)
		def := protocol.DefaultDaemonBackpressureConfig()
		if resp.Custom {
			t.Fatalf("read failure %v must report custom=false", err)
		}
		if resp.DaemonBackpressureConfig != *def {
			t.Fatalf("read failure %v must degrade to the defaults, got %+v", err, resp.DaemonBackpressureConfig)
		}
	}
}

func TestBackpressureSettingsFromRead_SavedRow(t *testing.T) {
	row := savedBackpressureRow()
	resp := backpressureSettingsFromRead(&row, nil)
	if !resp.Custom {
		t.Fatal("saved row must report custom=true")
	}
	if resp.DaemonBackpressureConfig.Enabled {
		t.Fatal("saved card must round-trip through the API response")
	}
}
