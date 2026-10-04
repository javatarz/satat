output "state_bucket" {
  description = "Set this as the GitHub variable TF_STATE_BUCKET."
  value       = aws_s3_bucket.state.bucket
}

output "terraform_role_arn" {
  description = "Set this as the GitHub variable AWS_TERRAFORM_ROLE_ARN."
  value       = aws_iam_role.terraform.arn
}
