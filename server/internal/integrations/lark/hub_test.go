package lark

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// fakeHubQueries is the unit-test seam for HubQueries. The lease state
// is held in memory so a single fake can play both "we hold the lease"
// and "another replica holds the lease" scenarios across one test.
type fakeHubQueries struct {
	mu             sync.Mutex
	installations  []db.LarkInstallation
	listErr        error
	leaseOwner     map[string]string    // installation_id -> ws_lease_token
	leaseExpiresAt map[string]time.Time // installation_id -> expiry
	acquireErr     error
	releaseErr     error
	now            func() time.Time
	acquireCount   int32
}

func newFakeHubQueries() *fakeHubQueries {
	return &fakeHubQueries{
		leaseOwner:     make(map[string]string),
		leaseExpiresAt: make(map[string]time.Time),
		now:            time.Now,
	}
}

func (f *fakeHubQueries) ListActiveLarkInstallations(ctx context.Context) ([]db.LarkInstallation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]db.LarkInstallation, len(f.installations))
	copy(out, f.installations)
	return out, nil
}

func (f *fakeHubQueries) AcquireLarkWSLease(ctx context.Context, arg db.AcquireLarkWSLeaseParams) (db.LarkInstallation, error) {
	atomic.AddInt32(&f.acquireCount, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.acquireErr != nil {
		return db.LarkInstallation{}, f.acquireErr
	}
	id := uuidString(arg.ID)
	owner, hasOwner := f.leaseOwner[id]
	exp := f.leaseExpiresAt[id]
	now := f.now()
	// CAS: accept when no holder, holder expired, or holder is us.
	if !hasOwner || exp.Before(now) || owner == arg.NewToken.String {
		f.leaseOwner[id] = arg.NewToken.String
		f.leaseExpiresAt[id] = arg.NewExpiresAt.Time
		// Return the (synthetic) row — the supervise loop only checks
		// the error, not the row contents.
		return db.LarkInstallation{ID: arg.ID}, nil
	}
	// Live lease held by someone else.
	return db.LarkInstallation{}, errPgxNoRows
}

func (f *fakeHubQueries) ReleaseLarkWSLease(ctx context.Context, arg db.ReleaseLarkWSLeaseParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.releaseErr != nil {
		return f.releaseErr
	}
	id := uuidString(arg.ID)
	if f.leaseOwner[id] == arg.CurrentToken.String {
		delete(f.leaseOwner, id)
		delete(f.leaseExpiresAt, id)
	}
	return nil
}

// presetLease forcibly assigns a lease to a holder other than the hub
// under test. Used to verify "another replica owns it" branches.
func (f *fakeHubQueries) presetLease(id pgtype.UUID, token string, expires time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.leaseOwner[uuidString(id)] = token
	f.leaseExpiresAt[uuidString(id)] = expires
}

// fakeConnector counts how many times Run was invoked and behaves
// according to the script provided per-call. The default behavior
// (script nil) blocks on ctx.Done — useful for the "owns lease, stays
// connected" test.
type fakeConnector struct {
	mu     sync.Mutex
	runs   int
	script []func(ctx context.Context, emit func(InboundMessage)) error
	emit   func(InboundMessage)
}

func (f *fakeConnector) Run(ctx context.Context, _ db.LarkInstallation, emit func(InboundMessage)) error {
	f.mu.Lock()
	idx := f.runs
	f.runs++
	if idx < len(f.script) {
		fn := f.script[idx]
		f.mu.Unlock()
		return fn(ctx, emit)
	}
	f.mu.Unlock()
	// Default: hold until cancelled.
	f.emit = emit
	<-ctx.Done()
	return nil
}

func (f *fakeConnector) Runs() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs
}

func uuidFromString(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		t.Fatalf("scan uuid %q: %v", s, err)
	}
	return u
}

