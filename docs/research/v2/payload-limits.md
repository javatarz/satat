# Payload limits of the Hatchet and HTTP handoff routes (#194)

Part of #156. Measures how large a finished step's git bundle can get on two routes from the worker to a separate pusher (ADR 0032): the bundle as a Hatchet task input, and an HTTP `PUT` to an internal pusher endpoint. The choice between routes is #156's. This records numbers, failure modes, GitHub's own limits, the Git LFS refusal and a proposed ceiling.

## Short answer

- **Hatchet route: a bundle of about 3 MiB at most**, with the pinned hatchet-lite v0.110.5 and Go SDK v0.110.5. The SDK sends `[]byte` as base64 in JSON (4/3 the size) and the server accepts a message up to 4 MiB. A 3,072,003 byte bundle passed, a 3,145,728 byte (3 MiB) bundle failed. The 20-commit "large" bundle (4.0 MiB) fails.
- **Raising the server limit does not fix it.** With `SERVER_GRPC_MAX_MSG_SIZE` at 256 MiB the trigger is accepted, but the engine cannot deliver the task to the worker. The run retries 10 times and ends `FAILED` with a misleading message, and the worker logs nothing.
- **HTTP route: no ceiling below the disk.** Streaming to disk held the receiver at 28 MiB RSS for every size up to 100 MiB (the largest tried). The ceiling is ours to set.
- **A bundle size ceiling does not bound the work the pusher does.** A 1.4 MiB bundle expanded to a 400 MiB diff and took the current pusher to 1.8 GiB of memory. A cap on uncompressed content is needed as well.
- **Proposed ceiling: 50 MiB per bundle on the HTTP route**, plus caps on the changed content (below). On the Hatchet route the ceiling would be 2 MiB, which refuses the 20-commit bundle.
- **A Hatchet client token cannot be scoped to one task or workflow.** It is scoped to the tenant.

## Setup

- Run on 2026-10-26 on a MacBook (10 cores, 16 GiB, Apple silicon), Docker 29.5.2.
- Throwaway Compose project `satat-payload-spike` (own name, own ports 18888 and 17077, own volumes): Postgres 15.6 and hatchet-lite v0.110.5, both by the digests in `compose.yaml` (the hatchet-lite digest was checked with `scripts/image-digest.sh`). Placeholder database password, a token made with `hatchet-admin`. The running `satat` project was not touched.
- Go SDK `github.com/hatchet-dev/hatchet` v0.110.5 (`go.mod`). Hatchet worker and sender ran on the host, connecting to the published gRPC port. The HTTP route ran host to host on loopback.
- One process per role per case, so peak memory is the process's `getrusage` `Maxrss` for that case alone. hatchet-lite and Postgres memory is the peak of `docker stats` during the case, taken against a start value.
- The Hatchet task input is `{run, base_sha, head_sha, bundle []byte}`. The receiver writes the bundle to a file and returns its size and sha256, which the sender checks. The HTTP receiver streams the body to a temp file with `http.MaxBytesReader`, hashes as it writes, and renames on success.
- Bundles are made by `git bundle create BASE..branch`, as `runner/runner.go` does. The size ones are one commit adding a file of random bytes (they do not compress), sized so the bundle matches the label. "Realistic" is three commits that edit and add Go files (20 KiB). "Large" is 20 commits, each adding a 900 KiB text file (4.0 MiB). Both routes used the same files. The 1 KiB bundle is 1,025 bytes, the 50 MiB one is 52,428,801 and the 100 MiB one 104,857,598.
- Work files are in `docs/scratch/payload-spike/` (gitignored, not committed): the harness `cmd/spike`, `run-hatchet.sh`, `run-http.sh`, `results.jsonl`, `lfscheck/` and `pushbomb/`.

## Results

### Route 1: Hatchet task input (default hatchet-lite settings, as pinned)

Peak memory is RSS of the process. hatchet-lite and Postgres are shown as peak minus start.

