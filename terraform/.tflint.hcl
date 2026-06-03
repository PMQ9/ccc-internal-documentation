# tflint config for the CCC BookStack Terraform footprint.
# Run:  cd terraform && tflint --init && tflint --recursive
# CI installs the AWS ruleset (downloads on `tflint --init`).

config {
  # Lint module call blocks too; we're a flat root module so this is mostly a no-op, but harmless.
  call_module_type = "local"
  force            = false
}

plugin "terraform" {
  enabled = true
  preset  = "recommended" # naming, unused decls, comment syntax, required_version, etc.
}

plugin "aws" {
  enabled = true
  version = "0.47.0"
  source  = "github.com/terraform-linters/tflint-ruleset-aws"
}

# Provider-version pinning is enforced via versions.tf + .terraform.lock.hcl, so the
# "must pin provider" rule is redundant noise here.
rule "terraform_required_providers" {
  enabled = true
}

rule "terraform_required_version" {
  enabled = true
}

# Naming convention: snake_case for resource/variable/output names (matches the codebase).
rule "terraform_naming_convention" {
  enabled = true
  format  = "snake_case"
}

# Documented variables/outputs are a house rule here (every var has a description already).
rule "terraform_documented_variables" {
  enabled = true
}

rule "terraform_documented_outputs" {
  enabled = true
}

# We intentionally do NOT enforce terraform_standard_module_structure (single flat root module
# by design, one file per concern — documented in terraform/README.md).
rule "terraform_standard_module_structure" {
  enabled = false
}
