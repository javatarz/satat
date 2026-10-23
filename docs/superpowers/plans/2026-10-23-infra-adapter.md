# Infrastructure Adapter and On-Demand Agents Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `satat deploy up|down|status|render` brings the platform up on Compose through an adapter interface, `satat agent run|ls|rm` spawns one-shot agents through Hatchet, and the worker reports readiness that includes Hatchet.

**Architecture:** A new `deploy` package defines a `Target` interface, and `deploy/compose` implements it by rendering `compose.yaml` from an embedded template and driving `docker compose`. Agents reuse the existing launcher through a second trigger event on the existing Hatchet workflow, so caps are shared. A new `health` package gives the worker `/livez` and `/readyz`, using Hatchet's own worker record (status and `lastHeartbeatAt`) as the Hatchet signal.

**Tech Stack:** Go 1.27, Hatchet Go SDK `github.com/hatchet-dev/hatchet v0.110.5` (`sdks/go`, `features.RunsClient`, `features.WorkersClient`), `github.com/moby/moby/client`, `text/template` + `embed`, Docker Compose v2, 1Password CLI (`op read`).

**Spec:** [docs/research/v2/deploy-targets.md](../../research/v2/deploy-targets.md)

## Global Constraints

- Agents hold no Hatchet token and have no health check. The worker is the only Hatchet worker and the only container with the socket proxy (ADR 0027).
- No container flag of an agent comes from `agent run` input, issue text or agent output. Input goes to `/etc/satat/input.json` only.
- Agent containers carry `satat.*` labels and **no** `com.docker.compose.*` label.
- `.env` (gitignored, mode `0600`) holds only `POSTGRES_PASSWORD` and `HATCHET_CLIENT_TOKEN`. Other secrets are resolved into the child process environment and never written to disk.
- `POSTGRES_PASSWORD` is never rotated once present in `.env`.
- Agent result: exit code, status, output file names and sizes, and the last **4096 bytes** of stdout and of stderr, with every secret value replaced by `***`.
- Caps: `satat:agent` and `satat:issue` are two events on the one workflow `satat-issue`, so the four concurrency groups are shared. The repo key for agent runs is `adhoc`.
- Readiness turns false within **30 s** of losing Hatchet, and the worker is not restarted.
- The worker exits non-zero only if it is never ready within the start-up grace (**2 min**). After its first ready, it never exits on Hatchet loss.
- Lane images are registry references pinned by manifest digest, pullable anonymously.
- `go test -p 1 ./... -count=1` passes, and `gofmt -l .` is empty, before every commit. Run `pre-commit run --all-files` too.
- Commit subjects start with `Add`, `Fix`, `Update`, `Docs`, `Test`, `Config` and so on, are at most 72 characters, have no full stop, and end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **A secret split across the 4 KiB tail boundary:** the tail is scrubbed **before** it's cut, so half a token never survives. Test in Task 6.
2. **`deploy up` on a stack that's already running:** it doesn't create a new Hatchet token, rotate the password or restart healthy services. Test in Task 5.
3. **`agent rm` on a run that already finished, or an unknown run ID:** it exits 0 with "not running" for a finished run, and non-zero with "no such run" for an unknown one. Test in Task 7.
4. **A lane's `pass_env` name missing from `deploy.secrets`:** config load fails and names the variable and the lane. Test in Task 2.
5. **Hatchet reachable but the worker record is stale (an old INACTIVE `satat-worker-*` from a previous container):** readiness looks only at this process's own worker name. Test in Task 1.

---

## File map

