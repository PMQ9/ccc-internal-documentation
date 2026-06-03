############################
# Core
############################
variable "region" {
  description = "AWS region (confirm with VUIT)."
  type        = string
  default     = "us-east-1"
}

variable "environment" {
  description = "Environment name (used in tags + resource names)."
  type        = string
  default     = "prod"
}

variable "name_prefix" {
  description = "Prefix for resource names."
  type        = string
  default     = "ccc-wiki"
}

############################
# Networking
############################
variable "vpc_cidr" {
  description = "VPC CIDR. /20 gives ample room; do not overlap with anything VUIT may peer."
  type        = string
  default     = "10.20.0.0/20"
}

variable "az_count" {
  description = "Number of AZs to span (2 per Balanced decision)."
  type        = number
  default     = 2
}

variable "vpn_ingress_cidrs" {
  description = <<-EOT
    Vanderbilt VPN egress CIDRs allowed to reach the ALB on 443. THESE COME FROM VUIT and are
    not guessable (GlobalProtect egress/NAT pools). The site's "only on VPN" property depends
    entirely on this list being correct. Empty by default so a misconfigured apply fails closed
    rather than exposing the ALB to the internet.
  EOT
  type        = list(string)
  default     = []
}

############################
# DNS / TLS
############################
variable "domain_name" {
  description = "Vanderbilt subdomain for the wiki, e.g. wiki.ccc.vanderbilt.edu (coordinate with VUIT)."
  type        = string
  default     = "wiki.ccc.vanderbilt.edu"
}

variable "certificate_arn" {
  description = <<-EOT
    ARN of an existing/imported ACM cert (e.g. a VUIT InCommon/Sectigo cert imported into ACM).
    If empty, an ACM-managed cert is requested for domain_name via DNS validation.
  EOT
  type        = string
  default     = ""
}

variable "route53_zone_id" {
  description = <<-EOT
    Route53 hosted zone ID for domain_name IF it lives in THIS account (then TF creates the
    validation + ALIAS records). Usually empty — VUIT owns DNS; add the records they need from
    the outputs.
  EOT
  type        = string
  default     = ""
}

############################
# Compute
############################
variable "instance_type" {
  description = "EC2 instance type for the app (Graviton/ARM; LinuxServer publishes arm64)."
  type        = string
  default     = "t4g.small"
}

variable "bookstack_image" {
  description = "Pinned BookStack image (validated locally on connor-server)."
  type        = string
  default     = "lscr.io/linuxserver/bookstack:v26.05-ls265"
}

variable "root_volume_gb" {
  description = "Root EBS (gp3) size. Media lives on EFS, so this stays small."
  type        = number
  default     = 30
}

############################
# Database (RDS MySQL, Multi-AZ per Balanced decision)
############################
variable "db_instance_class" {
  description = "RDS instance class. t4g.small (2 GiB) min — micro/1 GiB starves InnoDB + upgrade migrations."
  type        = string
  default     = "db.t4g.small"
}

variable "db_engine_version" {
  description = "RDS MySQL engine version."
  type        = string
  default     = "8.0"
}

variable "db_allocated_gb" {
  description = "Initial RDS storage (gp3)."
  type        = number
  default     = 20
}

variable "db_max_allocated_gb" {
  description = "Storage autoscaling cap."
  type        = number
  default     = 100
}

variable "db_backup_retention_days" {
  description = "RDS automated backup / PITR retention window."
  type        = number
  default     = 35
}

variable "db_name" {
  description = "BookStack database name."
  type        = string
  default     = "bookstackapp"
}

variable "db_username" {
  description = "BookStack database user."
  type        = string
  default     = "bookstack"
}

############################
# Backups / observability
############################
variable "backup_retention_days" {
  description = "AWS Backup retention for EFS + RDS recovery points."
  type        = number
  default     = 35
}

variable "log_retention_days" {
  description = "CloudWatch Logs retention for app logs."
  type        = number
  default     = 90
}

variable "alarm_email" {
  description = "Email subscribed to the SNS alarm topic (CCC/VUIT ops). Empty = no subscription created."
  type        = string
  default     = ""
}

############################
# App config (non-secret; secret values are generated/placeholder in Secrets Manager)
############################
variable "app_timezone" {
  description = "IANA timezone for the BookStack container (TZ)."
  type        = string
  default     = "America/Chicago"
}