| Bundle | Bytes | Result | Round trip | Sender RSS | Receiver RSS | hatchet-lite / Postgres growth |
|---|---|---|---|---|---|---|
| 1 KiB | 1,025 | pass | 0.09 s | 31 MiB | 33 MiB | 0 / 6 MiB |
| realistic | 20,374 | pass | 0.09 s | 32 MiB | 34 MiB | 1 / 8 MiB |
| 100 KiB | 102,402 | pass | 0.10 s | 32 MiB | 36 MiB | 8 / 0 MiB |
| 1 MiB | 1,048,576 | pass | 0.24 s | 37 MiB | 47 MiB | 45 / 16 MiB |
| 2 MiB | 2,097,155 | pass | 0.56 s | 46 MiB | 57 MiB | 93 / 29 MiB |
| 3,000 KiB | 3,072,003 | pass | 0.48 s | 53 MiB | 70 MiB | 36 / 56 MiB |
| 3 MiB | 3,145,728 | **fail** | 46.6 s to give up | 106 MiB | none | none |
| large (20 commits) | 4,208,789 | **fail** | 52.4 s to give up | 120 MiB | none | none |
| 4 MiB | 4,194,306 | **fail** | 17.2 s to give up | 125 MiB | none | none |
| 10 MiB | 10,485,762 | **fail** | 40.6 s to give up | 278 MiB | none | none |
| 50 MiB | 52,428,801 | **fail** | 39.0 s to give up | 1,197 MiB | none | none |
| 100 MiB | 104,857,598 | **fail** | 61.6 s to give up | 2,237 MiB | none | none |

Every pass delivered the same sha256. The growth columns are noisy (one sample a second, and hatchet-lite keeps memory it has used), so read them as order of magnitude only: after about 7 MiB of inputs in total hatchet-lite had gone from 98 to 241 MiB.

### Route 2: HTTP `PUT`, streamed to disk, no application limit

| Bundle | Bytes | Result | Round trip | Sender RSS | Receiver RSS |
|---|---|---|---|---|---|
| 1 KiB | 1,025 | pass | 3 ms | 28 MiB | 28 MiB |
| realistic | 20,374 | pass | 1 ms | 27 MiB | 28 MiB |
| 100 KiB | 102,402 | pass | 2 ms | 27 MiB | 27 MiB |
| 1 MiB | 1,048,576 | pass | 2 ms | 28 MiB | 28 MiB |
| large (20 commits) | 4,208,789 | pass | 5 ms | 28 MiB | 28 MiB |
| 4 MiB | 4,194,306 | pass | 6 ms | 27 MiB | 28 MiB |
| 10 MiB | 10,485,762 | pass | 12 ms | 28 MiB | 28 MiB |
| 50 MiB | 52,428,801 | pass | 57 ms | 28 MiB | 28 MiB |
| 100 MiB | 104,857,598 | pass | 125 ms | 27 MiB | 28 MiB |

Memory is flat because both sides stream a file. The times are loopback on one host and say nothing about a Docker bridge, TLS or a real disk; they show that the route adds no cost of its own. The sender here reads from a file. Today the worker holds the bundle as a `[]byte` (`Launch.Files["changes.bundle"]`), so the 28 MiB figure holds only if the launcher also streams the copy out of the container.

## Failure modes

### Hatchet

1. **Over about 3 MiB, at the default limit: the trigger is refused and no run is created.**
   - Error at the sender: `could not trigger workflow: rpc error: code = ResourceExhausted desc = message size 5531109 is larger than configured max 4194304`. A bundle just over the line gives `message is larger than configured max 4194304 - unable to determine message size: decompressed message is larger than t...`.
   - The number in the message is the JSON message, 4/3 of the bundle plus a little. The server maximum is `SERVER_GRPC_MAX_MSG_SIZE` (4,194,304 bytes by default, `pkg/config/server/server.go` in the SDK module).
   - The SDK retried before giving up: 17 to 62 s per failure.
   - The run list showed no run for any failed trigger. Nothing is retried or lost on the engine side, because the engine never saw a run. The caller gets an error after a minute.
   - The sender pays for the size even when refused. It builds the JSON and compresses it in memory: 1.2 GiB for a 50 MiB bundle and 2.2 GiB for 100 MiB. The worker's `mem_limit` in `compose.yaml` is 512 MiB, so a bundle of about 20 MiB would OOM-kill the worker on this route before the server could refuse it.
2. **Raising the server limit moves the failure to delivery and makes it worse.** hatchet-lite recreated with `SERVER_GRPC_MAX_MSG_SIZE=268435456`, then 4 MiB and 10 MiB bundles:
   - The trigger is accepted and a run is created.
   - The engine cannot push the task to the worker: `could not send start action to worker: canceled: http2: stream closed`, logged as a warning every few seconds. The likely cause is the worker's gRPC client, which has the library default 4 MiB receive limit. The SDK sets no larger value and offers no option to change it (I found none in `pkg/client` and `sdks/go`). I did not prove this cause directly; the symptom is the same at 4 MiB and 10 MiB.
   - After 11 attempts (`retryCount` 10) the run ends `FAILED` with `Could not send task to worker. This likely means that too many slots have been configured for the number of workers or the network latency between engine and worker is unusually high.` That text does not mention size.
   - The worker log shows nothing. The task function never ran, so a task-level `WithRetries` did not apply. Hatchet's own re-assignment ran 10 times and then gave up.
   - The sender waited 40 to 90 s for the error.
   So a bundle above about 3 MiB cannot be made to work by server configuration alone.
