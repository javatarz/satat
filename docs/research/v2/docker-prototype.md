# Docker prototype measurements (#112)

Findings from building the Docker prototype (ADR 0026). Updated per slice. Numbers are
point-in-time `docker stats`, not load measurements.

## Slice 1: Compose platform

Run on 2026-10-19: colima (arm64) with a 5.77 GiB VM, `docker compose up -d`.

| Service | Image | Idle memory |
|---|---|---|
| `hatchet-lite` | `v0.110.5` | 137 MiB |
| `postgres` | `15.6` | 258 MiB |
| `docker-proxy` | `v0.5.0` | 18 MiB |
| **Platform total** | | **about 413 MiB** |

ADR 0024 budgeted about 2.4 GiB for k3s plus the platform (with LiteLLM and a worker). Without k3s
and LiteLLM the platform is under a fifth of that. The worker is not in the stack yet.

`hatchet-lite` was healthy about 40 s after `up` (it runs its migrations on first start and
creates a tenant). The dashboard answered on `127.0.0.1:8888`.

### Docker socket proxy (`tecnativa/docker-socket-proxy` v0.5.0)

Probed from a throwaway container on the `docker-api` network, with the flags in `compose.yaml`
(`CONTAINERS`, `NETWORKS`, `VOLUMES`, `IMAGES`, `POST`, `PING`, `VERSION` on):

| Request | Result |
|---|---|
| `GET /containers/json`, `/images/json`, `/networks`, `/volumes` | 200 |
| `POST /containers/create` | reaches the engine (400 for the empty body used) |
| `POST /containers/{id}/exec` | **reaches the engine** (400 for the empty body), not blocked |
| `POST /exec/{id}/start` | 403 |
| `POST /build`, `GET /secrets`, `GET /swarm` | 403 |
| `POST /containers/prune` | **200**: removes every stopped container on the engine, not only Satat's |

Consequences, for ADR 0027:

- Exec creation passes, but starting an exec is denied, so it is inert. Do not rely on that.
- Nothing in the proxy scopes a request to Satat's own containers. `prune` and a `create` with a
  privileged body both pass. The request builder (ADR 0027's first control) is the real gate, and
  the worker must never call `prune`. The reaper removes by label, container by container.
- The proxy works as a narrowing layer and as a record of which endpoints the launcher uses.

### Still to measure

Worker RAM, real step RAM and CPU, step start time, and the platform's RAM during a step (slices 2
to 4 and the first real run).

## Slice 2: launcher interface and Docker implementation

Run on 2026-10-19 on the same colima VM. Code: `launcher/` (interface), `launcher/docker/`
(backend, hardening floor, reaper), `launcher/conformance/` (the suite a Kubernetes backend must also pass).

Results:

- **Conformance suite: 11 of 11 pass** against the Docker backend: exit code and logs, input file,
  timeout, cancel, cancel of a missing step, caller-context cancel, duplicate launches sharing one
  execution, rerun after cleanup, out-of-memory reported, lockdown observed from inside the step
  (uid 10001, read-only root, no socket, `CapEff` zero, `NoNewPrivs` 1), and an internal lane with no route out.
  The whole suite takes about 11 s.
- **Docker-specific tests pass:** nothing left behind after ok, failing, timed-out and cancelled steps (container
  and volume); adoption of a container a dead worker left running or finished; refusal to adopt a container
  that is not ours; secrets passed by name only; the reaper keeps fresh containers and removes old ones.
- **The floor refuses 47 mutations** of a good request (bind mounts, the socket, `privileged`, host network
  and namespaces, added capabilities, root users, unpinned images, missing limits and so on), and each
  error names the problem. Lane config is checked when the launcher is built.
