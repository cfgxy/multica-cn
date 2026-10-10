package computer

import (
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeRemote struct {
	exists     bool
	existsErr  error
	password   string
	pamCalls   int
	created    bool
	deleted    bool
	written    bool
	started    bool
	writeErr   error
	createErr  error
	installErr error
}

func (f *fakeRemote) UserExists(string) (bool, error) {
	if f.existsErr != nil {
		return false, f.existsErr
	}
	return f.exists, nil
}
func (f *fakeRemote) PasswordMatches(_ string, password string) (bool, error) {
	f.pamCalls++
	return f.exists && password == f.password, nil
}
func (f *fakeRemote) CreateUser(string, string) error {
	f.created = true
	if f.createErr != nil {
		return f.createErr
	}
	f.exists = true
	return nil
}
func (f *fakeRemote) DeleteUser(string) error { f.deleted = true; return nil }
func (f *fakeRemote) WriteFiles(string, CredentialFiles) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.written = true
	return nil
}
func (f *fakeRemote) InstallDaemon(string) error {
	if f.installErr != nil {
		return f.installErr
	}
	f.started = true
	return nil
}

type memAttempts struct {
	mu        sync.Mutex
	n         int
	refundErr error
	resetErr  error
}

func (m *memAttempts) WithKey(_, _ string, fn func(Attempt) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return fn(m)
}
func (m *memAttempts) Reserve(limit int) (int, bool, error) {
	if m.n >= limit {
		return m.n, false, nil
	}
	m.n++
	return m.n, true, nil
}
func (m *memAttempts) Refund() error {
	if m.refundErr != nil {
		return m.refundErr
	}
	if m.n > 0 {
		m.n--
	}
	return nil
}
func (m *memAttempts) Reset() error {
	if m.resetErr != nil {
		return m.resetErr
	}
	m.n = 0
	return nil
}

func req(password string) Request {
	return Request{
		ComputerID:   "gpu-pc",
		ServerURL:    "http://10.10.0.31:3000",
		Username:     "zhang",
		Password:     password,
		GitName:      "Zhang",
		GitEmail:     "zhang@company.example",
		GitLabURL:    "http://10.12.0.79",
		GitLabToken:  "glpat-x",
		MulticaPAT:   "mul_pat",
		FailureLimit: 2,
	}
}

func TestApplyCreateReuseRename(t *testing.T) {
	fresh := &fakeRemote{}
	store := &memAttempts{}
	res, err := Apply(fresh, store, req("secret"))
	if err != nil || res.Action != ActionCreate || !fresh.created || !fresh.written || !fresh.started {
		t.Fatalf("create: %+v %v remote=%+v", res, err, fresh)
	}

	same := &fakeRemote{exists: true, password: "secret"}
	res, err = Apply(same, store, req("secret"))
	if err != nil || res.Action != ActionReuse || same.created || !same.written {
		t.Fatalf("reuse: %+v %v remote=%+v", res, err, same)
	}

	other := &fakeRemote{exists: true, password: "other"}
	res, err = Apply(other, &memAttempts{}, req("secret"))
	if err != nil || res.Action != ActionRename || other.created || other.written || other.started || other.deleted {
		t.Fatalf("rename must not touch the account: %+v %v remote=%+v", res, err, other)
	}
}

func TestApplyLockSkipsPAM(t *testing.T) {
	remote := &fakeRemote{exists: true, password: "other"}
	store := &memAttempts{}
	if _, err := Apply(remote, store, req("secret")); err != nil {
		t.Fatal(err)
	}
	res, err := Apply(remote, store, req("secret"))
	if err != nil || res.Action != ActionLocked {
		t.Fatalf("second failure should lock: %+v %v", res, err)
	}
	calls := remote.pamCalls
	res, err = Apply(remote, store, req("secret"))
	if err != nil || res.Action != ActionLocked || remote.pamCalls != calls {
		t.Fatalf("locked call must not touch PAM: %+v calls %d -> %d err %v", res, calls, remote.pamCalls, err)
	}
}

func TestApplyConcurrentReserveDoesNotExceedPAM(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	remote := &gateRemote{fakeRemote: fakeRemote{exists: true, password: "other"}, entered: entered, release: release}
	store := &memAttempts{}
	one := req("secret")
	one.FailureLimit = 1
	var wg sync.WaitGroup
	results := make(chan Result, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := Apply(remote, store, one)
			results <- res
			errs <- err
		}()
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("expected one PAM call")
	}
	select {
	case <-entered:
		t.Fatal("second concurrent request reached PAM")
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	wg.Wait()
	close(results)
	close(errs)
	var locked, other int
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for res := range results {
		if res.Action == ActionLocked {
			locked++
		} else {
			other++
		}
	}
	if locked != 2 || remote.pamCalls != 1 {
		t.Fatalf("locked=%d other=%d pam=%d", locked, other, remote.pamCalls)
	}
}

