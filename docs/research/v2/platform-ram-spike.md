# Platform RAM and CPU spike (#104)

Part of #99. Asks whether k3s, Hatchet, Postgres and LiteLLM fit on the t4g.xlarge (4 vCPU, 16 GiB) with headroom for concurrent step pods. Accepted in ADR 0022 pending this number.

## Setup

- **Local, no cloud.** A dedicated colima profile (`satat-104`, `vz`, aarch64, containerd) running real k3s v1.34.12. The profile was rebuilt from scratch for each tier. Images came from public registries (read-only pulls).
- **Stack.** `postgres:15.6` (one instance, two databases), `hatchet-lite:latest`, `litellm:main-stable` with a dummy model and no traffic, and a Python Hatchet worker pod standing in for the Satat worker. coredns, metrics-server and local-path ran as k3s defaults. Alloy, ntfy, oauth2-proxy and Caddy were not deployed.
- **Step pods.** The worker's Hatchet task creates one pod per step (`python:3.13-slim`, request 250m CPU / 512 Mi, limit 640 Mi) and waits for it to finish. A step holds 400 MiB, runs a fixed amount of CPU work (about 10 s on an uncontended core), then sleeps 20 s, which approximates a harness that waits on an LLM. The CPU work is a fixed iteration count, so contention shows up as a longer step.
- **Tiers.** Profile size is the only variable: 2 CPU / 4 GiB, 4 CPU / 6 GiB and 8 CPU / 8 GiB. N concurrent steps were triggered at once. RAM and CPU were sampled every 6 s with metrics-server.
- Run on 2026-10-16 on an Apple-silicon laptop with 16 GiB. The harness was `docs/scratch/104/` (gitignored, not committed).

## Results

### Platform footprint

Measured per pod with metrics-server, settled for about 3 minutes after start.

| Component | RAM idle | RAM under load (peak seen) | CPU idle |
|---|---|---|---|
| LiteLLM | 590 to 760 MiB | 778 MiB | 2 to 6m |
| Postgres | 300 to 400 MiB | 782 MiB | 13 to 30m |
| Hatchet-lite | 35 to 140 MiB | 174 MiB | 8 to 13m |
| Worker (Python SDK + kubernetes client) | 230 to 350 MiB | 363 MiB | 1 to 4m |
| k3s system pods | about 150 MiB | about 220 MiB | under 15m |
| **Whole VM after settling, no steps** | **about 2.0 to 2.4 GiB** | | **0.1 to 0.2 core** |

The first two minutes after start cost about 1 core, mostly LiteLLM's database migrations and index builds. Idle CPU then falls to the figure above.

The worker figure is for my Python stand-in and says little about the real worker.

### Concurrent steps (clean runs)

`run_s` is the step pod's run time. The uncontended baseline is 29 to 30 s (10 s of CPU work and 20 s of sleep). Create-to-running is the time from the pod being created to it running.

| Tier | N | Wall | Create to running | `run_s` max | Node RAM peak | Node CPU peak | Result |
|---|---|---|---|---|---|---|---|
| 2 CPU / 4 GiB | 1 | 32 s | 0.5 s | 30 s | 3.7 GiB | 2.0 cores | pass |
| | 2 | 32 s | 1.8 s | 30 s | 3.1 GiB | 1.0 | pass |
| | 4 | 42 s | 1.6 s | 39 s | 3.6 GiB | 1.5 | pass, **CPU-bound steps ran 1.3x slower** |
| | 6 | | | | | | **fail: out of memory**, Hatchet, LiteLLM and the worker restarted |
| 4 CPU / 6 GiB | 1 | 31 s | 1.0 s | 29 s | 2.8 GiB | 0.7 | pass |
| | 2 | 32 s | 1.5 s | 29 s | 3.3 GiB | 1.1 | pass |
| | 4 | 33 s | 1.4 s | 31 s | 4.1 GiB | 3.3 | pass, no slowdown |
| | 8 | 40 s | 2.9 s | 39 s | 5.4 GiB | 3.4 | pass, **CPU-bound steps ran 1.3x slower** |
| | 10 | 50 s | 26.5 s | 42 s | 5.6 GiB of 5.9 | 3.8 | pass but at the memory limit: pods took 26 s to start |
| | 12 | | | | | | **fail**: Hatchet, LiteLLM and the worker restarted, probes timed out |
| 8 CPU / 8 GiB | 1 | 31 s | 1.5 s | 29 s | 2.9 GiB | 0.7 | pass |
| | 4 | 32 s | 1.5 s | 30 s | 4.3 GiB | 2.0 | pass |
| | 8 | 37 s | 5.4 s | 31 s | 6.1 GiB | 5.9 | pass, no slowdown |

Hatchet's own queueing was never the delay on a healthy node: enqueue to task start was 0.02 to 0.13 s at every clean N.

The 4 CPU / 6 GiB N=12 and 2 CPU / 4 GiB N=6 failures were memory exhaustion on the VM. The platform pods had no requests or limits, so the kubelet and the OOM killer treated them like any step.

### Invalid runs (rig artefact, not a platform limit)

