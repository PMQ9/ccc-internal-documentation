############################
# RDS MySQL — Multi-AZ (Balanced decision)
############################
resource "aws_db_subnet_group" "this" {
  name       = "${var.name_prefix}-db-subnets"
  subnet_ids = aws_subnet.private[*].id
  tags       = { Name = "${var.name_prefix}-db-subnets" }
}

resource "aws_db_instance" "this" {
  identifier     = "${var.name_prefix}-db"
  engine         = "mysql"
  engine_version = var.db_engine_version
  instance_class = var.db_instance_class

  db_name  = var.db_name
  username = var.db_username
  # RDS manages the master password in Secrets Manager with native rotation (no plaintext, no
  # long-lived key). The app reads it from the generated secret (see iam.tf grant + user-data).
  manage_master_user_password = true

  allocated_storage     = var.db_allocated_gb
  max_allocated_storage = var.db_max_allocated_gb
  storage_type          = "gp3"
  storage_encrypted     = true

  multi_az               = true
  db_subnet_group_name   = aws_db_subnet_group.this.name
  vpc_security_group_ids = [aws_security_group.db.id]
  publicly_accessible    = false
  port                   = 3306

  backup_retention_period    = var.db_backup_retention_days
  backup_window              = "07:00-08:00" # UTC ~= 01:00-02:00 America/Chicago
  maintenance_window         = "Mon:08:30-Mon:09:30"
  copy_tags_to_snapshot      = true
  auto_minor_version_upgrade = true # BookStack tolerates minor MySQL bumps; major is manual + snapshot-first
  deletion_protection        = true
  skip_final_snapshot        = false
  final_snapshot_identifier  = "${var.name_prefix}-final-${var.environment}"
  apply_immediately          = false

  performance_insights_enabled          = true
  performance_insights_retention_period = 7
  enabled_cloudwatch_logs_exports       = ["error", "slowquery"]

  tags = { Name = "${var.name_prefix}-db" }
}

############################
# EFS — BookStack media (/config), survives instance replacement across AZs
############################
resource "aws_efs_file_system" "this" {
  creation_token  = "${var.name_prefix}-config"
  encrypted       = true
  throughput_mode = "bursting"

  lifecycle_policy {
    transition_to_ia = "AFTER_30_DAYS"
  }
  lifecycle_policy {
    transition_to_primary_storage_class = "AFTER_1_ACCESS"
  }

  tags = { Name = "${var.name_prefix}-config" }
}

resource "aws_efs_mount_target" "this" {
  count           = var.az_count
  file_system_id  = aws_efs_file_system.this.id
  subnet_id       = aws_subnet.private[count.index].id
  security_groups = [aws_security_group.efs.id]
}

# Access point pins ownership to uid/gid 1000 (matches BookStack PUID/PGID) so the container
# writes media with correct ownership regardless of which instance mounts it.
resource "aws_efs_access_point" "config" {
  file_system_id = aws_efs_file_system.this.id
  posix_user {
    uid = 1000
    gid = 1000
  }
  root_directory {
    path = "/bookstack"
    creation_info {
      owner_uid   = 1000
      owner_gid   = 1000
      permissions = "0755"
    }
  }
  tags = { Name = "${var.name_prefix}-config-ap" }
}

############################
# AWS Backup — RDS + EFS recovery points (versioning != backup; this is the real backup)
############################
resource "aws_backup_vault" "this" {
  name = "${var.name_prefix}-vault"
  tags = { Name = "${var.name_prefix}-vault" }
}

resource "aws_backup_plan" "this" {
  name = "${var.name_prefix}-plan"

  rule {
    rule_name         = "daily"
    target_vault_name = aws_backup_vault.this.name
    schedule          = "cron(0 7 * * ? *)" # 07:00 UTC daily
    start_window      = 60
    completion_window = 180
    lifecycle {
      delete_after = var.backup_retention_days
    }
  }

  tags = { Name = "${var.name_prefix}-plan" }
}

resource "aws_backup_selection" "this" {
  name         = "${var.name_prefix}-selection"
  iam_role_arn = aws_iam_role.backup.arn
  plan_id      = aws_backup_plan.this.id

  resources = [
    aws_db_instance.this.arn,
    aws_efs_file_system.this.arn,
  ]
}
