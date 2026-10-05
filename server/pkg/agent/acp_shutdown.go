package agent

import (
	"context"
	"io"
	"log/slog"
	"time"
)

// supervisedWorkerExitGrace bounds the wait for a supervised ACP worker to
// exit on its own after the backend closes stdin. EOF propagation and a
// one-shot CLI's clean shutdown are millisecond-scale; the grace exists for
// loaded hosts and slow CLIs, not for normal operation.
const supervisedWorkerExitGrace = 5 * time.Second

// finishWorkerStdin closes the worker's stdin and — supervised mode only —
// gives it a bounded window to exit on the resulting EOF before falling
// back to the cancel driver's kill. The supervised bridge forwards the
// backend-side EOF to the worker's stdin (RUYI-424), so a one-shot CLI that
// idles after its final result — the whole ACP family — reaches a proven
// natural exit on its own, and the launcher records that exit instead of a
// kill. Cancelling unconditionally at this boundary used to SIGKILL finishing
// workers, which stranded the daemon task layer's final→completed convergence
// without a natural-exit record (RUYI-390). An already-done runCtx (user
// cancel, task timeout) skips the grace entirely — the driver is already
// tearing the worker down.
//
// Legacy (unsupervised) runs keep the shipped timing — cancel immediately,
// never wait before it: there is no launcher record to protect there, and
// the family cancellation tests pin that cancellation stays reachable
// before Wait against lingering fake CLIs.
//
// Wait is armed with runCtx, so the fallback cancel also unblocks a Wait
// parked on a worker that ignores both EOF and signal delivery; the memoized
// session wait then reports the cancellation rather than the exit, which
// every ACP backend discards — the proven exit lives in the launcher's own
// record.
func finishWorkerStdin(sess *workerSession, stdin io.Closer, runCtx context.Context, cancel context.CancelFunc, logger *slog.Logger, provider string) {
	_ = stdin.Close()
	if sess.mode != sessionSupervised {
		cancel()
		return
	}
	waitCh := make(chan struct{})
	go func() {
		_ = sess.Wait(runCtx)
		close(waitCh)
	}()
	timer := time.NewTimer(supervisedWorkerExitGrace)
	defer timer.Stop()
	select {
	case <-waitCh:
		return
	case <-timer.C:
	}
	if runCtx.Err() == nil {
		logger.Warn(provider+" worker ignored stdin EOF; forcing shutdown",
			"pid", sess.PID(), "grace", supervisedWorkerExitGrace.String())
	}
	cancel()
	<-waitCh
}
