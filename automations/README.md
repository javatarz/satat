# Automation hooks

OpenHands lifecycle hooks that enforce Satat's bottom-rung quality gates:
deterministic checks the agent cannot talk its way past, because they run
outside the prompt in the agent runtime.

The canonical copies live here:

- `hooks/hooks.json` — registers the hooks (`pre_tool_use`, `stop`)
- `hooks/satat-hooks.sh` — both hooks, selected by mode (`pre-tool-use`, `stop`)
- `hooks/test-hooks.sh` — behavioural tests, run by pre-commit and CI

## How they are loaded

OpenHands only reads `.openhands/hooks.json` from the repository the agent is
working in, and Satat's conversations start in an empty workspace that the
agent clones into, so a repo-level hooks file would never load. Instead the
`satat-issue-to-pr` dispatcher in `javatarz/satat-automations` passes these
hooks as the conversation's `hook_config` when it starts the conversation. Each
`command` becomes `bash -c '<script>' satat-hooks <mode>` with the script
inlined, so there is no copy in the workspace for the agent to edit or delete.
The agent server stores the hook config with the conversation, so it also
applies on resume.

`satat-automations` holds a vendored copy of `hooks/` (the same pattern as the
vendored automation bundle). To change a hook, change it here, then copy
`hooks.json` and `satat-hooks.sh` into
`satat-automations/automations/satat-issue-to-pr/tarball/`.

## PreToolUse: tamper protection and dangerous commands

Runs before every tool call. It fails closed: an unreadable payload, a missing
tool name, or a missing `jq` blocks the call (exit `2`).

- **Protected paths.** Writes are blocked to anything under `.openhands/`, CI
  config (`.github/workflows/`, `.gitlab-ci.yml`, `.circleci/`, `Jenkinsfile`,
  …), and lint/format rules (`.eslintrc*`, `.prettierrc*`, `ruff.toml`,
  `.flake8`, `.yamllint`, `.shellcheckrc`, `.editorconfig`,
  `.pre-commit-config.yaml`, …). Test files (`tests/`, `test_*.py`,
  `*_test.go`, `*.test.ts`, `*.spec.ts`, `*Test.java`, …) may be **created**
  through `file_editor create` or an `apply_patch` "Add File", so the agent can
  add tests for its change, but an existing test cannot be changed or deleted.
  Covers `file_editor` (and its aliases) and `apply_patch`. Reads are never
  blocked.
- **Dangerous commands.** Recursive `rm` of `/`, `/*`, `~` or `$HOME` (any flag
  form, `--no-preserve-root`, under `sudo`); `chmod` to `777`/`0777`/`a+rwx`;
  `curl`/`wget` piped into a shell or interpreter, `bash <(curl …)` and
  `sh -c "$(curl …)"`.
- **Shell writes to protected paths.** Redirects, `tee`, and file-changing
  commands (`rm`, `mv`, `cp`, `sed -i`, `git checkout`/`restore`, …) that name a
  protected path.

Shell checks are best effort: a blocklist cannot see through `cd tests && rm
x.py`, scripts, or interpreters. Tools the hook does not know are allowed. The
Stop hook's tamper check is the backstop for anything that slips through.

## Stop: tamper check and CI-round cap

Runs when the agent tries to finish.

1. **Tamper check.** Diffs the work tree against the merge-base with the
   default branch (`SATAT_BASE_REF` overrides) and fails the round if any
   protected file was modified, deleted or added, except newly added tests. This
   catches protected-path edits however they were made.
2. **Checks.** Runs `SATAT_TEST_CMD` if set, otherwise
   `pre-commit run --all-files` (from `PATH`, else via `uvx`). A configured
   check that cannot run fails the round rather than passing silently.

A failed round exits `2` and feeds the output back so the agent fixes it. The
cap is **3** consecutive failed rounds (`SATAT_CI_ROUND_CAP` overrides it). On
the third the hook allows the finish with a `handing off to human — exceeded
CI-round cap` message and resets the counter, so a later revision in the same
conversation starts fresh. A passing round also resets it. The counter lives in
a temp file keyed by session, outside the work tree.

If a human changed protected files on the branch, every round fails the tamper
check and the agent hands off after the cap. That is intended: the agent should
not continue on top of changes it is not allowed to make.

## Verifying

```bash
automations/hooks/test-hooks.sh

echo '{"tool_name":"file_editor","tool_input":{"command":"str_replace","path":"tests/x_test.py"}}' \
  | automations/hooks/satat-hooks.sh pre-tool-use; echo "exit=$?"   # exit=2 if tests/x_test.py exists
```
