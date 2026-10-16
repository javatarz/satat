# Hatchet caps spike (#101)

Part of #99. Checks whether Hatchet's concurrency keys give global, per-lane, per-repo and per-account caps together, and whether a lane can be backed off to zero and restored without losing queued work. Feeds the Hatchet ADR (#107).

## Setup

- `hatchet-lite` (latest image on 2026-10-16) with Postgres 15.6, via Docker Compose on the laptop.
- Python SDK `hatchet-sdk` 1.42.1. Python only because it was quickest to script. The Go SDK exposes the same options.
- Run on 2026-10-16. The harness was `docs/scratch/hatchet-spike/` (gitignored, not committed).
- Each test recorded start and end times inside the task and computed peak concurrency per key offline.

## Results

| # | Check | Result |
|---|-------|--------|
| 1 | Four concurrency keys on one workflow (`'global'`, `input.lane`, `input.repo`, `input.account`), 36 runs of 3 s | **Pass.** No key ever exceeded its cap. With global 6 / lane 3 / repo 2 / account 3 the peaks were 4 / 3 / 2 / 3. With global 2 the global cap bound (peak 2, makespan 55.8 s for 36 runs, as expected). |
| 2 | Per-lane caps that differ (Claude 1, gateway 3) | **Pass**, via a CEL `max_runs` (`max_runs="input.cap"`). `max_runs` takes an int or a CEL expression. Peaks were 1 and 3. |
| 3 | Cap of zero | **Held, not lost.** `max_runs` evaluating to 0 queued three runs and none ran. A later run for the same key with cap 2 released all of them. The cap is read from run input, so there is no live knob: a release needs a new run (or a re-registered workflow). |
| 4 | Workflow pause with long queue TTL | **Pass.** `pause(queue_ttl=720h)` on two workflows, five runs each, queued for about 6.3 min, then `unpause()`. All 10 ran. In-flight runs are not interrupted. |
| 5 | Live lane back-off with a rate limit | **Pass.** A task with `RateLimit(static_key="lane-claude", units=1)`. `rate_limits.put(key, 0, MINUTE)` was accepted, five runs queued and none ran in 47 s. After `put(key, 100, MINUTE)` all five ran within about 35 s (the next refill window). `schedule_timeout` was 720 h. |
| 6 | `schedule_timeout` behaviour (20 s timeout, waited 3.5 min) | **Mixed.** Runs held by a concurrency cap or a zero rate limit ended `FAILED` without running (exact cancel time not measured, between 65 s and about 215 s). Runs on a **paused** workflow stayed `QUEUED`: pause shields them from `schedule_timeout`, and the queue TTL governs instead. |
| 7 | GitHub webhook intake | **Pass.** Source `GITHUB`, HMAC SHA256 on `X-Hub-Signature-256`, event key `'github:' + headers['x-github-event'] + ':' + input.action`. Six signed deliveries returned 200 and started runs. A bad signature returned 403. A per-repo cap keyed off `input.repository.full_name` serialised runs per repo (5.1 s gaps for 5 s tasks) and ran repos in parallel. |
| 8 | Cron trigger | **Pass, with a gotcha.** `on_crons=["* * * * *"]` fires. A cron on a workflow whose concurrency key reads `input.*` produced **no runs and no error**, because the cron input is empty. Creating the cron with `hatchet.cron.create(..., input={"repo": ...})` fixed it. |

## Findings

1. **The four caps hold together.** Global, lane, repo and account are four `ConcurrencyExpression` entries with `GROUP_ROUND_ROBIN` on one workflow. Over-cap work queues. The account dimension needs no special handling.
2. **Per-key limits are not uniform.** `max_runs` as a CEL expression gives each lane (or account, or repo) its own cap, as long as the trigger puts the cap in the input or metadata. The mapper must therefore look the cap up from `satat.yaml` and pass it. Alternatively make each lane its own workflow with a literal `max_runs`.
3. **Claude back-off has two working mechanisms.** Pause the lane's workflow (needs the lane to be its own workflow, queue TTL long) or drive a rate limit to zero (live, one API call, resumes at the next window). The rate limit works from a single shared workflow and does not need the lane to be a separate workflow, so it is the better default. A cap of zero in `max_runs` also holds work but cannot be changed live.
4. **Queue waits are silently cancelled unless the timeout is raised on every task.** `schedule_timeout` applies to waits behind a cap or a rate limit, not only to waits for a worker slot. The ADR must require a long `schedule_timeout` (720 h worked) on every task, and a long `queue_ttl` on every pause. The docs say default 5 min; this spike confirmed the cancel with a 20 s override and did not wait out the 5-minute default on an unpaused queue.
5. **Pause has its own clock.** A paused workflow's queued runs are governed by the queue TTL, not `schedule_timeout`. The dashboard defaults the TTL to 24 h; the SDK call requires one.
6. **Retention is not a risk for queued work.** The data-retention setting (`SERVER_LIMITS_DEFAULT_TENANT_RETENTION_PERIOD`, default 720 h) deletes runs in a final state, per the docs. It affects history, not queued runs. Not tested. Set it long if audit history matters.
7. **Cron needs explicit input when keys read from input.** Use `hatchet.cron.create` with input per repo, or avoid input-keyed expressions on cron workflows. This failed silently.
8. **Webhook intake works end to end for GitHub** with signature checking. The event key and the concurrency key can both come from the payload, so the mapper may be thin.
9. **Idle footprint is small.** After the tests, `hatchet-lite` used 56 MiB and Postgres 393 MiB (point-in-time `docker stats`, not a load measurement). #104 measures the real figure on the VM.

## Not covered

- A queue held for hours. The longest wait was about 6 min. The 720 h settings were accepted and not contradicted, but a multi-hour wait was not observed.
- Exact time to `schedule_timeout` cancellation, and whether the unpaused 5-minute default behaves the same as the 20 s override.
- A cron firing twice at the first minute for a plain workflow (two rows appeared at one timestamp). Possibly from a re-registered worker. Not investigated.
- The data-retention variable, tenant-scoped concurrency (`is_tenant_scoped`) across workflows, and behaviour on k3s or arm64.
- Behaviour at load: 36 runs at a time only.

## Feeds

- #107, Hatchet ADR. The Temporal fallback trigger ("caps or timeouts fail") did not fire: caps pass, timeouts need explicit overrides.
- The Claude-lane back-off design (build step 6): prefer a live rate limit over pause or a zero cap.
- Build step 4 (webhooks, mapper, reconciler): set `schedule_timeout` and cron input explicitly.
