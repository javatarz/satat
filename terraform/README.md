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

This ticket declares only `region` (default `ap-south-1`); later tickets add more. AWS
credentials are **not** passed to the provider — GitHub Actions assumes an IAM role via
OIDC, so the provider block sets only `region`.

## Local use

```bash
cd terraform
terraform init -backend-config="bucket=<TF_STATE_BUCKET>"
terraform plan
```

`key`, `region`, and locking are fixed in `backend.tf`; only the bucket (globally unique
per account) is supplied at init. `terraform init -backend=false` followed by
`terraform validate` checks syntax and provider schema without touching remote state.
