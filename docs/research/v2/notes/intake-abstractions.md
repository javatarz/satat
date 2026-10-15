# Research: intake abstractions (forge, tracker, alert, envelope)

Date: 2026-10-15. Scope: code- and spec-level abstractions that hide GitHub/GitLab/alert-source differences from Satat's intake (requirements: NN8 triggers, NN9 every dependency a plugin, NN11 license allows a public recipe; open questions 3 tracker scope and 7 extension language). Facts were checked against repos with `gh api` (licence, latest release, last push, source files) and against vendor docs. Claims I could not confirm are marked UNVERIFIED.

## Summary table

| Candidate | Kind | Lang | Providers | Webhook parse (normalised) | Signature verify | API: issue / PR / comment / review | SPDX | Latest release | Activity | Fit |
|---|---|---|---|---|---|---|---|---|---|---|
| **jenkins-x/go-scm** | Forge lib | Go | GitHub, GitLab, Gitea, Bitbucket Cloud, Bitbucket Server (stash), Azure, Gogs, fake | GitHub: push, PR, **issues**, issue_comment, **label**, **pull_request_review**, review_comment, installation, check, status, release... GitLab: push, MR, note, release (**Issue Hook returns UnknownWebhook**; MR approve maps to generic `update`). Gitea: issues, comments, PR, review approved/rejected/comment | GitHub HMAC (reads `X-Hub-Signature`, sha1 or sha256 by prefix); GitLab `X-Gitlab-Token` constant-time compare; Gitea HMAC-SHA256 | Yes / Yes / Yes / Yes on GitHub and Gitea; **GitLab ReviewService returns ErrNotSupported** | BSD-3-Clause | v1.17.1 (2026-10-12) | 71 commits since 2026-04; powers Lighthouse | **Best Go option**, with gaps to patch |
| **drone/go-scm** (Harness) | Forge lib | Go | GitHub, GitLab, Gitea, Gitee, Gogs, Bitbucket, Stash, Azure, Harness | GitHub: push, create/delete, PR, issue_comment, pull_request_review, check_run, status... (**`issues` event commented out**). GitLab: push, MR, note, pipeline (**Issue Hook returns ErrUnknownEvent**) | GitHub `X-Hub-Signature-256` (falls back to sha1); GitLab token compare (not constant time); Gitea HMAC | Issues: create, comment, close. PRs: create, comment, merge. Reviews: Find/List/Create/Delete only | BSD-2-Clause-Patent (GitHub shows NOASSERTION) | v1.42.20 (2026-10-15) | 31 commits since 2026-04 | CI-oriented; weaker on issues and labels than jx fork |
| yi-nology/git-platform-sdk | Forge lib | Go | GitHub, GitLab, Gitea, Forgejo, Gitee, GitCode, Tencent Code | Claims a normalised vocabulary (`cr.*`, `push`, `issue.*`, `comment.*`) with golden fixtures | Claims HMAC-SHA256/SHA1/GitLab token, constant time | Claims issues, CRs, reviews | MIT | v0.83.0 (2026-10-13) | Created 2026-05, 0 stars, 2 contributors | Too young to depend on; worth reading as a design reference. Capability claims UNVERIFIED |
| git-pkgs/forge | Forge lib + CLI | Go | GitHub, GitLab, Gitea/Forgejo, Bitbucket Cloud (also Gerrit, Tangled dirs) | **None** | n/a | Issues, PRs, reviews (approve, request), labels, comments | MIT | v0.10.1 (2026-10-01) | Active, 223 stars | API-side only |
| fluxcd/go-git-providers | Forge lib | Go | GitHub, GitLab, Bitbucket Server | None | n/a | Repos, deploy keys, PRs; no issue or review coverage (UNVERIFIED in detail) | Apache-2.0 | v0.29.0 (2026-06-30) | Low | Not a fit |
| **packit/ogr** | Forge lib | Python | GitHub, GitLab, Forgejo, Pagure | **None** (packit-service parses webhooks itself) | n/a | Issues, PRs, comments, labels, flags; reviews UNVERIFIED | MIT | 0.63.0 (2026-09-04) | 44 commits since 2026-04, Red Hat team | **Best Python API-side option** |
| phil65/githarbor | Forge lib | Python | GitHub, GitLab, Azure, Gitea, Codeberg, Bitbucket | None | n/a | Read-mostly (list/get PRs, issues) | MIT | v1.0.0 (2025-10-07) | Last push 2025-12, 2 stars | Not a fit |
| oritwoen/gixa | Forge lib | TS | GitHub, GitLab, Gitea/Forgejo, GitBucket | **None** (README: "does not handle webhooks") | n/a | Repos, issues, PRs, users (list/get/create); no comments or reviews | MIT | v0.5.1 (2026-10-14) | 10 stars, MVP | Not a fit |
| capawesome-team/repo-sdk | Forge lib | TS | GitHub (incl. App auth), GitLab, Bitbucket, Azure, Gitea/Forgejo | Verify + parse, but only repo/ref events (push/tag); no issue/PR namespaces | Per-provider `verifyWebhook` | **No issues, PRs, comments, reviews** | MIT | v0.3.4 (2026-10-14) | Created 2026-07, 1 star | Not a fit |
| Renovate platform layer | Internal module | TS | GitHub, GitLab, Bitbucket (+Server), Gitea, Forgejo, Azure, CodeCommit, Gerrit, SCM-Manager | None (Renovate is not webhook-driven) | n/a | Issues, PRs, comments (Renovate's own needs) | AGPL-3.0 | 44.164.5 (2026-10-15) | Very active | Not published as a library; AGPL copy-in would bind Satat's code. Reference only |
| Backstage `@backstage/integration` | Config + auth lib | TS | GitHub, GitLab, Bitbucket Cloud/Server, Gitea, Gerrit, Azure, Harness, ... | None | n/a | URL reading and **credential providers** (GitHub App installation tokens via `DefaultGithubCredentialsProvider`); no issue/PR CRUD | Apache-2.0 | v1.55.3 (2026-09-29) | Very active | Useful only for GitHub App token minting in TS |
| Single-provider SDKs | Provider libs | Go / Py / TS | One each | go-github `ValidatePayload`+`ParseWebHook`; GitLab client-go `HookEventType`+`ParseWebhook`; githubkit `webhooks.verify/parse`; `@octokit/webhooks` | Yes (GitHub ones); GitLab token is a header compare you write | Full | go-github BSD-3-Clause; client-go Apache-2.0; githubkit MIT; octokit MIT; python-gitlab LGPL-3.0; PyGithub LGPL-3.0; gitbeaker MIT | go-github v92.0.0 (2026-09-14); client-go v3.23.0 (2026-10-14); githubkit v0.16.1 (2026-08-14) | Very active | The realistic base in Python/TS |
| Merge.dev Ticketing, Unified.to Ticketing, Apideck Issue Tracking | Managed unified API | REST | GitHub Issues, GitLab, Jira, Linear, ... (Merge lists Linear and Jira as beta on some pages) | Vendor webhooks | Vendor | Tickets + comments; **no PR/MR or review model** | Proprietary SaaS | n/a | n/a | **Fails NN11** (managed control plane) |
| Nango | Integration platform | TS | 400+ APIs incl. GitHub, GitLab, Linear, Jira | Webhook forwarding | Yes | You write the sync scripts | **Elastic-2.0** (not OSI) | n/a | Very active | Fails NN11 if OQ4 requires OSI; still you write per-provider code |
| **CloudEvents** | Envelope spec | Spec + SDKs (Go, Py, JS/TS, Java, C#, Rust, Ruby, PHP, Kotlin, PowerShell) | Adapter docs for GitHub and GitLab (envelope only; `data` = raw body) | n/a | n/a | n/a | Apache-2.0 | Spec v1.0.2 (2022); CNCF graduated 2024-01-25. sdk-go v2.16.2, sdk-python 2.2.0 (2026-06-11), sdk-javascript v10.0.0 | Active | **Use as envelope** |
| CDEvents | Event vocabulary on CloudEvents | Spec + Go/Rust SDKs (Python SDK dead since 2023, Java stale) | Defines `change.created/reviewed/merged`, `ticket.created/updated/closed`, `incident.detected/reported/resolved` | n/a | n/a | n/a | Apache-2.0 | spec v0.5.1 (2026-04-15) | Moderate | Borrow names; `change.reviewed` has **no review state**, so not sufficient |
| Alertmanager webhook v4 | De facto alert payload | Format | Alertmanager; **Grafana's default webhook payload is a superset** (`version: "1"`, adds `orgId`, `state`, `title`, `message`, `values`) | n/a | AM: none (use `http_config` bearer/basic); Grafana: HMAC-SHA256 hex, optional timestamp | n/a | Apache-2.0 | AM v0.34.1 (2026-09-17) | Very active | **Use as the alert schema** |
| Keep (keephq) | Alert platform with 130+ providers | Python | Grafana, Alertmanager, Sentry, Datadog, PagerDuty, ... → `AlertDto` | Yes, per provider | Per provider | n/a | MIT outside `ee/` | v0.54.3 (2026-09-09) | Acquired by Elastic 2025-05; 39 commits since 2026-07 | A whole platform (DB, UI); mine `format_alert` mappings, don't deploy for this |

## 1. Forge abstraction libraries

### What a library needs to do for Satat

Triggers: issue labelled, PR review submitted, comment created (issue and PR), plus push for some workflows. Actions: create issue, comment, open PR/MR, read reviews and review comments, add or remove labels. Plus verify signatures.

### Go: jenkins-x/go-scm is the closest fit, with three gaps

- Repo: https://github.com/jenkins-x/go-scm. BSD-3-Clause. v1.17.1 released 2026-10-12. Used by Jenkins X Lighthouse (https://github.com/jenkins-x/lighthouse, v2.1.0 2026-10-14), which is a Prow-style ChatOps bot, so the library is shaped around exactly the issue/PR/comment/label/review loop Satat needs.
- Drivers: azure, bitbucket, gitea, github, gitlab, gogs, stash, fake (`scm/driver/`). The `fake` driver is an in-memory implementation, which helps NN9's "local implementation" for tests.
- Normalised webhook kinds (`scm/webhook.go`): branch, check_run, check_suite, deploy, deployment_status, fork, installation, installation_repository, issue, issue_comment, label, ping, pull_request, pull_request_comment, push, release, repository, review, review_comment, star, status, tag, watch. GitHub PR actions are normalised (labeled, unlabeled, opened, synchronize, ready_for_review, review_requested, ...).
- API surface: `IssueService` (Find, List, Search, Create, CreateComment, EditComment, Close, Reopen, AddLabel, DeleteLabel, Assign...), `PullRequestService` (Create, Update, Merge, ListChanges, ListComments, AddLabel, RequestReview...), `ReviewService` (Find, List, Create, ListComments, Submit, Dismiss; states APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED, PENDING), `AppService.CreateInstallationToken` for GitHub Apps.
- Signature verification: GitHub HMAC via `pkg/hmac.ValidatePrefix` (sha1 or sha256 chosen by the `sha1=`/`sha256=` prefix) but it reads only the `X-Hub-Signature` header, i.e. the legacy SHA-1 one (`scm/driver/github/webhook.go`). GitLab compares `X-Gitlab-Token` in constant time. Gitea uses HMAC-SHA256 on `X-Gitea-Signature`. If `SecretFunc` returns an empty key, validation is skipped (fail-open). Setting `GO_SCM_LOG_WEBHOOKS=true` logs the HMAC key.
- **Gaps found in source:**
  1. GitLab `Issue Hook` returns `scm.UnknownWebhook` (`scm/driver/gitlab/webhook.go`). "Issue labelled" on GitLab is **not normalised**.
  2. GitLab `Merge Request Hook` actions `approved`/`unapproved` fall through to `ActionUpdate`. There is no review event for GitLab.
  3. GitLab `ReviewService` methods all return `scm.ErrNotSupported` (`scm/driver/gitlab/review.go`). GitLab has no "review with state" object. Approvals and discussions are separate APIs.

### Go: drone/go-scm (Harness)

- Repo: https://github.com/drone/go-scm. LICENSE text is BSD-2-Clause-Patent (GitHub reports NOASSERTION). v1.42.20 released 2026-10-15. Drivers: azure, bitbucket, gitea, gitee, github, gitlab, gogs, harness, stash.
- Built for CI triggers. The GitHub driver has `// case "issues":` commented out, so it never parses GitHub issue events. GitLab `Issue Hook` returns `ErrUnknownEvent`. `ReviewService` is only Find/List/Create/Delete. It prefers `X-Hub-Signature-256`, and its GitLab token compare is a plain `!=`.
- Verdict: weaker than the jx fork for an issue-driven workflow.

### Python

- No OSS Python library normalises **webhooks** across forges. packit/ogr (https://github.com/packit/ogr, MIT, 0.63.0 2026-09-04; services: github, gitlab, forgejo, pagure) is the best **API-side** abstraction: issues, PRs, comments, labels and commit flags behind `ogr.abstract`. Packit parses webhooks inside packit-service, not in ogr. ogr's review coverage is UNVERIFIED.
- Webhooks: use githubkit (MIT, v0.16.1; `githubkit.webhooks.verify` / `parse` with Pydantic models) for GitHub, and hand-rolled Pydantic models for GitLab's `X-Gitlab-Event` payloads. python-gitlab (LGPL-3.0, v8.6.0) has no webhook models (UNVERIFIED, none found).
- Also seen, not recommended: githarbor (dormant), gfo (0BSD, a CLI, 0 stars).

### TypeScript

- No TS library normalises issue/PR/review webhooks across forges. gixa says it does not handle webhooks. repo-sdk verifies and parses webhooks but only for repo/ref events and has no issues or PRs. Renovate's `lib/modules/platform` covers 11 platforms but is AGPL-3.0 internal code, not a package. Backstage `@backstage/integration` (Apache-2.0) is useful only for GitHub App installation-token minting and host config.
- Webhooks: `@octokit/webhooks` (MIT, typed, verify+route) for GitHub. GitLab has gitbeaker (MIT, v43.8.0 2025-11-01, API only). GitLab webhook types are hand-written.

### Managed unified APIs (Merge, Unified.to, Apideck)

All three are hosted SaaS. That fails NN11 ("no managed control plane we cannot run ourselves"). They also model **tickets**, not PR/MR/review, so they would cover only the tracker half. Sources: https://www.merge.dev/integrations/gitlab, https://unified.to/ticketing, https://www.apideck.com/issue-tracking-api.

## 2. Tracker abstraction (open question 3)

- No OSS library normalises issues across GitHub Issues, GitLab Issues, Jira and Linear. The managed ones are Merge, Unified.to and Apideck. Nango is Elastic-2.0 and you still write the per-provider sync scripts.
- The closest open **vocabulary** is CDEvents `ticket.created/updated/closed`. Its subject fields are summary, ticketType, group, creator, assignees, priority, labels, milestone, uri (`schemas/ticketcreated.json`). cdviz-dev/transformers-community (Apache-2.0) has VRL transformers from `jira_webhook`, `github_events`, `gitlab_webhook`, `gitea_webhook`, `forgejo_webhook` and `bitbucket_webhook` to CDEvents. These run in the Rust cdviz-collector (0.53.1, 2026-10-08), which is a separate service, and the project is small (2 stars).
- Recommendation: if the tracker becomes pluggable, define a small `Tracker` interface (get item, list by label/state, comment, set labels/state, link PR) with GitHub first. Back it with go-scm's IssueService on forges, and write Jira and Linear adapters directly against their APIs (Linear is GraphQL; Jira workflows use transitions, not a state field). Keep the forge (PR/review) interface separate from the tracker interface. A Jira or Linear item still produces a PR on a forge.

## 3. Alert payload normalisation

| Source | Native format | Can it emit Alertmanager-shaped JSON? | Authenticity |
|---|---|---|---|
| Prometheus Alertmanager | Webhook v4: `version`, `groupKey`, `truncatedAlerts`, `status`, `receiver`, `groupLabels`, `commonLabels`, `commonAnnotations`, `externalURL`, `alerts[]` (status, labels, annotations, startsAt, endsAt, generatorURL, fingerprint) | Native. A `payload` Go-template override exists ("use at your own risk") | No signing. Use `http_config` bearer or basic auth |
| Grafana Alerting | Default webhook payload = Alertmanager shape + `orgId`, `state`, `title`, `message`, per-alert `values`, `silenceURL`, `dashboardURL`; `version: "1"` | **Yes by default** (superset). A Custom Payload template is also available | HMAC-SHA256 hex in `X-Grafana-Alerting-Signature`, optional timestamp header (`HMAC(ts + ":" + body)`) |
| Sentry (integration platform) | `Sentry-Hook-Resource` header (issue, event_alert, metric_alert, error, comment, ...); body `action`, `data`, `actor`, `installation` | No. It needs a translator | HMAC-SHA256 hex in `Sentry-Hook-Signature` |
| Datadog | Webhook integration with a **user-defined JSON payload** using `$ALERT_ID`, `$ALERT_TITLE`, `$ALERT_TRANSITION`, `$ALERT_PRIORITY`, `$ALERT_SCOPE`, ... | **Yes via template**. You can author the payload to match your schema | Custom headers only (shared secret header). Signing UNVERIFIED |
| PagerDuty | Webhooks v3, one event per delivery (`incident.triggered`, ...) | No. It needs a translator | `X-PagerDuty-Signature: v1=<hex>[,v1=...]` HMAC-SHA256. go-pagerduty `webhookv3.VerifySignature` implements it |

- **OpenTelemetry has no alert payload standard.** Alert semantic conventions are an open proposal (semantic-conventions#1081) and alerts-as-events is an open spec issue (opentelemetry-specification#4729, 2025-11). Not usable.
- **CDEvents** `incident.detected/reported/resolved` exists but is thin, and its Python SDK is dead.
- **Keep** is the only OSS project that normalises 130+ alert sources (`keep/providers/`, 132 entries) into one `AlertDto` (name, status, severity, lastReceived, environment, service, source, description, url, labels, fingerprint). It is a full platform: API, DB, UI and workflows. Elastic acquired it in May 2025, and the repo is still MIT outside `ee/` and active (v0.54.3, 2026-09-09). Deploying Keep only for normalisation would add a second workflow engine and a stateful service. Its per-provider mapping code is a useful MIT reference.

**Practical recommendation:** make the Alertmanager v4 alert object Satat's canonical alert schema. Grafana and Alertmanager then need no translation (Grafana's extra fields are ignored). Configure Datadog's payload template to emit the same shape. Write two small translators, for Sentry and PagerDuty (~50-100 lines each, plus HMAC verification). Dedupe on `fingerprint` (or `groupKey`), and route on `labels` (`repo`, `service`, `severity`). Ask alert authors to put a `repo` label or annotation on the rule so the alert maps to a repo without a lookup table.

## 4. Event envelope: CloudEvents

- Spec v1.0.2 is the current release (core, JSON format, HTTP binding, webhook spec). CNCF graduated on 2024-01-25. Work-in-progress continues on `main`. Source: https://github.com/cloudevents/spec.
- SDKs: Go (sdk-go v2.16.2), Python (`cloudevents` 2.2.0 on PyPI, 2026-06-11), JS/TS (v10.0.0), Java, C#, Rust, Ruby, PHP, Kotlin, PowerShell. All Apache-2.0.
- Adapters: the spec repo has adapter docs for GitHub and GitLab (also AWS S3/SNS, CouchDB). They map `id` = `X-GitHub-Delivery`, `source` = repository URL, `type` = `com.github.<event>.<action>` or `com.gitlab.merge_request.<action>`, `com.gitlab.note.merge_request`, ..., and `data` = the raw body. So CloudEvents normalises the **envelope** (id for dedupe, type for routing, source, time), not the payload.
- Cost to adapt: trivial. Twenty lines per source at the receiver. None of GitHub, GitLab, Grafana, Sentry, Datadog or PagerDuty emit CloudEvents natively (Datadog could via its template, UNVERIFIED in practice).
- Fit with Stack A: Hatchet's own webhook ingress (https://docs.hatchet.run/v1/webhooks) has a GitHub preset (signature verified) and generic sources with Basic, API-key-header or HMAC (header, algorithm, hex/base64) auth. A CEL expression builds the event key, e.g. `'github:' + headers['x-github-event'] + ':' + input.action`. That would verify GitHub, GitLab (API-key header `X-Gitlab-Token`), Grafana (HMAC hex, no timestamp) and Sentry (HMAC hex) without Satat code. PagerDuty's `v1=` prefix and comma-separated signatures probably do not fit the generic HMAC option (UNVERIFIED). But Hatchet ingress passes the **raw** payload. Normalisation would still have to run as the first Hatchet task, and the trigger contract would be tied to Hatchet, against NN9. A Satat-owned receiver keeps the contract portable.

## Conclusion

### Smallest code Satat writes

1. **One receiver** (`POST /hooks/{source}`). It verifies authenticity per source, wraps the request in a CloudEvent (`id` = delivery id, `source` = forge/repo or alert source, `type` = `satat.<domain>.<verb>`, `subject` = work-item key), dedupes on `id`, and publishes to the queue (Hatchet event).
2. **A normalised event schema** (CloudEvents `data`). It is versioned JSON Schema checked into the repo:
   - `satat.item.labelled` / `unlabelled`: `{forge, repo, kind: issue|pr, number, label, actor, url}`
   - `satat.item.commented`: `{forge, repo, kind, number, comment_id, body, actor, is_bot}`
   - `satat.pr.reviewed`: `{forge, repo, number, review_id?, state: approved|changes_requested|commented, body?, actor}`
   - `satat.pr.updated` / `opened` / `closed` / `merged`, `satat.push`
   - `satat.alert.firing` / `resolved`: Alertmanager v4 alert object + `{source, repo?}`
   - `satat.cron.tick`: `{schedule_id}` (no adapter; emitted by the scheduler)
   - Borrow CDEvents names where they line up, but keep review `state`, which CDEvents lacks.
3. **Adapters.** Inbound: GitHub, GitLab, Alertmanager/Grafana (shared), Sentry, PagerDuty (Datadog via its payload template). Each is a pure function `(headers, body) -> []Event`, tested with recorded fixtures. Outbound: one `Forge` interface (CreateIssue, Comment, OpenPR, ListReviews, ListReviewComments, AddLabel/RemoveLabel, MintToken) that workflows call. Workflows never see provider payloads.

### Library to lean on, per extension language

| Extension language | Inbound webhooks | Outbound forge API | Envelope |
|---|---|---|---|
| **Go** (best supported) | jenkins-x/go-scm `Webhooks.Parse` for GitHub/Gitea. Add your own GitLab `Issue Hook` and MR `approved` parsing (or upstream a PR), and check for `X-Hub-Signature-256`. go-pagerduty `webhookv3` for PagerDuty | jenkins-x/go-scm (GitHub, Gitea, Bitbucket). GitLab approvals and discussions via `gitlab.com/gitlab-org/api/client-go` (Apache-2.0, v3.23.0) | cloudevents/sdk-go |
| **Python** | githubkit (GitHub verify+parse); own Pydantic models for GitLab, Sentry, PagerDuty | packit/ogr (GitHub, GitLab, Forgejo); drop to python-gitlab (LGPL-3.0) for approvals | cloudevents (sdk-python) |
| **TypeScript** | @octokit/webhooks (GitHub); own types for GitLab etc. | No multi-forge library worth adopting. Octokit + gitbeaker behind your own interface; Backstage `@backstage/integration` for GitHub App tokens | cloudevents (sdk-javascript) |

Go is the only language where one OSS library covers both webhook parsing and the API across GitHub, GitLab and Gitea. Even there, the GitLab issue and approval paths need Satat-side code. That argues for Go for open question 7 if forge breadth matters. In Python or TypeScript, Satat writes the normalisation layer over single-provider SDKs (roughly two adapters, a few hundred lines each).

### What stays provider-specific regardless

- **Auth:** GitHub App (JWT → per-installation token, 1 h, scoped per repo; fits NN10 short-lived credentials) vs GitLab project/group access tokens or OAuth (long-lived, rotated by you) vs Gitea tokens. Token minting is per provider.
- **Webhook authenticity:** HMAC (GitHub `X-Hub-Signature-256`, Gitea, Grafana, Sentry, PagerDuty `v1=` list) vs a shared plaintext token (GitLab `X-Gitlab-Token`) vs none (Alertmanager; rely on network or bearer).
- **Webhook registration:** GitHub App-level subscription (one config for all installed repos) vs per-project or per-group hooks on GitLab.
- **Labels:** GitHub flat labels on issues and PRs; label events are explicit (`labeled` action with `label`). GitLab has scoped labels (`scope::value`, mutually exclusive) and reports label changes only as `changes.labels.previous/current` diffs inside `update` events, so "which label was added" must be computed. Jira uses labels plus components plus transitions. Linear uses labels plus workflow states.
- **Reviews:** GitHub has review objects with state (APPROVED / CHANGES_REQUESTED / COMMENTED) and line comments tied to a review. GitLab has separate approvals (approve/unapprove, no "changes requested" until recent versions; UNVERIFIED for 2026) plus discussions/notes, and the "review submitted" signal is a batch of notes. Satat's `pr.reviewed` must be synthesised on GitLab.
- **PR vs MR identity:** GitHub `number` (shared with issues) vs GitLab `iid` (per project, separate from issues) plus a global `id`. Draft semantics differ (GitHub `draft` flag vs GitLab `Draft:` title prefix / `draft` attribute).
- **Comments:** GitHub issue comments vs review comments vs review-thread replies. GitLab notes vs discussions (threads, resolvable). Bot self-loop detection differs (`sender.type == "Bot"` vs a user id check).
- **Alerts:** severity vocabularies (Alertmanager labels, Sentry levels, PagerDuty urgency/priority, Datadog P1-P5) need a per-source mapping table. Resolution semantics differ (Alertmanager `resolved` batches vs PagerDuty `incident.resolved`).

## Sources

- jenkins-x/go-scm: https://github.com/jenkins-x/go-scm (files `scm/webhook.go`, `scm/issue.go`, `scm/pr.go`, `scm/review.go`, `scm/app.go`, `scm/driver/github/webhook.go`, `scm/driver/gitlab/webhook.go`, `scm/driver/gitlab/review.go`, `scm/driver/gitea/webhook.go`, `pkg/hmac/hmac.go`); releases https://github.com/jenkins-x/go-scm/releases
- jenkins-x/lighthouse: https://github.com/jenkins-x/lighthouse
- drone/go-scm: https://github.com/drone/go-scm (LICENSE, `scm/webhook.go`, `scm/pr.go`, `scm/review.go`, `scm/driver/github/webhook.go`, `scm/driver/gitlab/webhook.go`)
- yi-nology/git-platform-sdk: https://github.com/yi-nology/git-platform-sdk
- git-pkgs/forge: https://github.com/git-pkgs/forge ; https://nesbitt.io/2026/03/13/forge.html
- fluxcd/go-git-providers: https://github.com/fluxcd/go-git-providers
- packit/ogr: https://github.com/packit/ogr
- phil65/githarbor: https://github.com/phil65/githarbor ; prata0x/gfo: https://github.com/prata0x/gfo
- oritwoen/gixa: https://github.com/oritwoen/gixa ; capawesome-team/repo-sdk: https://github.com/capawesome-team/repo-sdk
- Renovate platforms: https://github.com/renovatebot/renovate/tree/main/lib/modules/platform
- Backstage integration: https://github.com/backstage/backstage/tree/master/packages/integration
- go-github: https://github.com/google/go-github ; GitLab client-go: https://gitlab.com/gitlab-org/api/client-go (`event_parsing.go`, LICENSE) ; xanzy/go-gitlab (archived): https://github.com/xanzy/go-gitlab
- githubkit: https://github.com/yanyongyu/githubkit (`githubkit/webhooks/__init__.py`) ; @octokit/webhooks: https://github.com/octokit/webhooks.js ; gitbeaker: https://github.com/jdalrymple/gitbeaker ; python-gitlab: https://github.com/python-gitlab/python-gitlab ; PyGithub: https://github.com/PyGithub/PyGithub
- Merge ticketing: https://www.merge.dev/integrations/gitlab , https://docs.merge.dev/merge-unified/ticketing/overview.mdx ; Unified.to: https://unified.to/ticketing ; Apideck: https://www.apideck.com/issue-tracking-api ; Nango LICENSE: https://github.com/NangoHQ/nango/blob/master/LICENSE
- CloudEvents: https://github.com/cloudevents/spec (README, `cloudevents/adapters/github.md`, `gitlab.md`) ; SDKs https://github.com/cloudevents/sdk-go , https://github.com/cloudevents/sdk-python , https://github.com/cloudevents/sdk-javascript ; PyPI https://pypi.org/project/cloudevents/
- CDEvents: https://github.com/cdevents/spec (`schemas/changereviewed.json`, `schemas/ticketcreated.json`) ; https://github.com/cdevents/sdk-python ; cdviz: https://github.com/cdviz-dev/cdviz-collector , https://github.com/cdviz-dev/transformers-community
- Alertmanager webhook config: https://github.com/prometheus/alertmanager/blob/main/docs/configuration.md#webhook_config ; notification data: https://prometheus.io/docs/alerting/latest/notifications/
- Grafana webhook notifier: https://grafana.com/docs/grafana/latest/alerting/configure-notifications/manage-contact-points/integrations/webhook-notifier/
- Sentry webhooks: https://docs.sentry.io/organization/integrations/integration-platform/webhooks/
- Datadog webhooks: https://docs.datadoghq.com/integrations/webhooks/
- PagerDuty signatures: https://docs.pagerduty.com/developer/verifying-webhook-signatures ; go-pagerduty webhookv3: https://github.com/PagerDuty/go-pagerduty/blob/main/webhookv3/webhookv3.go
- OpenTelemetry alert proposals: https://github.com/open-telemetry/semantic-conventions/issues/1081 , https://github.com/open-telemetry/opentelemetry-specification/issues/4729
- Keep: https://github.com/keephq/keep (LICENSE, `keep/api/models/alert.py`, `keep/providers/`) ; Elastic acquisition: https://www.elastic.co/security-labs/blog/elastic-and-keep-join-forces , https://www.businesswire.com/news/home/20250521686867/en/Elastic-Completes-Acquisition-of-Keep
- Hatchet webhooks: https://docs.hatchet.run/v1/webhooks , https://docs.hatchet.run/cookbooks/webhooks-github