3. **Stored in Postgres, more than once.** After about 6 MiB of bundles (8.4 MiB as base64) `v1_payload` held 8.7 MB, `v1_payloads_olap` 8.7 MB and `MessageQueueItem` 24 MB. A bundle in a task input stays in run history for the retention period (720 h in `compose.yaml`). It contains the agent's source changes.

### HTTP

| Case | Result |
|---|---|
| Ceiling 50 MiB, `Content-Length` over it (100 MiB, 50 MiB + 1 byte) | `413`, decided from the header before any body is read. The sender saw the `413`. |
| Ceiling 50 MiB, chunked upload with no length, 100 MiB | `413 body over the 52428800 byte ceiling` after 50 MiB were read (`http.MaxBytesReader`). The partial file is deleted. |
| Sender dies at 20 MiB of 50 MiB | Receiver: `unexpected EOF`, no file left. Sender: `sender cut the upload`. |
| Sender stalls 6 s at 1 MiB, receiver idle timeout 2 s | Receiver: `i/o timeout` after 2 s, `400`, no file left. |

The idle timeout is a read deadline moved forward on every read (`http.ResponseController.SetReadDeadline`), so a slow but steady upload survives and a stalled one does not. A fixed `ReadTimeout` would cut a legitimate 50 MiB upload on a slow link. `ReadHeaderTimeout` is still set. Retrying is safe because the pusher is idempotent per run (`Outcome.Reused`), and an upload leaves nothing behind unless it completes.

## GitHub's limits

