# Partial S3 backend. The bucket is supplied at `terraform init` time via -backend-config
# (from the TF_STATE_BUCKET GitHub variable) because backend configuration is evaluated
# before input variables exist, so it cannot use var.*. State locking uses the S3 lockfile
# (Terraform >= 1.10); no DynamoDB. Forks change region/bucket here.
terraform {
  backend "s3" {
    key          = "satat/terraform.tfstate"
    region       = "ap-south-1"
    use_lockfile = true
    encrypt      = true
  }
}
