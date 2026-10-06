# Satat Terraform

Provisions the Satat VM on AWS. Both provisioning (this directory) and configuration
deploy run in **GitHub Actions**; app config is deployed separately — see
[ADR 0013](../docs/adr/0013-provision-deploy-pipeline-split.md).

## Pipeline

`.github/workflows/terraform.yml`:

- **Pull request** touching `terraform/**`: `fmt -check`, `validate`, `plan`; the plan is
  posted as a PR comment. The workflow authenticates to AWS via **GitHub OIDC** (no keys).
- **Push to `main`**: the same plan artifact is applied, gated by the `production` GitHub
  Environment (add required reviewers there to gate applies).

There is **no Terraform Cloud**. State lives in **S3** with the native S3 lockfile
(`use_lockfile`, Terraform ≥ 1.10) — no DynamoDB.

## Bootstrap

One-time, run locally before the first pipeline run: see
[`../bootstrap/README.md`](../bootstrap/README.md). It creates the S3 state bucket, the
GitHub OIDC provider, and the Terraform IAM role, and prints two values to set as GitHub
repository variables:

- `TF_STATE_BUCKET`
- `AWS_TERRAFORM_ROLE_ARN`

Forks must also change the trusted repository in `bootstrap/variables.tf`.

## Variables

AWS credentials are **not** passed to the provider — GitHub Actions assumes an IAM role via
OIDC, so the provider block sets only `region`.

- `region` — default `ap-south-1`
- `instance_type` — default `t4g.large` (2 vCPU / 8 GB, ARM)
- `root_volume_size` — default `100` GiB (gp3)
- `deploy_wg_public_key`, `laptop_wg_public_key` — WireGuard peer public keys
- `deploy_ssh_public_key`, `owner_ssh_public_key` — SSH public keys authorized for `deploy`

## Instance

- **On-demand** (`t4g.large`). Previously Spot, but `t4g.xlarge` Spot capacity was too scarce
  in `ap-south-1` and repeatedly blocked provisioning. On-demand trades cost for reliability.
  Resize later by changing `instance_type` (in-place stop/start).
- **Elastic IP** so the public address is stable across stop/start (use it for DNS).
- Security group allows only `443/tcp` (Caddy) and `51820/udp` (WireGuard).
- Root volume: 100 GiB gp3, encrypted.

The AMI is discovered automatically (latest Ubuntu 24.04 arm64 from Canonical); no image
ID is configured. The default VPC and a default subnet are used.

## Keys

Generate the WireGuard keypairs (run on the client, keep the private keys off this repo):

```bash
wg genkey | tee ci.key | wg pubkey > ci.pub            # ci peer (10.10.0.3)
wg genkey | tee laptop.key | wg pubkey > laptop.pub    # laptop peer (10.10.0.2)
ssh-keygen -t ed25519 -C ci@satat -f ci_ssh            # deploy SSH key (pipeline)
ssh-keygen -t ed25519 -C owner@laptop -f owner_ssh     # owner SSH key
```

Store the **public** keys as GitHub repository **variables** named
`TF_VAR_deploy_wg_public_key`, `TF_VAR_laptop_wg_public_key`,
`TF_VAR_deploy_ssh_public_key`, and `TF_VAR_owner_ssh_public_key` — the workflow passes
them to Terraform as `TF_VAR_*` environment variables. The private keys are stored only
where used: `ci.key` and `ci_ssh` become GitHub secrets (`DEPLOY_WG_PRIVATE_KEY`,
`DEPLOY_SSH_PRIVATE_KEY`); `laptop.key` and `owner_ssh` stay with the owner.

## Network

`sshd` binds `wg0` only and allows just the `deploy` user, so SSH is reachable solely over
the tunnel. The VM is the WireGuard server (`10.10.0.1/24`); peers are `laptop`
(`10.10.0.2`, owner) and `ci` (`10.10.0.3`, pipeline).

Build the laptop's WireGuard config with the VM's public key. It is only readable by root
and SSH is tunnel-only, so fetch it once via the EC2 serial console or by reading it as
root on the instance:

```sh
cat /etc/wireguard/server.pub
```

## DNS

DNS is **manual**. After a successful apply:

```
terraform output -raw instance_ip
```

Point an A record for `SATAT_DOMAIN` at that Elastic IP.

## Local use

```bash
cd terraform
terraform init -backend-config="bucket=<TF_STATE_BUCKET>"
terraform plan
```

`key`, `region`, and locking are fixed in `backend.tf`; only the bucket (globally unique
per account) is supplied at init. `terraform init -backend=false` followed by
`terraform validate` checks syntax and provider schema without touching remote state.