- **Through the socket proxy:** the same suite passes with the `compose.yaml` flags. `POST=1` also enables
  `PUT` and `DELETE`. The endpoints the launcher uses are: `_ping`, `containers/{create,json,{id}/{json,
  start,stop,wait,logs,archive}}`, `DELETE containers/{id}`, `volumes/{create,list}`, `DELETE volumes/{name}`,
  `networks/{name,create}` and `images/{name}/json` and `images/create`.

Findings that changed the design:

1. **The cleanup path removed a container it should not have.** A test that makes the launcher refuse a
   same-named container that is not Satat's showed the deferred cleanup removing it by name. Cleanup now
   acts only on an ID the call created or adopted. Kept as a regression test.
2. **A read-only root filesystem refuses `PutArchive`.** Input goes into a per-step volume mounted at
   `/etc/satat`, which the copy can write. The volume is named from the step, labelled, and removed with
   the container.
3. **`/workspace` is a tmpfs for now.** It counts against the step's memory limit, so a lane's workspace and
   `/tmp` must be smaller than its memory (checked when the lane is built). Real repos may need a disk volume.
4. **Duplicate and re-delivered launches need two mechanisms.** Within one worker process, calls for one
   step share one execution (`singleflight` on the container name). Across a crash, the deterministic
   name makes the next attempt adopt the leftover container, running or finished.


## Slice 4: first real run (2026-10-20)

Claude Code 2.1.301 on the subscription token, in the `claude` lane (3 GiB, 2 CPUs, 1 GiB workspace), against
a throwaway private repo (`javatarz/satat-sandbox`, issue 1: add `greet(name)` with two tests). Run by hand with
`cmd/satat-step`.

- **It worked end to end in 15 s, exit 0.** Clone with a read-only PAT, 8 turns, one commit, a patch with the
  function and both tests. Cost reported 0.05 USD (shadow price; the run is on the subscription). Nothing
  was pushed, the container and volume were gone afterwards, and neither token value appears in any output file.
- **Memory is small: about 137 MiB peak** in samples taken every 3 s over the 15 s run (108, 127, 136 MiB; CPU
  under 5%). A 3 GiB limit is far too generous for this size of task. This is one tiny repo, so it says the harness
  is light, not what a real repo needs. #109 should still measure a larger one.
- **Rate-limit events are in the stream** as `rate_limit_event`, with the window (`five_hour`, `seven_day`),
  `utilization` (0.03 after this run), `resetsAt` and overage status. This is the input the back-off needs.
- **The harness meets the floor with no change.** HOME on the `/workspace` tmpfs was enough: no
  permissions error on the read-only root, non-root uid 10001, no capabilities.
- **Finding: the agent could not run the tests.** The image has no `python`, so it wrote the code and tests and
  said they were unverified. A lane's image has to carry the toolchain of the repos it serves. Slice 4 does not
  decide how (one image per language, a base plus language layers, or a repo-declared image); it needs an
  answer before real repos.
- **Finding: a bare `exit status 1` hid a 401.** The first two attempts failed on a bad token. The runner now
  names the API error from the stream (`authentication_failed (HTTP 401)`). `claude auth status` reports
  `oauth_token` for any token, valid or not, so it only proves the token was seen, not that it works.

## Slice 5: Hatchet workflow and worker (2026-10-21)

The worker now runs in the Compose project (`worker` service), registers `satat-issue` with Hatchet Lite, and
runs the same pipeline as the hand trigger, started by an event (`satat-enqueue`, standing in for the webhook).

- **End to end through Hatchet:** an issue pushed as an event became a completed run and a draft PR in 23 s of
  task time (QUEUED to FINISHED), with no container or volume left behind.
- **The four caps work in Satat's own wiring.** With `per_repo: 1`, two events for one repo ran one after the
  other: the second was queued at 16:55:03 and started at 16:55:26, three seconds after the first finished.
  Caps and timeouts are checked by `Spec.Validate` before anything is registered; `Register` refuses a spec
  with fewer than the four caps or a schedule timeout under 24 h, and a test covers each mutation.
