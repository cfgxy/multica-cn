package service

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/plugincontract"
)

// Event delivery is best-effort by decision, not by accident: an event may be
// dropped under backpressure, and one that is delivered may arrive more than
// once. What the platform owes a handler in exchange is the ability to tell a
// repeat from a new event — and that is worth exactly nothing unless the id it
// recognises survives the retry that produced the duplicate.
//
// These tests hold the two halves of that: one delivery keeps one id across
// every attempt, and two deliveries never share one.

// eventDeliveryService builds a dispatcher's service over a real database.
//
// deliver() consults the circuit breaker and records telemetry, and both read
// Queries. A Queries over an unopened pool does not merely fail those reads, it
// dereferences nil inside pgxpool, so the database is a precondition of
// exercising this path at all rather than a detail of what is asserted.
//
// The rows written are invocation telemetry under randomly generated ids;
// relationships are application-owned by repository policy, so no workspace or
// installation has to exist for them to land.
func eventDeliveryService(t *testing.T, harness *hookTestServer) *PluginService {
	t.Helper()
	service := hookTestService(t, harness)
	service.Queries = db.New(newPluginStoragePool(t))
	return service
}

func deliveredIDs(t *testing.T, harness *hookTestServer, want int) []string {
	t.Helper()
	ids := make([]string, 0, want)
	for i := 0; i < want; i++ {
		select {
		case received := <-harness.received:
			var body hookRequestBody
			if err := json.Unmarshal(received.Body, &body); err != nil {
				t.Fatalf("decode delivered body %d: %v", i, err)
			}
			ids = append(ids, body.DeliveryID)
		default:
			t.Fatalf("expected %d deliveries, only %d arrived", want, i)
		}
	}
	return ids
}

// The retry is the whole point. An endpoint that fails twice and then succeeds
// sees the same logical event three times; a handler that stored "delivery X is
// done" after the third can only avoid re-doing the work if the first two
// carried that same X.
func TestEventRetriesReuseOneDeliveryID(t *testing.T) {
	harness := newHookTestServer(t)
	failures := 0
	harness.respond = func(w http.ResponseWriter, _ []byte) {
		failures++
		if failures <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}

	service := eventDeliveryService(t, harness)
	host := hookTestHost(harness)
	installation := hookTestInstallation(t, harness.server.URL+"/hooks/summarize", host,
		[]string{plugincontract.TriggerEvent})
	hook, err := FindHook(installation, "summarize")
	if err != nil {
		t.Fatalf("find hook: %v", err)
	}

	dispatcher := NewPluginEventDispatcher(service)
	t.Cleanup(dispatcher.Close)
	// deliver() directly rather than through the queue: this is about the id
	// across attempts, and the backoff between them is real time the worker
	// pool would only add scheduling noise to.
	dispatcher.deliver(t.Context(), installation, hook, dispatchJob{eventType: plugincontract.EventIssueCreated})

	ids := deliveredIDs(t, harness, 3)
	if ids[0] == "" {
		t.Fatal("the first attempt carried no delivery_id; a handler has nothing to deduplicate on")
	}
	if !strings.HasPrefix(ids[0], "ped_") {
		t.Fatalf("delivery_id = %q, want the ped_ prefix that marks an event delivery", ids[0])
	}
	for attempt, id := range ids {
		if id != ids[0] {
			t.Fatalf("attempt %d carried delivery_id %q, want %q — a retry that changes the id makes every duplicate look new",
				attempt+1, id, ids[0])
		}
	}
}

// And the id must not be so stable that it merges events. The same issue
// updated the same way twice publishes two byte-identical payloads; treating
// them as one delivery would make a correct handler discard real work.
func TestTwoEventsNeverShareADeliveryID(t *testing.T) {
	harness := newHookTestServer(t)
	service := eventDeliveryService(t, harness)
	host := hookTestHost(harness)
	installation := hookTestInstallation(t, harness.server.URL+"/hooks/summarize", host,
		[]string{plugincontract.TriggerEvent})
	hook, err := FindHook(installation, "summarize")
	if err != nil {
		t.Fatalf("find hook: %v", err)
	}

	dispatcher := NewPluginEventDispatcher(service)
	t.Cleanup(dispatcher.Close)
	// Identical jobs, twice: same event type, same payload, same installation.
	job := dispatchJob{
		eventType: plugincontract.EventIssueCreated,
		payload:   map[string]any{"issue": map[string]any{"id": "3fa85f64-5717-4562-b3fc-2c963f66afa6"}},
	}
	dispatcher.deliver(t.Context(), installation, hook, job)
	dispatcher.deliver(t.Context(), installation, hook, job)

	ids := deliveredIDs(t, harness, 2)
	if ids[0] == ids[1] {
		t.Fatalf("two separate events shared delivery_id %q — a handler deduplicating on it would drop the second", ids[0])
	}
}

// The generator itself, independent of transport: distinct per call, and
// prefixed so a delivery id is identifiable in a log next to the scheduled
// path's psd_ ids.
func TestEventDeliveryIDsAreDistinctAndPrefixed(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 256; i++ {
		id := newEventDeliveryID()
		if !strings.HasPrefix(id, "ped_") {
			t.Fatalf("delivery id %q is missing the ped_ prefix", id)
		}
		if seen[id] {
			t.Fatalf("delivery id %q was minted twice", id)
		}
		seen[id] = true
	}
}
