# ADR 0018: AWS EC2 as the host

## Status

Accepted (2026-10-05). Supersedes the Contabo host references in
[ADR 0011](0011-secrets-isolation.md), [ADR 0013](0013-provision-deploy-pipeline-split.md),
and [ADR 0015](0015-monitoring.md).

## Context

The stack runs on a single VM. Contabo bills per billing period (monthly, in advance) with
no proration: an instance used for two days still costs a full month. Most of Satat's time
is idle (the agent waits for labeled issues), so we want per-hour/per-second billing and
the ability to stop the instance to save cost. We also want a stable public address for
DNS across stop/start.

## Decision

Host on **AWS EC2**:

- **Instance**: on-demand `t4g.large` — 2 vCPU / 8 GB, ARM (Graviton) — in `ap-south-1`
  (Mumbai). Spot was the original plan, but `t4g.xlarge` Spot capacity was chronically
  unavailable in `ap-south-1` (persistent requests sat unfulfilled with
  `InsufficientInstanceCapacity`), so the saving was not realisable and we run on-demand,
  stopping the instance when idle to keep cost proportional to use.
- **AMI**: latest Ubuntu 24.04 arm64 from Canonical, discovered at plan time (no manual
  image ID).
- **Disk**: 100 GiB gp3, encrypted.
- **Address**: an Elastic IP gives a stable public IPv4 for the DNS A record across
  stop/start.
- **Firewall**: a security group allowing only `443/tcp` and `51820/udp`.
- **Provisioning**: GitHub Actions with S3 state and OIDC (see ADR 0013). The app layer is
  unchanged and provider-agnostic.

## Consequences

- **Per-second billing** while running; the instance can be stopped when idle.
- **Idle cost is not zero**: a stopped instance still bills for its EBS volume and its
  public IPv4 (roughly US$13/month at these sizes: gp3 ≈ US$9 + public IPv4 ≈ US$3.65).
  Snapshot-and-terminate is the only way to reach ~zero, at the cost of the live data volume.
- **On-demand, no reclaim**: unlike Spot there is no interruption risk. If capacity needs
  change, the instance is resized in place (stop/start) by changing the `instance_type`
  variable.
- **Provider-specific Terraform**: `terraform/` targets AWS; the Compose stack, Caddy,
  gateway templates, automations, and deploy pipeline are unaffected.
- **Region**: `ap-south-1` for proximity; can be changed with the `region` variable.
- **Rejected**: Contabo (no hourly billing; monthly in advance), and Spot `t4g.xlarge`
  (capacity unavailable in `ap-south-1`, so no saving was achievable).
