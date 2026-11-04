"""Harbor agent that runs Satat on a task (#172).

Satat is an external agent here: its loop runs on the host, not in the task's sandbox. It copies
the task's repo out of the sandbox, runs one Satat step on it with `satat-step --snapshot` (the
same claude lane container, runner and push rehearsal as a real issue), then applies Satat's
commits back into the sandbox for Harbor's verifier.

Run it from the satat repo root with this directory on PYTHONPATH:

    PYTHONPATH=evals/agents harbor run -p evals/tasks -a satat_agent:SatatAgent -n 1

The lane's pass_env (CLAUDE_CODE_OAUTH_TOKEN) must be in the environment. A step that leaves no
result.json, or that the runner reports as failed (claude exited with an error), raises after its
commits are applied and its usage recorded: Harbor's own claude-code agent raises on the same
failures, so the baseline and Satat count them the same way.
"""

import asyncio
import json
import os
import re
import secrets
import shutil
import subprocess
import tempfile
from pathlib import Path

from harbor.agents.base import BaseAgent
from harbor.environments.base import BaseEnvironment
from harbor.models.agent.context import AgentContext

SATAT_ROOT = Path(__file__).resolve().parents[2]
REPO_DIR = "/app"
BUNDLE_PATH = "/tmp/satat-changes.bundle"


def _satat_version() -> str:
    """The satat commit under test, marked dirty when the working tree has changes."""
    try:
        return subprocess.run(
            ["git", "describe", "--always", "--dirty"],
            cwd=SATAT_ROOT, capture_output=True, text=True, check=True,
        ).stdout.strip()
    except (OSError, subprocess.CalledProcessError):
        return "unknown"


def _docker_host() -> str | None:
    """The Docker endpoint for satat-step's Go client, which reads DOCKER_HOST and not the CLI's
    context (colima, Docker Desktop)."""
    if os.environ.get("DOCKER_HOST"):
        return os.environ["DOCKER_HOST"]
    try:
        return subprocess.run(
            ["docker", "context", "inspect", "--format", "{{.Endpoints.docker.Host}}"],
            capture_output=True, text=True, check=True,
        ).stdout.strip() or None
    except (OSError, subprocess.CalledProcessError):
        return None


def usage_from_transcript(path: Path) -> dict:
    """Totals over every claude session in the transcript: the first run and any follow-up after
    a failed push rehearsal each end with a "result" event."""
    totals = {"input": 0, "cache_read": 0, "cache_creation": 0, "output": 0,
              "cost_usd": 0.0, "turns": 0, "sessions": 0, "models": set()}
    if not path.exists():
        return totals
    for line in path.read_text(errors="replace").splitlines():
        try:
            ev = json.loads(line)
        except json.JSONDecodeError:
            continue
        if ev.get("type") == "system" and ev.get("model"):
            totals["models"].add(ev["model"])
        if ev.get("type") != "result":
            continue
        u = ev.get("usage") or {}
        totals["input"] += u.get("input_tokens") or 0
        totals["cache_read"] += u.get("cache_read_input_tokens") or 0
        totals["cache_creation"] += u.get("cache_creation_input_tokens") or 0
        totals["output"] += u.get("output_tokens") or 0
        totals["cost_usd"] += ev.get("total_cost_usd") or 0.0
        totals["turns"] += ev.get("num_turns") or 0
        totals["sessions"] += 1
    return totals


class SatatAgent(BaseAgent):
    _binary: Path | None = None

    @staticmethod
    def name() -> str:
        return "satat"

    def version(self) -> str | None:
        return _satat_version()

    async def setup(self, environment: BaseEnvironment) -> None:
        # Build satat-step once per Harbor process, from the tree under test.
        if SatatAgent._binary is None:
            out = SATAT_ROOT / "docs" / "scratch" / "bin" / "satat-step"
            proc = await asyncio.create_subprocess_exec(
                "go", "build", "-o", str(out), "./cmd/satat-step",
                cwd=SATAT_ROOT, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.STDOUT,
            )
            log, _ = await proc.communicate()
            if proc.returncode != 0:
                raise RuntimeError(f"go build satat-step failed:\n{log.decode(errors='replace')}")
            SatatAgent._binary = out

    async def run(self, instruction: str, environment: BaseEnvironment, context: AgentContext) -> None:
        out = self.logs_dir / "satat"
        out.mkdir(parents=True, exist_ok=True)
        work = Path(tempfile.mkdtemp(prefix="satat-harbor-"))
        try:
            await self._run(instruction, environment, context, work, out)
        finally:
            shutil.rmtree(work, ignore_errors=True)

    async def _run(self, instruction, environment, context, work: Path, out: Path) -> None:
        snapshot = work / "repo"
        await environment.download_dir(REPO_DIR, snapshot)
        instr = work / "instruction.md"
        instr.write_text(instruction)

        task = re.sub(r"[^a-z0-9-]+", "-", environment.environment_name.lower()).strip("-") or "task"
        run_id = f"h{secrets.token_hex(6)}"
        env = dict(os.environ)
        if host := _docker_host():
            env["DOCKER_HOST"] = host
        cmd = [
            str(SatatAgent._binary), "--config", str(SATAT_ROOT / "satat.yaml"),
            "--repo", f"eval/{task}", "--snapshot", str(snapshot), "--instruction", str(instr),
            "--out", str(out), "--run", run_id, "--quiet",
        ]
        with open(out / "satat-step.log", "wb") as log:
            proc = await asyncio.create_subprocess_exec(
                *cmd, cwd=SATAT_ROOT, env=env, stdout=log, stderr=asyncio.subprocess.STDOUT,
            )
            try:
                code = await proc.wait()
            except asyncio.CancelledError:
                # Harbor's agent timeout: satat-step stops its container on SIGTERM.
                proc.terminate()
                await proc.wait()
                raise

        result_path = out / "result.json"
        if not result_path.exists():
            raise RuntimeError(f"satat-step exited {code} and left no result.json: see {out / 'satat-step.log'}")
        result = json.loads(result_path.read_text())

        bundle = out / "changes.bundle"
        if result.get("commits", 0) > 0 and bundle.exists():
            await environment.upload_file(bundle, BUNDLE_PATH)
            res = await environment.exec(
                f"git fetch -q {BUNDLE_PATH} 'refs/heads/*:refs/satat/*'"
                f" && git reset -q --hard {result['head_sha']}",
                cwd=REPO_DIR,
            )
            if res.return_code != 0:
                raise RuntimeError(f"applying Satat's commits failed: {res.stderr or res.stdout}")

        u = usage_from_transcript(out / "transcript.jsonl")
        context.n_input_tokens = u["input"] + u["cache_read"] + u["cache_creation"]
        context.n_cache_tokens = u["cache_read"]
        context.n_output_tokens = u["output"]
        context.cost_usd = u["cost_usd"]
        context.metadata = {
            "satat_run": run_id,
            "status": result.get("status"),
            "error": result.get("error"),
            "commits": result.get("commits", 0),
            "rehearsal": result.get("rehearsal"),
            "turns": u["turns"],
            "sessions": u["sessions"],
            "models": sorted(u["models"]),
            # Every session reports its quota with status "allowed"; only a refusal is a hit.
            "rate_limit_hits": sum(
                1 for e in result.get("rate_limit_events") or []
                if (e.get("rate_limit_info") or {}).get("status") != "allowed"
            ),
            "step_exit": code,
            "duration_ms": result.get("duration_ms"),
        }
        if result.get("status") == "failed":
            raise RuntimeError(f"Satat's step failed: {result.get('error')}")