func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestHubAcquiresLeaseAndStartsSupervisor(t *testing.T) {
	q := newFakeHubQueries()
	instID := uuidFromString(t, "11111111-1111-1111-1111-111111111111")
	q.installations = []db.LarkInstallation{{ID: instID, Status: "active"}}

	conn := &fakeConnector{}
	factory := func(_ db.LarkInstallation) (EventConnector, error) { return conn, nil }

	hub := NewHub(q, factory, nil, HubConfig{
		LeaseTTL:           500 * time.Millisecond,
		LeaseRenewInterval: 50 * time.Millisecond,
		PollInterval:       10 * time.Millisecond,
		MinBackoff:         5 * time.Millisecond,
		MaxBackoff:         50 * time.Millisecond,
		ResetBackoffAfter:  1 * time.Second,
		Logger:             newDiscardLogger(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)

	// Wait until the supervisor has started the connector at least once.
	if !waitFor(200*time.Millisecond, func() bool { return conn.Runs() >= 1 }) {
		t.Fatalf("expected connector to start; runs=%d", conn.Runs())
	}

	cancel()
	hub.Wait()

	// After shutdown the lease should be released so another replica
	// can take over without waiting for the TTL to elapse.
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.leaseOwner[uuidString(instID)]; ok {
		t.Fatalf("lease should be released after shutdown, got owner %q", q.leaseOwner[uuidString(instID)])
	}
}

func TestHubSkipsInstallationWhenAnotherReplicaHoldsLease(t *testing.T) {
	q := newFakeHubQueries()
	instID := uuidFromString(t, "22222222-2222-2222-2222-222222222222")
	q.installations = []db.LarkInstallation{{ID: instID, Status: "active"}}
	// Another replica already owns the lease for the next 10 seconds.
	q.presetLease(instID, "other-replica", time.Now().Add(10*time.Second))

	conn := &fakeConnector{}
	factory := func(_ db.LarkInstallation) (EventConnector, error) { return conn, nil }

	hub := NewHub(q, factory, nil, HubConfig{
		LeaseTTL:           500 * time.Millisecond,
		LeaseRenewInterval: 20 * time.Millisecond,
		PollInterval:       20 * time.Millisecond,
		MinBackoff:         5 * time.Millisecond,
		MaxBackoff:         20 * time.Millisecond,
		ResetBackoffAfter:  1 * time.Second,
		Logger:             newDiscardLogger(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)

	// Give the hub plenty of opportunity to try to take over.
	time.Sleep(150 * time.Millisecond)

	if conn.Runs() != 0 {
		t.Fatalf("connector should not run while another replica owns lease; runs=%d", conn.Runs())
	}

	cancel()
	hub.Wait()
}

func TestHubReclaimsLeaseAfterAnotherReplicaExpires(t *testing.T) {
	q := newFakeHubQueries()
	instID := uuidFromString(t, "33333333-3333-3333-3333-333333333333")
	q.installations = []db.LarkInstallation{{ID: instID, Status: "active"}}
	// Set the other replica's lease to expire in 80ms so the hub
	// (which polls/renews on 20ms intervals) will pick it up.
	q.presetLease(instID, "other-replica", time.Now().Add(80*time.Millisecond))

	conn := &fakeConnector{}
	factory := func(_ db.LarkInstallation) (EventConnector, error) { return conn, nil }

	hub := NewHub(q, factory, nil, HubConfig{
		LeaseTTL:           500 * time.Millisecond,
		LeaseRenewInterval: 20 * time.Millisecond,
		PollInterval:       20 * time.Millisecond,
		MinBackoff:         5 * time.Millisecond,
		MaxBackoff:         20 * time.Millisecond,
		ResetBackoffAfter:  1 * time.Second,
		Logger:             newDiscardLogger(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)

	if !waitFor(500*time.Millisecond, func() bool { return conn.Runs() >= 1 }) {
		t.Fatalf("expected connector to start after lease expiry; runs=%d", conn.Runs())
	}
	cancel()
	hub.Wait()
}

func TestHubReapsSupervisorWhenInstallationRevoked(t *testing.T) {
	q := newFakeHubQueries()
	instID := uuidFromString(t, "44444444-4444-4444-4444-444444444444")
	q.installations = []db.LarkInstallation{{ID: instID, Status: "active"}}

	conn := &fakeConnector{}
	factory := func(_ db.LarkInstallation) (EventConnector, error) { return conn, nil }

	hub := NewHub(q, factory, nil, HubConfig{
		LeaseTTL:           500 * time.Millisecond,
		LeaseRenewInterval: 20 * time.Millisecond,
		PollInterval:       20 * time.Millisecond,
		MinBackoff:         5 * time.Millisecond,
		MaxBackoff:         20 * time.Millisecond,
		ResetBackoffAfter:  1 * time.Second,
		Logger:             newDiscardLogger(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)
	defer func() { cancel(); hub.Wait() }()

	if !waitFor(200*time.Millisecond, func() bool { return conn.Runs() >= 1 }) {
		t.Fatalf("expected connector to start; runs=%d", conn.Runs())
	}

	// Simulate revocation: the installation disappears from
	// ListActiveLarkInstallations. The Hub should cancel its
	// supervisor on the next sweep, which releases the lease.
	q.mu.Lock()
	q.installations = nil
	q.mu.Unlock()

	if !waitFor(500*time.Millisecond, func() bool {
		q.mu.Lock()
		defer q.mu.Unlock()
		_, stillHeld := q.leaseOwner[uuidString(instID)]
		return !stillHeld
	}) {
		t.Fatalf("expected lease to be released after revocation")
	}
}

func TestHubBacksOffOnFactoryError(t *testing.T) {
	q := newFakeHubQueries()
	instID := uuidFromString(t, "55555555-5555-5555-5555-555555555555")
	q.installations = []db.LarkInstallation{{ID: instID, Status: "active"}}

	factoryCalls := int32(0)
	factory := func(_ db.LarkInstallation) (EventConnector, error) {
		atomic.AddInt32(&factoryCalls, 1)
		return nil, errors.New("boom")
	}

	hub := NewHub(q, factory, nil, HubConfig{
		LeaseTTL:           500 * time.Millisecond,
		LeaseRenewInterval: 20 * time.Millisecond,
		PollInterval:       20 * time.Millisecond,
		MinBackoff:         5 * time.Millisecond,
		MaxBackoff:         20 * time.Millisecond,
		ResetBackoffAfter:  1 * time.Second,
		Logger:             newDiscardLogger(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)

	// Let the supervisor retry under backoff. We want > 1 call to
	// prove the loop is alive but the increasing delay should keep
	// the rate sane.
	if !waitFor(200*time.Millisecond, func() bool { return atomic.LoadInt32(&factoryCalls) >= 2 }) {
		t.Fatalf("expected factory retries under backoff; got %d", atomic.LoadInt32(&factoryCalls))
	}
	calls := atomic.LoadInt32(&factoryCalls)
	cancel()
	hub.Wait()
	if calls > 200 {
		t.Fatalf("backoff appears broken: %d factory calls in 200ms", calls)
	}
}

// waitFor polls cond until it returns true or the deadline is reached.
// Returns true on success. Tests use this instead of time.Sleep so they
// remain robust on slow CI runners without slowing fast ones down.
func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return cond()
}
