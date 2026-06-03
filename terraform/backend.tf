# Remote state. The state bucket must be created OUT OF BAND before `terraform init`
# (it cannot be managed by the same state it stores). Create once, manually:
#
#   aws s3api create-bucket --bucket ccc-wiki-tfstate-<acct> --region us-east-1
#   aws s3api put-bucket-versioning --bucket ccc-wiki-tfstate-<acct> \
#       --versioning-configuration Status=Enabled
#   aws s3api put-bucket-encryption --bucket ccc-wiki-tfstate-<acct> \
#       --server-side-encryption-configuration \
#       '{"Rules":[{"ApplyServerSideEncryptionByDefault":{"SSEAlgorithm":"aws:kms"}}]}'
#
# Then init with the values in backend.hcl:  terraform init -backend-config=backend.hcl
#
# S3 native state locking (use_lockfile, provider >= 5.60 / TF >= 1.10) avoids a DynamoDB table.
terraform {
  backend "s3" {
    # bucket       = "ccc-wiki-tfstate-<acct>"   # via backend.hcl
    # key          = "ccc-wiki/prod/terraform.tfstate"
    # region       = "us-east-1"
    # encrypt      = true
    # use_lockfile = true
  }
}
