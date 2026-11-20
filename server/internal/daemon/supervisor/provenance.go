package supervisor

import (
	"fmt"
	"path/filepath"

	"github.com/multica-ai/multica/server/pkg/agent"
)

const workerProvenanceFile = "worker-provenance.json"

// WriteWorkerProvenance persists bounded provider evidence inside the
// supervisor-owned run directory. It rejects an identity mismatch instead of
// allowing evidence to be attached to a different worker.
func (m *Manager) WriteWorkerProvenance(runID string, provenance agent.RunProvenance) error {
	if err := ValidateRunID(runID); err != nil {
		return err
	}
	if provenance.RunID != "" && provenance.RunID != runID {
		return fmt.Errorf("supervisor: provenance run mismatch")
	}
	provenance.RunID = runID
	return writeJSONFile(m.provenancePath(runID), &provenance, 0o600)
}

func (m *Manager) ReadWorkerProvenance(runID string) (*agent.RunProvenance, error) {
	if err := ValidateRunID(runID); err != nil {
		return nil, err
	}
	var provenance agent.RunProvenance
	if err := readJSONFile(m.provenancePath(runID), &provenance); err != nil {
		return nil, err
	}
	if provenance.RunID != runID {
		return nil, fmt.Errorf("supervisor: provenance run mismatch")
	}
	return &provenance, nil
}

func (m *Manager) provenancePath(runID string) string {
	return filepath.Join(m.Dir(runID), workerProvenanceFile)
}

var _ agent.WorkerProvenanceWriter = (*Supervisor)(nil)

func (s *Supervisor) WriteWorkerProvenance(runID string, provenance agent.RunProvenance) error {
	return s.mgr.WriteWorkerProvenance(runID, provenance)
}