- **The worker reaches Docker only through the proxy.** `DOCKER_HOST=tcp://docker-proxy:2375`, no socket mount,
  read-only root, no capabilities, 512 MiB limit. This settles the ADR 0027 item about the Go client through
  the proxy on colima. The socket check now allows `DOCKER_HOST` on the worker only, and only pointing at the
  proxy; three negative cases are caught.
- **Gotcha: the first tenant is not the default one.** Hatchet Lite creates an `internal` tenant before
  `Default`. A token made for the wrong tenant registers a worker and accepts events with no error, and no run
  is ever created. `scripts/hatchet-token.sh` now selects the tenant by slug.
- **Gotcha: the Compose file needs all required variables even for `exec`.** The token script supplies dummies.
- **Idle worker footprint** is small (a Go binary and gitleaks in a slim Debian image); the 512 MiB limit has
  not been approached. Not measured under load.
- **Not done:** the webhook intake (slice 6), retries (set to 0 because the launcher adopts a re-delivered step),
  the Claude-lane back-off, and a restart test (kill the worker mid-step and watch the next attempt adopt it) on
  the real stack.

## Deploy adapter (2026-10-23)

`satat deploy up`, the worker health endpoints and `satat agent run|ls|rm`
([ADR 0028](../../adr/0028-infrastructure-adapter-compose-first.md),
[0029](../../adr/0029-worker-health.md), [0030](../../adr/0030-on-demand-agent-runs.md)) were run on
the Compose stack on colima. These runs used a scratch copy of `satat.yaml` **without the `claude`
lane** (its image is a local-only digest, so the worker can't pull it on a clean daemon) and
**placeholder secrets** supplied as `env` references. Only the `smoke` and `sleep` lanes ran. No real
token, 1Password item or GitHub call was involved, so nothing below says anything about the `claude`
lane.

| Measurement | Result |
|---|---|
| Cold `deploy up` (no containers or volumes, includes the worker image build) | 3 min 32 s, all services healthy |
| Warm `deploy up` | 0.3 s: only `ps`, no new Hatchet token, no container restarted |
| Time to unready after stopping hatchet-lite | about 8 s (still ready at 4 s, which is inside the 12 s heartbeat age) |
| Recovery after starting hatchet-lite again | about 15 s while the worker record went INACTIVE, then ACTIVE. Restart count stayed 0 |
| `agent rm` on a running `sleep` agent | container gone within 4 s |
| Global cap of 2, three `sleep` agents | two RUNNING, one QUEUED |
| `docker compose up --remove-orphans` with two agents running | both agent containers left running |
| Warm `deploy up` with the worker already running | worker container ID and `StartedAt` unchanged |

Notes:

- The 0.3 s warm run was taken before `Up` was changed to always run `compose up -d --build worker`.
  The worker ID and `StartedAt` check in the last row was taken after that change, so together they
  cover the final behaviour.
- The global-cap run used a scratch config with `per_repo`, `per_account` and lane `max_runs` raised
  to 3. Agent runs share the repo key `adhoc`, so the shipped defaults (all 1) allow only one
  on-demand agent at a time ([ADR 0030](../../adr/0030-on-demand-agent-runs.md)).
- Agents that were running when the worker container was recreated were orphaned from the new
  worker. Cancelling them in Hatchet did not stop their containers; `deploy down` reaped them
  ("removed 2 agent containers"). By design (`cmd/satat-worker`), the reaper removes agent containers older than twice the longest lane timeout plus an hour; that wait was not measured.
- **Idle RAM with the health server was not measured.** The health server is one goroutine and one
  REST call every 5 s, but there is no number to record here.
- Not run: the `claude` lane and issue runs with real secrets, the R19 path (a failed agent run
  whose output Hatchet dropped, which is unit-tested only), paging of `agent ls` and run lookup
  against a busy tenant (unit-tested against a fake), and the worker heartbeat while an agent uses
  most of the VM's memory.
