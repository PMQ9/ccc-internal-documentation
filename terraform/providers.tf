provider "aws" {
  region = var.region

  # Every resource gets these tags (cost allocation + ownership; AWS skill: tag everything).
  default_tags {
    tags = {
      Project   = "ccc-internal-documentation"
      App       = "bookstack"
      Owner     = "CCC"
      ManagedBy = "terraform"
      Env       = var.environment
    }
  }
}
