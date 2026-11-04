"""Summarise a Harbor job into the eval plan's headline number and report card (#172).

    python3 evals/summarize.py docs/scratch/harbor-jobs/<job> [--save]

An attempt passes when its reward is 1 (hidden tests pass and the original tests still pass). A
trial that raised is a system error: it is counted apart and left out of the pass rate, since it
measured the machinery, not the agent. The exception is an agent timeout, which is the agent's
own failure and counts as a fail. Cost and speed come from the trial timings and from the
claude stream-json the agent left in its logs, so the stock claude-code agent and Satat are
measured the same way.

--save writes evals/results/<job>.json and .md, and compares with the newest earlier summary for
the same agent, flagging a report-card number more than 25% worse.
"""

import argparse
import json
import statistics
import sys
from datetime import datetime
from pathlib import Path

RESULTS = Path(__file__).resolve().parent / "results"
AGENT_FAILURES = {"AgentTimeoutError"}  # exceptions that are the agent's result, not a system error
WORSE = 0.25  # the plan's guardrail: flag anything more than 25% worse


def _ts(s: str | None) -> datetime | None:
    return datetime.fromisoformat(s.replace("Z", "+00:00")) if s else None


def _stream_usage(agent_dir: Path) -> dict:
    """Usage over every claude "result" event, and rate-limit events, in the agent's logs."""
    u = {"tokens": 0, "turns": 0, "rate_limits": 0, "models": set(), "found": False}
    for f in agent_dir.rglob("*"):
        if not f.is_file() or f.suffix not in (".jsonl", ".txt", ".log") or f.stat().st_size > 50 << 20:
            continue
        for line in f.read_text(errors="replace").splitlines():
            if not line.startswith("{"):
                continue
            try:
                ev = json.loads(line)
            except json.JSONDecodeError:
                continue
            kind = ev.get("type")
            if kind == "system" and ev.get("model"):
                u["models"].add(ev["model"])
            elif kind == "rate_limit_event":
                u["rate_limits"] += 1
            elif kind == "result":
                us = ev.get("usage") or {}
                u["tokens"] += sum(us.get(k) or 0 for k in (
                    "input_tokens", "cache_read_input_tokens", "cache_creation_input_tokens", "output_tokens"))
                u["turns"] += ev.get("num_turns") or 0
                u["found"] = True
    return u


def load(job: Path) -> dict:
    trials = []
    agent = version = None
    for rp in sorted(job.glob("*/result.json")):
        r = json.loads(rp.read_text())
        agent = r["agent_info"]["name"]
        version = r["agent_info"].get("version")
        rewards = (r.get("verifier_result") or {}).get("rewards") or {}
        ex = r.get("agent_execution") or {}
        start, end = _ts(ex.get("started_at")), _ts(ex.get("finished_at"))
        u = _stream_usage(rp.parent / "agent")
        meta = (r.get("agent_result") or {}).get("metadata") or {}
        error = (r.get("exception_info") or {}).get("exception_type")
        timed_out = error in AGENT_FAILURES
        trials.append({
            "task": r["task_name"].split("/")[-1],
            "trial": r["trial_name"],
            "error": None if timed_out else error,
            "timed_out": timed_out,
            "reward": 0 if timed_out else rewards.get("reward"),
            "solved": rewards.get("solved"),
            "intact": rewards.get("intact"),
            "minutes": round((end - start).total_seconds() / 60, 2) if start and end else None,
            "tokens": u["tokens"] if u["found"] else None,
            "turns": u["turns"] if u["found"] else None,
            "rate_limits": max(u["rate_limits"], meta.get("rate_limit_events") or 0),
            "models": sorted(u["models"] | set(meta.get("models") or [])),
        })
    if not trials:
        sys.exit(f"no trials in {job}")
    return {"job": job.name, "agent": agent, "version": version, "trials": trials}


def _median(xs):
    xs = [x for x in xs if x is not None]
    return round(statistics.median(xs), 2) if xs else None


