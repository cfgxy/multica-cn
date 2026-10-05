package auth

import (
	"context"
	"errors"
	"testing"
)

// The gate answers "may this grant still act?". Its verdict contract is what
// the revocation promise rests on: revoked / disabled / deleted-client all
// reject, cache only ever stores verdicts about existing rows, and a gate
// that cannot answer is an error the middleware treats as reject — never a
// silent "assume alive".

func gateLookupRows(rows map[string]OAuthGrantState) OAuthGrantLookup {
	return func(ctx context.Context, grantID string) (OAuthGrantState, bool, error) {
		state, ok := rows[grantID]
		return state, ok, nil
	}
}

func TestOAuthGateLiveGrantPasses(t *testing.T) {
	rows := map[string]OAuthGrantState{
		"g-live": {ClientFound: true},
	}
	gate := NewOAuthGate(nil, gateLookupRows(rows))

	state, found, fresh, err := gate.Check(context.Background(), "g-live")
	if err != nil || !found || !fresh {
		t.Fatalf("Check = (%v, %v, %v), err = %v; want found+fresh live", found, fresh, state, err)
	}
	if !state.Live() {
		t.Fatalf("state %+v should be live", state)
	}
}

func TestOAuthGateRejectsRevokedDisabledAndDeletedClient(t *testing.T) {
	rows := map[string]OAuthGrantState{
		"g-revoked":  {ClientFound: true, GrantRevoked: true},
		"g-disabled": {ClientFound: true, ClientDisabled: true},
		"g-orphan":   {ClientFound: false},
	}
	gate := NewOAuthGate(nil, gateLookupRows(rows))

	for id := range rows {
		state, found, _, err := gate.Check(context.Background(), id)
		if err != nil || !found {
			t.Fatalf("%s: Check err = %v, found = %v; want a resolved row", id, err, found)
		}
		if state.Live() {
			t.Fatalf("%s: state %+v must not be live", id, state)
		}
	}
}

func TestOAuthGateMissingGrantRowIsFoundFalse(t *testing.T) {
	gate := NewOAuthGate(nil, gateLookupRows(nil))

	_, found, fresh, err := gate.Check(context.Background(), "g-gone")
	if err != nil {
		t.Fatalf("Check err = %v", err)
	}
	if found {
		t.Fatal("a grant row that does not exist must report found=false")
	}
	if !fresh {
		t.Fatal("absence is never cached, so every miss is fresh")
	}
}

func TestOAuthGateLookupFailureIsErrGateUnavailable(t *testing.T) {
	gate := NewOAuthGate(nil, func(ctx context.Context, grantID string) (OAuthGrantState, bool, error) {
		return OAuthGrantState{}, false, errors.New("db down")
	})

	if _, _, _, err := gate.Check(context.Background(), "g-1"); !errors.Is(err, ErrGateUnavailable) {
		t.Fatalf("Check err = %v, want ErrGateUnavailable", err)
	}
}

func TestOAuthGateNilGateAndNilLookupUnavailable(t *testing.T) {
	var nilGate *OAuthGate
	if _, _, _, err := nilGate.Check(context.Background(), "g-1"); !errors.Is(err, ErrGateUnavailable) {
		t.Fatalf("nil gate err = %v, want ErrGateUnavailable", err)
	}
	if _, _, _, err := NewOAuthGate(nil, nil).Check(context.Background(), "g-1"); !errors.Is(err, ErrGateUnavailable) {
		t.Fatalf("nil lookup err = %v, want ErrGateUnavailable", err)
	}
}

func TestOAuthGateNilRedisHitsLookupEveryTime(t *testing.T) {
	calls := 0
	gate := NewOAuthGate(nil, func(ctx context.Context, grantID string) (OAuthGrantState, bool, error) {
		calls++
		return OAuthGrantState{ClientFound: true}, true, nil
	})

	for i := 0; i < 3; i++ {
		if _, found, _, err := gate.Check(context.Background(), "g-1"); err != nil || !found {
			t.Fatalf("Check #%d = (%v, %v)", i, found, err)
		}
	}
	if calls != 3 {
		t.Fatalf("lookup called %d times with a nil Redis, want 3 (no cache to serve)", calls)
	}
}

func TestOAuthGateInvalidateNilSafe(t *testing.T) {
	var nilGate *OAuthGate
	nilGate.Invalidate(context.Background(), "g-1")
	NewOAuthGate(nil, gateLookupRows(nil)).Invalidate(context.Background(), "g-1")
	NewOAuthGate(nil, gateLookupRows(nil)).InvalidateClient(context.Background(), []string{"a", "b"})
}
