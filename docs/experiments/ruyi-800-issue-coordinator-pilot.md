# RUYI-800 Issue Coordinator Pilot

## Status

Preparation only. No real agent runs, production flag changes, account access, or paid experiment is authorized by this plan. The backend downgrade checks must pass independent Review and QA before experiment execution is considered.

## Question

Does resuming an Issue-scoped coordinator session reduce repeated Issue reads and token cost without slowing the first useful action or reducing task completion?

## Sample and assignment

- Use 30 matched pairs of synthetic or explicitly approved, non-sensitive follow-up cases: 30 baseline runs and 30 opt-in runs, 60 runs total.
- Each pair uses equivalent Issue state, follow-up request, prior comments, and task acceptance criteria. Use isolated Issue fixtures per arm so one run cannot teach or mutate the other arm's context.
- Randomize which arm runs first within each pair. Keep the pair's task order and fixture content fixed after registration.
- Use the same agent identity, model identifier, provider, runtime build, prompt/runtime configuration, and resource limits for both arms. Record their exact versions before the first run. If any changes mid-pilot, stop and restart the affected pair after restoring parity.
- Baseline has the coordinator flag absent or explicitly false. Opt-in enables the flag only for the pilot workspace and designated synthetic Issues. Do not enable it globally.

## Measurements

Record one row per run with pair and arm identifiers, versions, start/end timestamps, outcome, task completion, first useful action time, total input tokens, cache-read tokens, Issue reads, comment reads, and the number of repeated reads of an unchanged record.

- First useful action: the first provider/tool action that changes task state or produces a task-specific artifact accepted by the case criteria. Context reads, status polling, and setup calls do not count.
- Completion rate: completed cases divided by started cases; report cancellations and infrastructure failures separately.
- Repeated reads: reads after the same run has already fetched the same unchanged Issue revision or comment set. Report total and per-run counts.
- Report p50 and p95 first-useful-action latency, input tokens, cache-read tokens, total Issue/comment reads, repeated reads, and completion rate for each arm. Include per-pair deltas and all excluded pairs with reasons.

## Analysis and decision rules

Treat this 30-pair pilot as a screening experiment, not a definitive model-quality claim. Report paired bootstrap 95% confidence intervals for median latency, input-token, cache-read, and repeated-read differences. Report p95 descriptively because 30 observations per arm provide a noisy tail estimate. Report completion-rate difference with an exact paired interval and the discordant pair counts.

Proceed to a larger evaluation only when all of these hold:

- Opt-in reduces median repeated reads by at least 20% and median input tokens by at least 10%.
- The paired interval for median first-useful-action latency does not indicate a regression greater than 10%.
- Completion-rate point estimate is no worse than baseline by more than 5 percentage points, with no safety or correctness regression in case review.
- No opt-in run resumes a session after an overflow or provider resume rejection, and every downgrade retains the allowed snapshot references and continues on a fresh session.

Stop the opt-in arm immediately if any unsafe session is resumed, snapshot data contains transcript or secret content, completion falls more than 5 percentage points below baseline, or p95 first-useful-action latency exceeds baseline by more than 10%. Preserve sanitized measurements and route the failure for Review before restarting.

## Required environment and authorization

- A dedicated non-production workspace with disposable synthetic Issues, isolated from shared business data.
- A fixed and recorded agent/provider/model/runtime configuration for the full pair set.
- Opt-in flag access scoped to the pilot workspace and designated Issue IDs, plus a tested rollback that disables the flag and returns claims to the existing per-task path.
- An approved telemetry destination that stores only run identifiers, counters, timings, version labels, and outcomes. Do not store prompts, transcripts, credentials, or raw provider payloads.
- Explicit authorization for real provider accounts, any provider cost, and the dedicated workspace/runtime. These approvals and resource details are not present in this preparation phase.

## Rollback

Disable the coordinator flag for the pilot scope, verify new claims use the existing per-task path, stop new opt-in runs, and retain only sanitized aggregate results and the failure evidence needed for diagnosis. Do not delete shared resources or stop a shared PostgreSQL service as part of rollback.