An 8 CPU / 12 GiB VM on the 16 GiB laptop made the host swap (2.5 of 3 GB swap in use, over a million swap-outs). N=4, 8, 12 and 16 on that tier are not valid. Postgres failed its readiness probe, Hatchet logged database timeouts, and the worker missed heartbeats for 201 s. N=4 and N=8 then failed with `AlreadyExists`, and N=12 and N=16 showed uniform stalls of 200 s. The raw results are kept as `results/t2-8cpu-12g-INVALID-host-swap`. A rerun of N=12 on that tier after the stall cleared completed in 69 s, with 7 of 12 steps waiting 35 s to start.

## Findings

1. **The platform needs about 2.4 GiB, not the 1.5 to 4 GB estimated earlier.** Idle: LiteLLM about 0.6 to 0.8 GiB, Postgres 0.3 to 0.4 GiB (0.8 GiB under load), Hatchet-lite 0.1 GiB or less (0.17 under load), k3s and system pods about 0.2 GiB, plus the worker. The whole VM sits at 2.0 to 2.4 GiB with nothing running.
2. **The t4g.xlarge fits with room.** 16 GiB minus 2.4 GiB leaves about 13.6 GiB. After 1.5 GiB of headroom for the kernel, page cache and spikes that is 12 GiB for steps. The synthetic step used 404 MiB, which would give 29 steps by RAM. The research note's figure of about 2 GB per harness pod is not measured here, and at that size the limit is about 6 concurrent steps. The real harness RSS is the number that sets concurrency. It is not covered by this spike.
3. **CPU is the likelier limit on a 4 vCPU box.** Steps that burn CPU run at full speed up to one step per core, and slow in proportion after that (8 CPU-bound steps on 4 CPUs ran 1.3x slower on the tail, 4 on 2 CPUs the same). Platform CPU is small (0.1 to 0.2 core idle, a few tens of millicores more under load). Concurrency should therefore be capped by RAM and by vCPU count for CPU-heavy steps, not by the platform's own needs.
4. **Memory exhaustion takes the platform down with the steps.** The failures at N=6 on 4 GiB and N=12 on 6 GiB restarted Hatchet, LiteLLM and the worker, because none had requests or limits. The deploy must give the platform pods requests (and Postgres and LiteLLM limits), and the global concurrency cap must keep step requests within node allocatable minus the platform's share. With zero platform requests, the scheduler counted only the step pods' 512 Mi requests, so it placed more steps than the node could hold alongside the platform.
5. **Hatchet reassigns work when the platform stalls, so the step launcher must be idempotent.** A 200 s stall (heartbeat lost) made Hatchet re-deliver steps that were already running, and the second attempt hit `AlreadyExists` on the pod name. The worker flow from #103 (create claim, watch, delete) should use a deterministic claim name, treat `AlreadyExists` as "adopt it", and delete on every exit path. Feeds ADR 0023.
6. **Pod start is fast until memory is tight.** Create to running was 1 to 2 s up to N=4 and 3 to 5 s at N=8 on a healthy node. It rose to 26 s at N=10 on 6 GiB, when the node was at 5.6 of 5.9 GiB.
7. **A burstable instance changes the CPU picture.** These numbers do not capture t4g CPU credits. A t4g.xlarge has a baseline of 40% per vCPU (AWS published figure, not re-verified here), so sustained CPU-bound steps would draw down credits and, in unlimited mode, cost extra. The idle platform draws about 0.1 to 0.2 core, well under baseline. Steps that wait on an LLM are mostly idle. A step that builds or tests for minutes is not.

## Not covered

- The real harness (Claude Code, OpenHands). RAM and CPU per real step are assumptions here, not measurements. This is the number that sets the concurrency cap.
- gVisor overhead, agent-sandbox and network policy on the node (#103 measured those separately).
- Real Graviton2. The laptop is faster per core than a t4g vCPU, so CPU timings are relative, not absolute. RAM figures transfer directly.
- Alloy, ntfy, oauth2-proxy, Caddy and the existing OpenHands Canvas. Anything that runs on the same VM adds to the 2.4 GiB.
- Sustained load. Each N ran for about 30 s. Postgres growth over days, Hatchet's retention behaviour and LiteLLM spend-log growth were not observed.
- LiteLLM with real traffic or the OTLP and Prometheus callbacks it runs with in production.
- A 16 GiB VM. The laptop cannot host one without swapping, so the top of the range was not run. RAM use is linear in the figures above (about 360 MiB per step on the node), so extrapolating is safe, but it was not measured.

## Decision input

The platform needs about 2.4 GiB with nothing running on any host, so a 16 GiB node leaves about 13 GiB for steps. The hosting target may move off EC2, so the t4g-specific points (burst credits, instance sizes) apply only if the VM stays on EC2. A real-Graviton check was dropped. On the current t4g.xlarge, keep it. Move to the next size (t4g.2xlarge, 8 vCPU / 32 GiB) only if the measured harness RSS cannot fit the planned concurrency, or if CPU-heavy steps (builds, tests) are the common case. Before ADR 0023 closes the Hatchet item: set platform requests and limits, size the global cap from real harness memory, and make the launcher idempotent.
