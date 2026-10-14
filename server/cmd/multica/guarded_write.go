package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/multica-ai/multica/server/internal/cli"
)

// guardedWriteOutcome labels the failure of an update that carried
// expected_revision (RUYI-666). The server applies a guarded write as one
// conditional UPDATE, so what a failure means for the data depends on where
// it happened:
//
//   - 4xx: the server rejected the request before touching the row — a
//     deterministic failure. The write did not land; resolving the conflict
//     (re-read, re-apply) is the only way forward.
//   - 5xx or a transport-level failure (timeout, refused connection, reset):
//     the request may have been applied before the failure surfaced — an
//     AMBIGUOUS outcome. The CLI has no automatic retry at any layer and this
//     contract forbids adding one for guarded writes: blind resending can
//     double-apply or mask a concurrent writer. The operator re-reads the
//     current revision and re-applies with the fresh value instead.
//
// Errors from requests that did not carry expected_revision are returned
// untouched: the legacy contract is unchanged (opt-in).
func guardedWriteOutcome(err error, rereadHint string) error {
	if err == nil {
		return nil
	}
	ambiguous := true
	var httpErr *cli.HTTPError
	if errors.As(err, &httpErr) && httpErr.StatusCode < 500 {
		ambiguous = false
	}
	if ambiguous {
		fmt.Fprintf(os.Stderr, "WARNING: outcome AMBIGUOUS — the guarded write (expected_revision) failed with a server error or below the HTTP layer, so it may or may not have landed. Do NOT blindly retry: %s\n", rereadHint)
	}
	return err
}