| File | Change | Responsibility |
|---|---|---|
| `health/health.go` | Create | `Checker`, `Check`, HTTP handler for `/livez` and `/readyz` |
| `health/hatchet.go`, `health/docker.go` | Create | The Hatchet worker-record check and the Docker ping check |
| `cmd/satat-worker/main.go` | Modify | Unique worker name, health server, exit rule, `healthcheck` subcommand, agent secrets for scrubbing |
| `config/config.go` | Modify | `Deploy` section: `target`, `secrets` |
| `deploy/deploy.go` | Create | `Target` interface, `Status`, `Secrets` |
| `deploy/secrets.go`, `deploy/envfile.go` | Create | Resolve `op://`/`env` references; ensure keys in `.env` |
| `deploy/compose/compose.go`, `deploy/compose/compose.yaml.tmpl` | Create | Compose adapter: Render, Up, Down, Status |
| `compose.yaml` | Regenerate | Rendered output, checked in |
| `workflows/issue.go` | Modify | `Kind`, `StepInput`, `EventAgent`, `MapAgent`, the agent branch and its result |
| `workflows/agent.go` | Create | `runAgent`, `tail` and scrubbing |
| `cmd/satat/main.go`, `cmd/satat/deploy.go`, `cmd/satat/agent.go` | Create | The `satat` CLI |
| `satat.yaml` | Modify | `deploy` section, `sleep` lane |
| `scripts/build-claude-image.sh` | Modify | `SATAT_REGISTRY` push and manifest digest |
| `.github/workflows/go.yml` | Modify | `satat deploy render --check` |
| `docs/design.md`, `docs/research/v2/docker-prototype.md` | Modify | Run-it-locally steps, what's built, measurements |

---

### Task 1: Worker health

**Files:**
- Create: `health/health.go`, `health/hatchet.go`, `health/docker.go`
- Test: `health/health_test.go`
- Modify: `cmd/satat-worker/main.go`, `compose.yaml` (hand edit; Task 3 turns it into a template)

**Interfaces:**
- Produces:
  - `type Check struct { Name string; Run func(ctx context.Context) error }`
  - `func New(interval time.Duration, checks ...Check) *Checker`
  - `func (c *Checker) Start(ctx context.Context)`: runs every check once immediately, then every `interval`
  - `func (c *Checker) Ready() (ok bool, failing map[string]string)`
  - `func (c *Checker) Handler() http.Handler`: `GET /livez` → 200 `{"status":"live"}`; `GET /readyz` → 200 `{"status":"ready"}` or 503 `{"status":"unready","failing":{name:msg}}`
  - `func HatchetWorker(list func(ctx context.Context) ([]rest.Worker, error), name string, maxAge time.Duration, now func() time.Time) Check`
  - `func DockerPing(ping func(ctx context.Context) error) Check`
  - `func WaitReady(ctx context.Context, c *Checker, grace time.Duration) error`