| Limit | Value | Source |
|---|---|---|
| File over 50 MiB | Warning from Git: "If you attempt to add or update a file that is larger than 50 MiB, you will receive a warning from Git." | [About large files on GitHub](https://docs.github.com/en/repositories/working-with-files/managing-large-files/about-large-files-on-github) |
| File over 100 MiB | Blocked: "GitHub blocks files larger than 100 MiB." Single objects: "The recommended maximum limit is 1MB. This is enforced at 100 MB." | same page, and [Repository limits](https://docs.github.com/en/repositories/creating-and-managing-repositories/repository-limits) |
| Push size | "This limit is enforced at 2GB." Recommended pushes: 6 a minute per repository. | Repository limits |
| Pack size | Not documented on either page. Not tested. | none |
| Repository size | Under 1 GB recommended, under 5 GB strongly recommended | About large files |
| Upload through the browser | 25 MiB | About large files |

The three figures in the issue (50 MiB warns, 100 MiB blocks, 2 GB per push) match the docs. These are limits on git objects and the push, not on the bundle. A bundle of compressible text can be far smaller than what it unpacks to (see below), so the bundle ceiling alone does not keep a push under them. I did not push to GitHub, so none of this was exercised, and the pack-size limit is unknown.

## Git LFS

A bundle that adds or changes an LFS pointer, or adds a `filter=lfs` rule, can be detected from the git objects alone and refused with a reason. The prototype is `docs/scratch/payload-spike/lfscheck/` (not committed). It runs after the pusher's existing `git fetch` from the bundle, on `base..head`, with no checkout, no hooks and no git-lfs binary:

1. `git diff --raw` lists the changed paths and their new blob ids. For any changed `.gitattributes` (root or nested), `git diff -U0` is searched for an added line containing `filter=lfs`.
2. Changed blobs of 1024 bytes or less (the pointer maximum) are read with `git cat-file --batch`. One that starts with `version https://git-lfs.github.com/spec/v1` (or the two older spec lines) is a pointer.
3. `git check-attr --source=<head> filter` on the changed paths. A path with `filter=lfs` whose blob is not a pointer is raw content under an LFS rule. This happens because the step has no git-lfs client, so no clean filter ran. Pushed as it is, it would put the real file into git history and bypass the rule.

Tests, all passing, each through a real `git bundle` and a fresh clone fetching it:

| Case | Reason returned |
|---|---|
| Real `git lfs track` and `git add` of a file (git-lfs 3.8.0) | `.gitattributes adds a Git LFS rule ("*.bin filter=lfs diff=lfs merge=lfs -text"); Satat does not support Git LFS in v0.2` |
| Real `git lfs` add under a rule already in the base | `big.bin is a Git LFS pointer; its object is not in the bundle and GitHub would not have it (Satat does not support Git LFS in v0.2)` |
| Hand-written pointer file only | `data/model.bin is a Git LFS pointer; ...` |
| Nested `.gitattributes` adding a rule only | `sub/.gitattributes adds a Git LFS rule (...)` |
| Raw 3 KB file under a rule already in the base | `x.bin matches a Git LFS rule but is stored as regular content; ...` |
| A doc quoting the pointer text, a 3 KB file that starts with the version line, an unrelated `.gitattributes` line | accepted |
| Base already uses LFS, change touches a file the rule does not match | accepted |

**Would GitHub reject a pointer-only push?** The issue says so. It has a server check for it: the `GH008: Your push referenced at least 1 unknown Git LFS object` error, from a pre-receive hook ([Stack Overflow answer](https://stackoverflow.com/questions/52612880/lfs-upload-missing-object-but-the-file-is-there), [git-lfs maintainer, issue 3565](https://github.com/git-lfs/git-lfs/issues/3565)). A 2026 forum thread reports it being skipped on one mirror push, which left pointers in the repo with no objects ([devactivity summary](https://devactivity.com/insights/github-lfs-mysteries-why-git-push-mirror-can-succeed-with-missing-objects-and-what-it-means-for-your-github-monitoring-dashboard/)). I did not test it against GitHub. The refusal in the pusher is needed either way: it gives a clear reason and does not depend on a server check that may not run.

**#175's repo check should also flag LFS repos.** If the base branch has `filter=lfs` in its root `.gitattributes`, the worker's clone has pointer text where the files should be (assuming no git-lfs in the worker or step images; `images/` does not mention LFS, but I did not inspect the built images), so the agent works on pointers and every change to such a path fails at the pusher. Failing the check before the step starts costs less than failing after it. Nested `.gitattributes` files are not covered by a root-only check.

## A bundle ceiling does not bound the pusher's work

`docs/scratch/payload-spike/pushbomb/` runs the current `pusher.Push` against a local bare repo and a fake GitHub API, with a bundle that is one text file of repeated lines:

| Content in the change | Bundle | Time | `pusher` process peak RSS | Largest child (git, gitleaks) |
|---|---|---|---|---|
| 10 MiB | 36 KB | 1.3 s | 67 MiB | 52 MiB |
| 100 MiB | 356 KB | 11.0 s | 473 MiB | 141 MiB |
| 400 MiB | 1.4 MiB | 43.7 s | 1,842 MiB | 540 MiB |

The pusher reads the whole diff (`gitOut`) into a string and feeds it to gitleaks, so memory grows with the uncompressed change. A 1.4 MiB bundle is far below any bundle ceiling and still needs a gigabyte or more. This matters for the split pusher, which is its own service and will have its own memory limit, and for the 100 MiB per-file and 2 GB per-push GitHub limits, which a small compressible bundle can also reach. The numbers are one machine and one content shape (a single repeated line); a real mix would differ.

## Proposed ceilings

On the HTTP route (or a shared volume), where size is ours to set:

| Limit | Value | Why |
|---|---|---|
| Bundle size | **50 MiB** (52,428,800 bytes) | Half of GitHub's per-file block and a quarter of its push limit, and at its per-file warning. HTTP ran 100 MiB flat at 28 MiB RSS, so the route does not need less. The realistic bundle is 20 KB and the 20-commit one 4 MiB. 50 MiB is generous and still bounded. |
| Any one changed blob | 50 MiB | GitHub warns at 50 MiB and blocks at 100 MiB. Checked with `git cat-file --batch-check` after the fetch, before the diff or gitleaks. |
| Total uncompressed size of changed blobs | 100 MiB (a first guess) | The pusher's diff scan costs about 4.5 times the content in memory (the 400 MiB case). 100 MiB is about 470 MiB of memory. Tune it to the pusher's memory limit. |

These are proposals. Only the first is measured against the routes. The other two come from the push test above and GitHub's limits.

**Where each is enforced**

- **Worker, before it sends.** Stop copying the bundle out of the container once it passes ceiling plus one byte (`io.LimitReader`), and fail the step with "bundle is over 50 MiB" instead of sending. This also protects the worker's memory (`mem_limit` 512 MiB), which holds the whole bundle today.
- **Pusher, again.** A `Content-Length` check (413 before reading), `http.MaxBytesReader` for the chunked case, a stream to a file, then the two content caps above. It trusts nothing in the request. Bundle size, blob size and total size are checked before gitleaks reads anything.
- Return a `Refusal` with the number and the limit in the text, so the PR-less failure shows the reason.

**On the Hatchet route** the ceiling would be 2 MiB (the measured limit is about 3 MiB; the rest is margin for the other input fields and for a different `Summary` length). It would refuse the 20-commit bundle in this spike. Moving the limit up needs both a server setting and a change the Go SDK does not appear to offer on the worker side, so I would not plan on it.

## What this means for #156

- **A bundle in a Hatchet task input is a 2 to 3 MiB design.** If bundles above that matter, Hatchet should carry a reference (a path or a key) and the bytes go another way (HTTP, a shared volume or object storage). That is the issue's option C.
- **A bundle in a Hatchet input is also kept in Postgres for the retention period** and shows in the dashboard. The HTTP route keeps no copy once the pusher deletes its scratch directory.
- **The failure mode is the weaker side of the Hatchet route.** Under the default settings the refusal is clear (a size in the message) but takes up to a minute. Under a raised server limit the error is wrong and hides the cause. A worker-side check before the trigger avoids both.
- **A Hatchet pusher is a worker with no inbound port**; an HTTP pusher needs an internal listener and a shared secret or network rule between the two. I measured size only, not that trade-off. The HTTP receiver also needs a result path back (the response body works, and a retry of the same upload is safe), where Hatchet gives the result through the run.
- The worker's memory is a problem on both routes until the launcher streams the bundle out of the container: it holds the whole file today (the Hatchet route needs about 22 times its size at sender peak for a 100 MiB bundle: 2,237 MiB).

## Can a Hatchet client token be scoped to one task or workflow?

No, not in v0.110.5. A token is scoped to a tenant.

- `hatchet-admin token create` takes `--name`, `--expiresIn` and `--tenant-id`, nothing else. The REST `CreateAPITokenRequest` has only `name` and `expiresIn`.
- The token's claims, decoded from the one made here, are `aud`, `exp`, `grpc_broadcast_address`, `iat`, `iss`, `server_url`, `sub` (the tenant id) and `token_id`. No workflow, action or role claim.
- In this spike the same token was used by the sender and the receiver without any distinction. A worker with that token registers any action name it likes and receives that work, and any holder can trigger any workflow.
- `HATCHET_CLIENT_NAMESPACE` prefixes workflow names but is chosen by the client, so it is not a restriction.
- A tenant boundary is the only separation. A pusher in its own tenant could not be triggered by the worker's token, since a token belongs to one tenant, so it does not fit this use.

For "only the pusher consumes push" on the Hatchet route, the pusher's token cannot enforce that. What keeps another worker from taking `push` tasks is that no other component holds a Hatchet token that it can use for that. The worker must hold one to run the issue workflow, so the worker could register as a `push` consumer itself. The pusher's push token is still kept by OpenBao policy (ADR 0032), so such a worker would receive bundles but could not push them, but it could read them and could fail them. On the HTTP route the pusher's listener is on a network only the worker and the pusher join, and nothing else can take work from it.

## Not verified

- Anything against real GitHub: the per-file warning and block, the push limit, the pack size limit, `GH008` for a pointer-only push, and what GitHub's own message says for a large push. Only the documentation was read.
- Hatchet and the HTTP route inside Docker networks: Hatchet used the published port on the host. HTTP was loopback. Times between containers, with TLS or on Kubernetes were not measured.
- The cause of the worker-side failure under a raised server limit is inferred (the 4 MiB default receive limit in the worker's gRPC client) and not isolated. A bundle between 3 and 4 MiB under a raised limit was not tried; 4 MiB and 10 MiB failed.
- A task that fails inside its handler on a large input (the `WithRetries` path) was not exercised.
- hatchet-lite versions other than v0.110.5, and any Hatchet setting that moves large payloads out of Postgres (the config has an external payload store; I did not try it).
- The pusher memory figures use the current `Bundle []byte` code and one repeated-line content shape. gitleaks speed on real diffs was not measured.
- Whether the built step and worker images contain git-lfs.
- Sizes above 100 MiB on HTTP, and a slow network link (only a stall and a cut were simulated).
- The `lfscheck` prototype was tested on the cases above and not on a large history, a submodule change or a rename. `git diff --no-renames` makes a rename show as add and delete, so a renamed pointer is caught as an added one.

## Feeds

- #156: the ceilings for each route, the failure modes and the token finding.
- The secrets store implementation card: the pusher's request limits, and the caps on changed content.
- #175: the LFS check on the base branch.
