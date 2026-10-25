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

## Slice 6: webhook intake (2026-10-25)

Code only; **not run against a real Hatchet webhook or GitHub yet.** `workflows/intake.go` adds a second
workflow, `satat-intake`, triggered by the event key `github:issues`. It maps GitHub's `issues.labeled` body to
the run input (`MapWebhook`) and pushes `satat:issue`, which the existing workflow already handles. It queues
only `labeled` + `ready-for-agent` on an open issue (not a PR) in a repo named in `SATAT_INTAKE_REPOS`; the
list is required, so an unset variable queues nothing and the worker logs that intake is off. The worker
still re-reads the issue's labels at run start, so a stale delivery for an issue that lost its label launches nothing.
It also re-reads the title and body (#181) and ignores the payload's text, so a forged delivery for a
genuinely labelled issue cannot supply its own text. This does not cover someone with write access
editing the body, or an edit after labelling (#175). A forged delivery can still choose which
labelled issue runs, in a repo the worker is configured for.
Unit tests cover the mapping, every skip, fail-closed config and a failed push (which fails the intake run so
Hatchet retries it).

Security limits of this slice:

- **The webhook secret is mandatory.** Never expose a webhook with a blank secret on a tunnel.
- **Tunnelling to port 8888 exposes the whole Hatchet API and dashboard,** not just the webhook path. Put a
  tunnel or proxy in front that forwards only the webhook path, or use `gh webhook forward` and expose nothing.
- Anyone holding a Hatchet token can push `github:issues` or `satat:issue` directly; the repo allowlist does
  not cover that.
- The #175 rule (Satat's machine user must not be the labeller) is deferred to #175. Nothing checks the
  sender yet; Satat never applies `ready-for-agent` today.
- Redeliveries of one delivery are not deduplicated: a redelivered event queues a second run for the issue.

Setup, once, in the Hatchet dashboard (Webhooks, new): source GitHub, the webhook secret from 1Password, event
key expression `'github:issues'` (a constant: GitHub's event type is a header, and the mapper checks the
payload's `action` and `label` itself). In GitHub, point an `Issues`-only webhook at the tunnel URL Hatchet
shows (or `gh webhook forward --events=issues --url=...` for the laptop). Compose binds Hatchet to
127.0.0.1, so a tunnel to port 8888 is needed.

Not verified: that Hatchet delivers the raw GitHub body as the task input with this event key, the exact URL
path, and the 3 s to 10 s GitHub gives a delivery to get a response.

## Cache volumes for #120 (2026-10-25)

Checks behind the launcher's cache volumes (ADR 0033), on Docker 29.5.2 (Ubuntu 24.04 VM, overlayfs).
Each step ran with the launcher's flags: read-only root, uid 10001, `--cap-drop ALL`,
`no-new-privileges`, `/tmp` as a `nosuid,nodev` tmpfs. The test image was busybox with
`/cache/go-mod` owned by 10001 and holding one file, `seed`.

| Case | Result |
|---|---|
| New named volume mounted at `/cache/go-mod`, copy-up on (the launcher's cache mounts) | `10001:10001 755`, `seed` copied in, writable by the step |
| Same with `volume-nocopy` (the input volume's setting) | `0:0 755`, empty, `Permission denied` for the step |
| Volume made by `volume create` first, then mounted | copied on first mount, as above |
| Volume first mounted where the image has no `/cache/go-mod`, then in the image above | first `0:0`; second mount copied and became `10001:10001` |
| Used volume (holds a file) mounted again | not re-seeded |
| Volume emptied by a step, then mounted again | **re-seeded**: `seed` is back |
| `/proc/mounts` | `/tmp` is `tmpfs ... nosuid,nodev,noexec`; the cache is `ext4 rw` with no `noexec` |
| Run a script from `/tmp`, then the same script from the cache | `Permission denied`, then it ran |
| `system df -v` while a running container holds the volume, then after it exits | `links=1`, then `links=0` |
| `volume rm` while held | refused: `volume is in use` |
| `volume create` on an existing name with other labels | succeeds and returns the existing volume, labels unchanged |

What this settles:

- **Copy-up carries the image's ownership into a new cache volume,** so caches need no chown and the
  step stays non-root. It also copies the directory's contents, and does so again whenever the volume is
  empty at mount time. The image's `/cache/*` directories should therefore be empty.
- **A cache volume made before the image had the directory is root-owned, but heals.** While it is
  empty, the next mount in an image that has the directory copies it again. A persistent cache can only
  stay root-owned if something non-empty got into it, which a non-root step cannot do.
- **`/tmp` is `noexec` even though the launcher does not ask for it:** Docker's tmpfs default adds it.
  A cache volume is not `noexec`, so `/cache/tmp` works as `TMPDIR` for builds that execute what they
  unpack. The launcher keeps `/tmp` as it is.
- **Use counts and the in-use refusal are both real.** The cache reaper skips any volume `df` reports
  with links, and if a step attaches one after the read, Docker refuses the removal.
- **Creating a volume is not a check that it is ours.** An existing name comes back with its own labels
  and no error, so the launcher compares labels, driver and options before it mounts a volume it did
  not just make. A test creates a `satat-` volume that is a host bind in disguise and checks the
  launcher refuses it and leaves it in place.
- **The cache reaper needs `SYSTEM: 1` on the socket proxy.** Volume sizes and use counts come only from
  `GET /system/df`. In the pinned proxy's `haproxy.cfg` that flag allows every path under `/system`;
  the launcher calls only that one. Whether the Engine API has any write endpoint under `/system` was
  not checked.

Not verified here: the real `claude` image with its cache env vars (it is not built on this machine),
how long `/system/df` takes with large caches, and a cache volume filling the VM's disk.