def score(run: dict) -> dict:
    trials = run["trials"]
    valid = [t for t in trials if not t["error"] and t["reward"] is not None]
    tasks = sorted({t["task"] for t in trials})
    per_task = {}
    for name in tasks:
        ts = [t for t in trials if t["task"] == name]
        ok = [t for t in ts if not t["error"] and t["reward"] is not None]
        per_task[name] = {
            "tries": "".join("E" if t["error"] or t["reward"] is None else ("✓" if t["reward"] == 1 else "✗") for t in ts),
            "all_passed": bool(ok) and len(ok) == len(ts) and all(t["reward"] == 1 for t in ok),
            "minutes": _median(t["minutes"] for t in ok),
            "tokens": _median(t["tokens"] for t in ok),
        }
    return {
        "attempts": len(trials),
        "system_errors": len(trials) - len(valid),
        "pass_rate": round(sum(t["reward"] == 1 for t in valid) / len(valid), 3) if valid else None,
        "reliability": round(sum(p["all_passed"] for p in per_task.values()) / len(tasks), 3),
        "median_tokens": _median(t["tokens"] for t in valid),
        "median_turns": _median(t["turns"] for t in valid),
        "median_minutes": _median(t["minutes"] for t in valid),
        "rate_limit_hits": sum(t["rate_limits"] for t in trials),
        "models": sorted({m for t in trials for m in t["models"]}),
        "tasks": per_task,
    }


def previous(agent: str, job: str) -> dict | None:
    """The newest saved summary for the same agent, other than this job."""
    found = []
    for p in RESULTS.glob("*.json"):
        s = json.loads(p.read_text())
        if s["agent"] == agent and s["job"] != job:
            found.append(s)
    return max(found, key=lambda s: s["saved_at"]) if found else None


def compare(now: dict, before: dict) -> list[str]:
    a, b = now["score"], before["score"]
    lines = []
    if a["pass_rate"] is not None and b["pass_rate"] is not None:
        d = a["pass_rate"] - b["pass_rate"]
        verdict = "better" if d > 0 else "worse" if d < 0 else "the same"
        lines.append(f"- Pass rate {b['pass_rate']:.0%} → {a['pass_rate']:.0%}: {verdict}.")
    for key, label, higher_is_better in [
        ("reliability", "Reliability", True), ("median_tokens", "Median tokens", False),
        ("median_turns", "Median turns", False), ("median_minutes", "Median minutes", False),
    ]:
        x, y = a[key], b[key]
        if x is None or not y:
            continue
        change = (x - y) / y
        if (change < -WORSE) if higher_is_better else (change > WORSE):
            lines.append(f"- **{label} is {abs(change):.0%} worse**: {y} → {x}.")
    if a["system_errors"] > b["system_errors"]:
        lines.append(f"- System errors rose: {b['system_errors']} → {a['system_errors']}.")
    return lines


def markdown(s: dict, before: dict | None) -> str:
    sc = s["score"]
    pct = lambda v: "–" if v is None else f"{v:.0%}"
    num = lambda v: "–" if v is None else v
    out = [
        f"# {s['job']}",
        "",
        f"Agent **{s['agent']}** {s['version'] or ''}, model {', '.join(sc['models']) or 'unknown'}, "
        f"{sc['attempts']} attempts, summarised {s['saved_at'][:10]}.",
        "",
        "| Pass rate | Reliability | System errors | Median tokens | Median turns | Median minutes | Rate-limit hits |",
        "|---|---|---|---|---|---|---|",
        f"| **{pct(sc['pass_rate'])}** | {pct(sc['reliability'])} | {sc['system_errors']} | {num(sc['median_tokens'])} "
        f"| {num(sc['median_turns'])} | {num(sc['median_minutes'])} | {sc['rate_limit_hits']} |",
        "",
        "| Task | Tries | Median minutes | Median tokens |",
        "|---|---|---|---|",
    ]
    out += [f"| {n} | {p['tries']} | {num(p['minutes'])} | {num(p['tokens'])} |" for n, p in sc["tasks"].items()]
    out += ["", "✓ passed, ✗ failed, E system error (not counted in the pass rate)."]
    if before:
        out += ["", f"## Compared with {before['job']}", ""] + (compare(s, before) or ["- No change worth flagging."])
    return "\n".join(out) + "\n"


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("job", type=Path, help="a Harbor job directory")
    ap.add_argument("--save", action="store_true", help="save the summary in evals/results/ and compare")
    args = ap.parse_args()
    run = load(args.job)
    summary = {
        "job": run["job"], "agent": run["agent"], "version": run["version"],
        "saved_at": datetime.now().isoformat(timespec="seconds"),
        "score": score(run), "trials": run["trials"],
    }
    before = previous(run["agent"], run["job"]) if args.save else None
    md = markdown(summary, before)
    print(md)
    if args.save:
        RESULTS.mkdir(exist_ok=True)
        (RESULTS / f"{run['job']}.json").write_text(json.dumps(summary, indent=2) + "\n")
        (RESULTS / f"{run['job']}.md").write_text(md)
        print(f"saved evals/results/{run['job']}.json and .md")


if __name__ == "__main__":
    main()