type gateRemote struct {
	fakeRemote
	mu      sync.Mutex
	entered chan struct{}
	release chan struct{}
}

func (g *gateRemote) PasswordMatches(user, password string) (bool, error) {
	g.mu.Lock()
	g.pamCalls++
	g.mu.Unlock()
	g.entered <- struct{}{}
	<-g.release
	return g.fakeRemote.password == password && g.exists, nil
}

func TestPasswordErrorRefundsReservation(t *testing.T) {
	remote := &errRemote{fakeRemote: fakeRemote{exists: true}, err: errString("ssh timeout")}
	store := &memAttempts{}
	_, err := Apply(remote, store, req("secret"))
	if err == nil {
		t.Fatal("expected error")
	}
	var n int
	var allowed bool
	err = store.WithKey("gpu-pc", "zhang", func(a Attempt) error {
		var err error
		n, allowed, err = a.Reserve(2)
		return err
	})
	if err != nil || !allowed || n != 1 {
		t.Fatalf("reservation stuck after transport error: n=%d allowed=%v err=%v", n, allowed, err)
	}
}

type errRemote struct {
	fakeRemote
	err error
}

func (e *errRemote) PasswordMatches(string, string) (bool, error) {
	e.pamCalls++
	return false, e.err
}

func TestConcurrentCreateOnce(t *testing.T) {
	remote := &createOnce{entered: make(chan struct{}, 2), release: make(chan struct{})}
	store := &memAttempts{}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := Apply(remote, store, req("secret"))
			errs <- err
		}()
	}
	select {
	case <-remote.entered:
	case <-time.After(time.Second):
		t.Fatal("create did not start")
	}
	select {
	case <-remote.entered:
		t.Fatal("second create ran concurrently")
	case <-time.After(150 * time.Millisecond):
	}
	close(remote.release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if remote.creates != 1 {
		t.Fatalf("creates=%d", remote.creates)
	}
}

type createOnce struct {
	mu      sync.Mutex
	exists  bool
	creates int
	entered chan struct{}
	release chan struct{}
}

func (c *createOnce) UserExists(string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exists, nil
}
func (c *createOnce) PasswordMatches(string, string) (bool, error) { return true, nil }
func (c *createOnce) CreateUser(string, string) error {
	c.mu.Lock()
	c.creates++
	c.exists = true
	c.mu.Unlock()
	c.entered <- struct{}{}
	<-c.release
	return nil
}
func (c *createOnce) DeleteUser(string) error                  { return nil }
func (c *createOnce) WriteFiles(string, CredentialFiles) error { return nil }
func (c *createOnce) InstallDaemon(string) error               { return nil }

func TestRefundAndResetFailuresAreReported(t *testing.T) {
	lookup := &fakeRemote{existsErr: errString("ssh down")}
	store := &memAttempts{refundErr: errString("disk full")}
	_, err := Apply(lookup, store, req("secret"))
	if err == nil || !strings.Contains(err.Error(), "refund failed") || !strings.Contains(err.Error(), "remains charged") && !strings.Contains(err.Error(), "attempt count") {
		t.Fatalf("refund error not reported: %v", err)
	}

	remote := &fakeRemote{exists: true, password: "secret"}
	charged := &memAttempts{resetErr: errString("disk full"), refundErr: errString("disk full")}
	_, err = Apply(remote, charged, req("secret"))
	if err == nil || !strings.Contains(err.Error(), "reset failed") || !strings.Contains(err.Error(), "remains charged") {
		t.Fatalf("reset+refund failure not reported: %v", err)
	}
	if charged.n != 1 {
		t.Fatalf("count=%d, want the reserved attempt kept", charged.n)
	}
}

func TestApplyRollsBackFailedCreate(t *testing.T) {
	remote := &fakeRemote{writeErr: errString("disk full password=secret")}
	_, err := Apply(remote, &memAttempts{}, req("secret"))
	if err == nil || !remote.deleted || !remote.created {
		t.Fatalf("err=%v deleted=%v created=%v", err, remote.deleted, remote.created)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("error leaked password: %v", err)
	}
}