- Worker constants: `healthInterval = 5 * time.Second`, `heartbeatMaxAge = 12 * time.Second` (3 × the SDK's 4 s heartbeat, `pkg/client/action_listener.go`), `startGrace = 2 * time.Minute`. The address comes from `SATAT_HEALTH_ADDR`, default `127.0.0.1:8001`.

- [ ] **Step 1: Write the failing tests** in `health/health_test.go`:
  - `TestReadyOnlyWhenEveryCheckPasses`: two checks, one failing → `Ready()` false, `failing["b"] == "down"`. Both pass → true and empty.
  - `TestLivezIgnoresChecks`: a failing check → `/livez` is 200 and `/readyz` is 503, with body `failing.b == "down"`.
  - `TestHatchetWorkerNeedsOwnFreshActiveRecord`: with `now = t0`, these cases (`maxAge 12s`, name `satat-worker-abc`):
    - own name, `ACTIVE`, heartbeat at `t0-5s` → nil
    - own name, `ACTIVE`, heartbeat at `t0-13s` → error containing `heartbeat`
    - own name, `INACTIVE` → error containing `INACTIVE`
    - only `satat-worker-old`, `ACTIVE` and fresh → error containing `not registered`
    - list returns an error → that error wrapped
  - `TestWaitReadyFailsAfterGrace`: an always-failing check, grace 50 ms → error naming the failing check. A check that passes on the second run → nil.
- [ ] **Step 2: Run** `go test ./health/ -run . -v` and expect FAIL: `undefined: New`.
- [ ] **Step 3: Implement** `health/*.go`. Store results under a mutex. `Ready()` is false before the first run finishes.
- [ ] **Step 4: Run** `go test ./health/ -v` and expect PASS.
- [ ] **Step 5: Wire up the worker** in `cmd/satat-worker/main.go`:
  - The worker name is `"satat-worker-" + os.Hostname()`.
  - `HatchetWorker` uses `hc.Workers().List(ctx)` and takes `.Rows`.
  - `DockerPing` uses `api.Ping`.
  - Serve `checker.Handler()` on `SATAT_HEALTH_ADDR` in a goroutine, `checker.Start(ctx)`, then run `w.StartBlocking(ctx)` in a goroutine. Main waits on `WaitReady(ctx, checker, startGrace)` and returns its error, so the process exits 1 if it's never ready. After that it blocks on the worker's result.
  - `os.Args[1] == "healthcheck"` → GET `http://$SATAT_HEALTH_ADDR/readyz` with a 3 s timeout, print the body to stdout, exit 0 on 200 and 1 otherwise. This runs before any config or env checks.
- [ ] **Step 6: Edit `compose.yaml`.** Add `HATCHET_CLIENT_SERVER_URL: http://hatchet-lite:8888` to the worker environment, so the REST client doesn't use the token's `localhost:8888`. Add `healthcheck: { test: ["CMD", "/usr/local/bin/satat-worker", "healthcheck"], interval: 10s, timeout: 5s, retries: 2, start_period: 2m }`.
- [ ] **Step 7: Run** `go test -p 1 ./... -count=1` and `scripts/check-compose-socket.sh`. Expect PASS.
- [ ] **Step 8: Manual check** (needs the stack and secrets):
  1. `scripts/compose-up.sh`, then `docker compose ps worker` shows `healthy`.
  2. `docker compose stop hatchet-lite`. Within 30 s, `docker compose exec -T worker /usr/local/bin/satat-worker healthcheck` prints `unready` with `hatchet`, and `docker inspect -f '{{.RestartCount}}' satat-worker-1` is unchanged.
  3. `docker compose start hatchet-lite` → ready again.

  Record the result in the PR body.
- [ ] **Step 9: Commit** `Add worker liveness and readiness with a Hatchet check`.

### Task 2: `deploy` section in config

**Files:**
- Modify: `config/config.go`, `satat.yaml`
- Test: `config/config_test.go`

**Interfaces:**
- Produces:
  - `type Deploy struct { Target string; Secrets map[string]string }` on `config.Config` as the field `Deploy`.
  - `const SecretFromEnv = "env"`.
  - `var WorkerSecrets = []string{"SATAT_PUSH_TOKEN"}`.
  - `func (c Config) SecretNames() []string`: sorted, unique: every lane's `PassEnv` plus `WorkerSecrets`.
- YAML: `deploy: { target: compose, secrets: { NAME: "op://..." | "env" } }`.
- Rules:
  - The `deploy` section is optional, so existing test configs stay valid. When it's absent, `Target == "compose"` and `Secrets` is nil.
  - When it's present:
    - `target` must be `compose`;
    - every name in `SecretNames()` must have an entry;
    - each value must be `env` or start with `op://`;
    - an entry for a name no lane or worker uses is an error.

- [ ] **Step 1: Write failing tests:**
  - `TestDeployDefaultsWhenAbsent`
  - `TestDeployRefusesUnknownTarget` (`kubernetes` → error containing `target`)
  - `TestDeployNeedsEverySecretMapped`: lane `claude` with `pass_env: [CLAUDE_CODE_OAUTH_TOKEN]`, no entry → error containing `CLAUDE_CODE_OAUTH_TOKEN` and `claude`; missing `SATAT_PUSH_TOKEN` → error containing `SATAT_PUSH_TOKEN` and `worker`
  - `TestDeployRefusesBadSecretRef` (`"plain-value"` → error containing `op://`)
  - `TestDeployRefusesUnusedSecret`
- [ ] **Step 2: Run** `go test ./config/ -v` and expect FAIL.
- [ ] **Step 3: Implement** the parsing and validation in `config.go`, using a `deployFile` with `KnownFields` still on.
- [ ] **Step 4: Add the `deploy` section to `satat.yaml`,** with the three `op://Satat Harness/...` entries from the spec, decision 2.
- [ ] **Step 5: Run** `go test -p 1 ./... -count=1`. Expect PASS, including `TestRepoConfigLoadsAndPassesTheFloor`.
- [ ] **Step 6: Commit** `Add deploy target and secret map to satat.yaml`.

### Task 3: `Target` interface and Compose render

**Files:**
- Create: `deploy/deploy.go`, `deploy/compose/compose.go`, `deploy/compose/compose.yaml.tmpl`, `cmd/satat/main.go`, `cmd/satat/deploy.go`
- Test: `deploy/compose/render_test.go`
- Modify: `compose.yaml` (now generated), `.github/workflows/go.yml`

**Interfaces:**
- Produces in `deploy`:
  ```go
  type Secrets map[string]string
  type ServiceStatus struct { Name, State string; Healthy bool; Detail string }
  type Status struct { Services []ServiceStatus }
  type Target interface {
      Render(cfg config.Config) (map[string][]byte, error)
      Up(ctx context.Context, cfg config.Config, secrets Secrets) error
      Down(ctx context.Context, removeData bool) error
      Status(ctx context.Context) (Status, error)
  }
  ```
- Produces in `deploy/compose`: `type Target struct { Dir string; Run Runner; Out io.Writer }`. `Render` returns `{"compose.yaml": ...}`. `Up`, `Down` and `Status` return `errors.ErrUnsupported` until Task 5.
- Template data: `WorkerEnv []string`, the sorted `cfg.SecretNames()`. Each becomes `NAME: ${NAME:?run satat deploy up}`. Everything else in the template is today's `compose.yaml` (after Task 1) verbatim, with a first line `# Generated by satat deploy render from deploy/compose/compose.yaml.tmpl and satat.yaml. Do not edit.`
- CLI: `satat deploy render [--config satat.yaml] [--check]`. It writes into the config file's directory. `--check` exits 1 with `compose.yaml is out of date: run satat deploy render` on a difference.

- [ ] **Step 1: Write failing tests:**
  - `TestRenderMatchesCheckedInCompose`: render `../../satat.yaml` and expect it byte-equal to `../../compose.yaml`.
  - `TestRenderAddsEveryPassEnvToTheWorker`: a config with lane `x` `pass_env: [FOO]` → output contains `FOO: ${FOO:?run satat deploy up}` under `worker.environment`. Parse it with `yaml.v3` and assert on `services.worker.environment.FOO`.
  - `TestRenderMountsTheSocketOnlyInTheProxy`: parse the output and expect only `docker-proxy` to have a volume containing `docker.sock`.
- [ ] **Step 2: Run** `go test ./deploy/... -v` and expect FAIL.
- [ ] **Step 3: Implement** `deploy.go`, the template (`//go:embed compose.yaml.tmpl`), `Render`, and `cmd/satat`, with subcommand dispatch on `os.Args[1:]` (`deploy`, `agent`) and `flag.NewFlagSet` per subcommand.
- [ ] **Step 4: Run** `go run ./cmd/satat deploy render`, then `git diff compose.yaml`. Expect only the header line and the reordered worker env, with the same set of variables.
- [ ] **Step 5: Run** `go test -p 1 ./... -count=1` and `scripts/check-compose-socket.sh`. Expect PASS.
- [ ] **Step 6: Add a CI step** in `.github/workflows/go.yml` after `go vet`: `- run: go run ./cmd/satat deploy render --check`. Add `satat.yaml` and `deploy/**` to both `paths` lists.
- [ ] **Step 7: Commit** `Add deploy target interface and Compose render`.

### Task 4: Secrets and `.env`

**Files:**
- Create: `deploy/secrets.go`, `deploy/envfile.go`
- Test: `deploy/secrets_test.go`, `deploy/envfile_test.go`

**Interfaces:**
- Produces:
  - `func Resolve(ctx context.Context, refs map[string]string, read func(ctx context.Context, ref string) (string, error), getenv func(string) (string, bool)) (Secrets, error)`
  - `func OpRead(ctx context.Context, ref string) (string, error)`: runs `op read --no-newline <ref>`
  - `func EnsureKey(path, key string, gen func() (string, error)) (created bool, err error)`
  - `func ReadKey(path, key string) (string, bool, error)`
  - `func RandomPassword() (string, error)`: 32 bytes from `crypto/rand`, base64url, no padding

- [ ] **Step 1: Write failing tests:**
  - `TestResolveUsesOpAndEnv`
  - `TestResolveNamesTheMissingEnv`: `env` ref unset → error containing the name, never a value.
  - `TestResolveErrorHidesValues`: the `read` error includes a fake secret, and the returned error doesn't.
  - `TestEnsureKeyCreatesOnceAndKeepsMode0600`: the first call creates, the second returns `created=false` with the same value, and the file mode is `0600`.
  - `TestEnsureKeyKeepsOtherLines`
- [ ] **Step 2: Run** `go test ./deploy/ -v` and expect FAIL.
- [ ] **Step 3: Implement.** `EnsureKey` writes through a temp file plus rename, like `hatchet-token.sh`.
- [ ] **Step 4: Run** `go test ./deploy/ -v` and expect PASS.
- [ ] **Step 5: Commit** `Add secret resolution and env file handling for deploy`.

### Task 5: `satat deploy up | down | status`

**Files:**
- Modify: `deploy/compose/compose.go`, `cmd/satat/deploy.go`
- Test: `deploy/compose/up_test.go`

**Interfaces:**
- Consumes: Task 3 `Target`, Task 4 `Resolve`, `OpRead`, `EnsureKey`, `ReadKey`, `RandomPassword`, `docker.New`, and `(*docker.Launcher).Reap(ctx, 0)`.
- Produces:
  - `type Runner interface { Run(ctx context.Context, env []string, name string, args ...string) ([]byte, error) }`
  - `type ExecRunner struct{}`
- `Up` does the spec's decision 3 steps 1–6 in order. Waiting polls `docker compose ps --format json` every 2 s. Timeouts: platform 3 min, worker 3 min. A worker timeout runs `exec -T worker /usr/local/bin/satat-worker healthcheck` and includes its JSON in the error. Hatchet token creation is `hatchet-token.sh`'s SQL lookup plus `hatchet-admin token create`, run through `Runner`.
- `Down(removeData)`: `docker compose down` plus `-v` when `removeData`, then `Reap(ctx, 0)` with a host Docker client (`client.FromEnv`).
- `Status`: services from `ps --format json`. The worker `Detail` is the healthcheck JSON.
- CLI: `satat deploy up|down [--volumes]|status [--config satat.yaml]`. `up` resolves `cfg.Deploy.Secrets` with `OpRead` and `os.LookupEnv`, and passes them to `Up`.

- [ ] **Step 1: Write failing tests** with a fake `Runner` that records calls and returns scripted `ps` JSON:
  - `TestUpRunsInOrder`: calls go `compose up -d postgres hatchet-lite docker-proxy` → `ps` (healthy) → `exec ... psql` → `exec ... hatchet-admin` → `compose up -d --build worker` → `ps` (worker healthy). `.env` gets both keys.
  - `TestUpIsIdempotent`: `.env` already has both keys and every service is healthy → no `hatchet-admin` call, the password is unchanged, and no `compose up` is issued for already-healthy services.
  - `TestUpRefusesStaleCompose`: the checked-in `compose.yaml` differs → error `out of date`, and no runner calls.
  - `TestUpSecretsOnlyInChildEnv`: the secret value appears in the `env` passed to the worker `compose up`, and not in any args, not in `.env`, and not in `Out`.
  - `TestUpTimeoutReportsFailingCheck`: the worker is never healthy → the error contains the healthcheck JSON's `failing` keys.
  - `TestDownPassesVolumesFlag`
- [ ] **Step 2: Run** `go test ./deploy/compose/ -v` and expect FAIL.
- [ ] **Step 3: Implement** `Up`, `Down`, `Status`, `ExecRunner` and the CLI.
- [ ] **Step 4: Run** `go test -p 1 ./... -count=1` and expect PASS.
- [ ] **Step 5: Manual check:**
  1. `docker compose down -v`, then delete `.env`.
  2. `go run ./cmd/satat deploy up` → ends with every service healthy.
  3. Run `deploy up` again → no change and no new token.
  4. `deploy status` → everything healthy.
  5. `deploy down` → `docker ps -a --filter label=satat.managed=true` is empty.

  Record the result in the PR.
- [ ] **Step 6: Commit** `Add satat deploy up, down and status for Compose`.

### Task 6: Agent runs on the existing workflow

**Files:**
- Modify: `workflows/issue.go`, `cmd/satat-worker/main.go`, `satat.yaml`
- Create: `workflows/agent.go`
- Test: `workflows/agent_test.go`, `launcher/docker/floor_test.go`

**Interfaces:**
- Produces in `workflows`:
  - `const EventAgent = "satat:agent"`, `const KindIssue = "issue"`, `const KindAgent = "agent"`, `const AdhocRepo = "adhoc"`, `const TailBytes = 4096`
  - `Input` gains `Kind string \`json:"kind,omitempty"\`` (empty means issue) and `StepInput json.RawMessage \`json:"step_input,omitempty"\``.
  - `Output` gains `Kind string`, `Stdout, Stderr string \`json:",omitempty"\``, `Files map[string]int \`json:"files,omitempty"\``, `TimedOut, OOMKilled, Cancelled bool`.
  - `func MapAgent(cfg config.Config, lane string, stepInput []byte) (Input, error)`
  - `Handler` gains `Secrets []string`, the values to scrub.
  - `func scrubTail(b []byte, max int, secrets []string) string`
- `Define` sets `Events: []string{EventIssue, EventAgent}`.
- `Handler.Run` dispatches on `in.Kind`:
  - `agent` → `Launcher.Launch(ctx, launcher.Step{Run: runID, Name: "agent", Lane: in.Lane, Input: in.StepInput})`. It never calls `Pusher`.
  - Status is one of `ok`, `failed`, `timed_out`, `oom`, `cancelled`.
  - A non-zero exit returns the `Output` with a nil error, so `--wait` can show it. Only a launch error returns an error.
- Worker: `Handler.Secrets` holds the non-empty values of `cfg.SecretNames()` from its environment.
- `satat.yaml`: lane `sleep` with the `smoke` image, `command: ["sh","-c","sleep 600"]`, `memory: 64MiB`, `cpus: 0.25`, `pids: 16`, `workspace: 8MiB`, `timeout: 15m`, `internal: true`. Re-run `satat deploy render`, since no `pass_env` means no change expected.

- [ ] **Step 1: Write failing tests** in `workflows/agent_test.go`, with a fake launcher and a fake pusher:
  - `TestDefineListensToBothEvents`
  - `TestMapAgentUsesAdhocRepoAndLaneCaps`: `Repo == "adhoc"`, `Account` and `LaneCap` come from config, and an unknown lane is an error.
  - `TestAgentRunNeverPushes`
  - `TestAgentRunReportsTailAndFiles`: stdout of 10 000 bytes → `len(Stdout) == 4096`, ending in the last bytes. `Files` is `{"a.txt": 3}`.
  - `TestScrubBeforeTail` (Review Focus 1): a secret of 20 bytes sitting across byte offset `len-4096` → the output contains neither the secret nor any 8-byte substring of it.
  - `TestAgentNonZeroExitIsAResult`: exit 2 → `Status "failed"`, `ExitCode 2`, err nil.
  - `TestEmptyKindIsIssue`
  - In `launcher/docker/floor_test.go`: `TestCreateRequestHasNoComposeLabels`. Run `buildRequest` and expect no label key with prefix `com.docker.compose.`, and `satat.managed == "true"`.
- [ ] **Step 2: Run** `go test ./workflows/ ./launcher/docker/ -run 'Agent|Scrub|Kind|Define|Compose' -v` and expect FAIL.
- [ ] **Step 3: Implement** `agent.go`, the `issue.go` changes, the worker's `Secrets`, and the `sleep` lane. In `scrubTail`, replace every secret in the full buffer first, then take the last `max` bytes.
- [ ] **Step 4: Run** `go test -p 1 ./... -count=1` and `go run ./cmd/satat deploy render --check`. Expect PASS.
- [ ] **Step 5: Commit** `Add on-demand agent runs to the issue workflow`.

### Task 7: `satat agent run | ls | rm`

**Files:**
- Create: `cmd/satat/agent.go`
- Test: `cmd/satat/agent_test.go`

**Interfaces:**
- Consumes: `workflows.MapAgent`, `workflows.EventAgent`, `workflows.Output`, `hc.Events().Push`, `hc.Runs().List`, `hc.Runs().Get`, `hc.Runs().Cancel`.
- Produces: `type hatchetAPI interface { Push(ctx context.Context, event string, in any) error; List(ctx context.Context) ([]RunRow, error); Get(ctx context.Context, id string) (RunRow, *workflows.Output, error); Cancel(ctx context.Context, id string) error }`, with a real implementation over the SDK and a fake one in tests. `type RunRow struct { ID, Kind, Lane, State string; Started time.Time }`.
- `agent run <lane> [--input file.json] [--wait] [--config satat.yaml]`:
  - Prints `queued <lane>: run lookup pending` and then the run ID, once `List` finds the newest run with input `kind=agent` and that lane, created after the push. Polling: every 1 s, for up to 30 s.
  - `--wait` polls `Get` every 2 s until it reaches a terminal state, prints the `Output` (status, exit, files, then the stdout and stderr tails), and exits with the agent's exit code, or 1 when it was cancelled or timed out.
- `agent ls`: a table of `ID KIND LANE STATE AGE` for runs in `QUEUED` or `RUNNING`.
- `agent rm <run>`:
  - unknown ID → exit 1 with `no such run`;
  - a terminal state → exit 0 with `not running (<state>)`;
  - otherwise `Cancel`, then print `cancelled`.

- [ ] **Step 1: Write failing tests** with the fake `hatchetAPI`:
  - `TestRunPushesAgentEvent`: the pushed input has `kind=agent`, `repo=adhoc` and the file's `step_input`.
  - `TestRunWaitExitCode`
  - `TestLsShowsOnlyActive`
  - `TestRmUnknownFinishedAndRunning` (Review Focus 3)
- [ ] **Step 2: Run** `go test ./cmd/satat/ -v` and expect FAIL.
- [ ] **Step 3: Implement.** The `List` filter uses `rest.V1WorkflowRunListParams` with `Since` set to 24 h ago and the workflow name `satat-issue`. Kind and lane are read from each run's input.
- [ ] **Step 4: Run** `go test -p 1 ./... -count=1` and expect PASS.
- [ ] **Step 5: Manual acceptance** (stack up via `deploy up`):
  1. `satat agent run smoke --wait` → `ok`, exit 0, and no `satat.*` container left.
  2. `satat agent run sleep`, then `agent ls` shows it `RUNNING`; `agent rm <id>` → `cancelled`, and within 15 s no container is left.
  3. With `limits.global: 2`, three `agent run sleep` → `agent ls` shows two `RUNNING` and one `QUEUED`. Then `rm` all three.
  4. While a `sleep` agent runs, `docker compose up -d --remove-orphans` → the agent container is still there.
  5. `satat agent run claude --input <small input>` and `satat-enqueue` for the test issue at the same time → `agent ls` shows one of them `QUEUED` until the other ends (shared lane and account caps).

  Record the result in the PR.
- [ ] **Step 6: Commit** `Add satat agent run, ls and rm`.

### Task 8: Registry references for lane images

**Files:**
- Modify: `scripts/build-claude-image.sh`

**Interfaces:**
- `SATAT_REGISTRY` is required, for example `localhost:5000` or `ghcr.io/javatarz`.
- The script builds `$SATAT_REGISTRY/satat-claude:dev` and pushes it. It reads the digest with `docker buildx imagetools inspect "$ref" --format '{{json .Manifest}}' | jq -r .digest` and prints `image: $SATAT_REGISTRY/satat-claude@<digest>`.

- [ ] **Step 1: Edit the script.** Fail with `set SATAT_REGISTRY` when it's unset, and keep `shellcheck` clean.
- [ ] **Step 2: Run** `pre-commit run shellcheck --files scripts/build-claude-image.sh` and expect PASS.
- [ ] **Step 3: Operator step, not the agent:** run the script against a registry, paste the printed reference into `satat.yaml`'s `claude` lane, and run `satat agent run smoke --wait` plus one `claude` issue run. Until then, `satat.yaml` keeps today's pin.
- [ ] **Step 4: Commit** `Update Claude image build to push and print a registry digest`.

### Task 9: Docs and measurements

**Files:**
- Modify: `docs/design.md` ("Run it locally" becomes `satat deploy up` plus `satat agent run smoke --wait`; "What is not built" loses the rows Tasks 1–7 built and gains "k3d adapter and Kubernetes launcher"), `docs/research/v2/docker-prototype.md` (a new "Deploy adapter" section with `deploy up` cold and warm times, idle RAM with the health server, and the time to unready after stopping hatchet-lite), and the "Not verified" lists in ADRs 0028–0030.

- [ ] **Step 1: Update the docs** with the numbers recorded in Tasks 1, 5 and 7.
- [ ] **Step 2: Run** `pre-commit run --all-files` and expect PASS.
- [ ] **Step 3: Commit** `Docs deploy adapter usage and measurements`.
