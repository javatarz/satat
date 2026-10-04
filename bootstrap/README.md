# Satat bootstrap

One-time setup, run **locally** with AWS admin credentials. It creates the pieces that
Terraform cannot create for itself:

- the **S3 state bucket** (versioned, encrypted, private),
- the **GitHub OIDC identity provider**, and
- the **Terraform IAM role** that GitHub Actions assumes.

Run it once; afterwards all provisioning runs in GitHub Actions via OIDC.

## Run

```bash
cd bootstrap
terraform init
terraform apply
```

Uses your ambient AWS credentials (`AWS_PROFILE` or env vars). State is local and
gitignored — this root is not managed by the CI pipeline.

## After

Set two GitHub repository **variables** from the outputs:

```bash
terraform output -raw state_bucket        # → TF_STATE_BUCKET
terraform output -raw terraform_role_arn  # → AWS_TERRAFORM_ROLE_ARN
```

Then provisioning runs in `.github/workflows/terraform.yml`.

## Notes

- The state bucket name is `satat-tfstate-<account-id>`.
- The Terraform role trusts `repo:javatarz/satat:*` (see `github_repository` variable) and
  has the state-bucket permissions plus `ec2:*`.
- A separate, more tightly scoped role for the start/stop power workflow is future work.
