// Package hostmetrics keeps the latest host resource snapshot per daemon
// (RUYI-618) and exposes it as Prometheus series. Snapshots ride heartbeat
// reports from daemon-side node-exporter relays and live only in memory —
// Prometheus is the metrics source of truth and no database table is
// involved. Stale daemons age out of the exposition so a decommissioned host
// stops being scraped rather than freezing its last values forever.
package hostmetrics

import (
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// DefaultSnapshotTTL is how long a daemon's snapshot stays exposed after its
// last heartbeat carried a resource report. Two missed 15s heartbeats keep
// the series alive; a daemon offline for a quarter hour disappears, matching
// the runtime liveness semantics rather than pretending the host froze.
const DefaultSnapshotTTL = 15 * time.Minute

type snapshot struct {
	report *protocol.DaemonResourceReport
	at     time.Time
}

// Store accumulates the latest snapshot per daemon and implements
// prometheus.Collector so the metrics registry reads straight from it.
type Store struct {
	mu   sync.Mutex
	now  func() time.Time
	ttl  time.Duration
	last map[string]snapshot
}

// NewStore returns an empty store. ttl <= 0 selects DefaultSnapshotTTL.
func NewStore(ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = DefaultSnapshotTTL
	}
	return &Store{
		now:  time.Now,
		ttl:  ttl,
		last: make(map[string]snapshot),
	}
}

// RecordHostResources stores the latest snapshot for daemonID. Reports from
// multiple runtimes of the same daemon converge here — same host, near-
// identical values, last write wins.
func (s *Store) RecordHostResources(daemonID string, report *protocol.DaemonResourceReport) {
	if daemonID == "" || report == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.last[daemonID] = snapshot{report: report, at: s.now()}
}

// populated prunes expired snapshots and returns the live set.
func (s *Store) populated() map[string]snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for id, snap := range s.last {
		if now.Sub(snap.at) > s.ttl {
			delete(s.last, id)
		}
	}
	out := make(map[string]snapshot, len(s.last))
	for id, snap := range s.last {
		out[id] = snap
	}
	return out
}
