terraform {
  # Floor is the real minimum the repo uses, not a generic guess: backend.tf relies on S3-native
  # state locking (use_lockfile, TF >= 1.10) and tests/plan.tftest.hcl uses mock_provider /
  # override_* (TF >= 1.7/1.8). CI runs 1.13.3 (see Makefile TF_IMG / ci.yml TF_VERSION).
  required_version = ">= 1.10.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.47"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
  }
}
