# Automation hooks

OpenHands lifecycle hooks that enforce Satat's bottom-rung quality gates:
deterministic checks the agent cannot talk its way past, because they run
outside the prompt in the agent runtime.

The hooks belong to the automation definition and are applied to the working
repository's `.openhands/` directory when the automation runs. The canonical
copies live here:

- `.openhands/hooks.json` — registers the hooks
- `.openhands/pretooluse-block-dangerous.sh` — tamper protection + dangerous-command block
- `.openhands/stop-ci-round-cap.sh` — CI-round cap

## PreToolUse: tamper protection and dangerous commands

Runs before every tool call and fails closed (exit `2`) when the call is not
allowed. It blocks writes to the files that judge the change, so an agent
cannot make a failing test pass by editing the test, weaken CI, or relax a lint
rule, and it blocks destructive shell commands:

- **Protected paths** — anything under `.openhands/` (including `hooks.json`
  itself), test files (`tests/`, `test/`, `test_*.py`, `*_test.go`, `*.test.ts`,
  `*.spec.ts`, `*_test.c`, …), CI config (`.github/workflows/`, `.gitlab-ci.yml`,
  `.circleci/`, `Jenkinsfile`, `azure-pipelines.yml`, …), and lint/format rules
  (`.eslintrc*`, `eslint.config.*`, `.prettierrc*`, `.pylintrc`, `ruff.toml`,
  `.flake8`, `.yamllint`, `.shellcheckrc`, `.pre-commit-config.yaml`, …).
- **Dangerous commands** — `rm -rf /` (either flag order, root only),
  `chmod 777`, and piping `curl`/`wget` straight into a shell.
- Writes to a protected path via a shell redirect or `tee` are blocked too, so
  `file_editor` is not the only way in.

Reads (`file_editor` `view`) are never blocked.

## Stop: CI-round cap

Runs when the agent tries to finish. It runs the repository's checks (tests via
`SATAT_TEST_CMD` if set, otherwise `pre-commit run --all-files` from `mise` or
`PATH`). On failure it exits `2` and feeds the check output back, so the agent
fixes the failure instead of finishing. The cap is **3** consecutive failed
rounds (`SATAT_CI_ROUND_CAP` overrides it). On the third failure the hook stops
blocking and allows the agent to finish with a `handing off to human — exceeded
CI-round cap` message, so a stuck agent cannot loop forever. Any passing round
resets the counter. Only tests/lint gate the finish; a repository with no checks
configured is allowed through.

The round counter is kept in a temp file outside the work tree, keyed by
session, so it is never committed and parallel conversations do not share it.

## Verifying

`shellcheck` covers both scripts (the `shellcheck` pre-commit hook runs them in
CI). Exercise them directly by piping a tool-call payload to the PreToolUse
script, or set `SATAT_TEST_CMD` and run the Stop script repeatedly:

```bash
echo '{"tool_name":"file_editor","tool_input":{"command":"create","path":"tests/x_test.py"}}' \
  | .openhands/pretooluse-block-dangerous.sh; echo "exit=$?"   # exit=2

SATAT_TEST_CMD='false' .openhands/stop-ci-round-cap.sh        # exit=2, then 0 on the third run
```
