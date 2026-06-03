data "aws_caller_identity" "current" {}

############################
# EC2 instance role (no long-lived keys; least privilege)
############################
data "aws_iam_policy_document" "ec2_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "ec2" {
  name               = "${var.name_prefix}-ec2-role"
  assume_role_policy = data.aws_iam_policy_document.ec2_assume.json
  tags               = { Name = "${var.name_prefix}-ec2-role" }
}

data "aws_iam_policy_document" "ec2" {
  # Read exactly the secrets this app needs — app key, break-glass, SAML, and the RDS-managed
  # master password secret. No wildcard.
  statement {
    sid     = "ReadAppSecrets"
    actions = ["secretsmanager:GetSecretValue"]
    resources = concat(
      [
        aws_secretsmanager_secret.app_key.arn,
        aws_secretsmanager_secret.breakglass.arn,
      ],
      [for s in aws_secretsmanager_secret.saml : s.arn],
      [aws_db_instance.this.master_user_secret[0].secret_arn],
    )
  }

  statement {
    sid     = "ReadConfigParams"
    actions = ["ssm:GetParameter", "ssm:GetParameters", "ssm:GetParametersByPath"]
    resources = [
      "arn:aws:ssm:${var.region}:${data.aws_caller_identity.current.account_id}:parameter/${var.name_prefix}/*",
    ]
  }

  statement {
    sid       = "WriteOwnLogs"
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents", "logs:DescribeLogStreams"]
    resources = ["${aws_cloudwatch_log_group.app.arn}:*"]
  }

  statement {
    sid       = "MountEfsViaAccessPoint"
    actions   = ["elasticfilesystem:ClientMount", "elasticfilesystem:ClientWrite"]
    resources = [aws_efs_file_system.this.arn]
    condition {
      test     = "StringEquals"
      variable = "elasticfilesystem:AccessPointArn"
      values   = [aws_efs_access_point.config.arn]
    }
  }
}

resource "aws_iam_role_policy" "ec2" {
  name   = "${var.name_prefix}-ec2-policy"
  role   = aws_iam_role.ec2.id
  policy = data.aws_iam_policy_document.ec2.json
}

# Session Manager (shell into the private instance without SSH/bastion) + CloudWatch agent metrics.
resource "aws_iam_role_policy_attachment" "ssm_core" {
  role       = aws_iam_role.ec2.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_role_policy_attachment" "cw_agent" {
  role       = aws_iam_role.ec2.name
  policy_arn = "arn:aws:iam::aws:policy/CloudWatchAgentServerPolicy"
}

resource "aws_iam_instance_profile" "ec2" {
  name = "${var.name_prefix}-ec2-profile"
  role = aws_iam_role.ec2.name
}

############################
# AWS Backup service role
############################
data "aws_iam_policy_document" "backup_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["backup.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "backup" {
  name               = "${var.name_prefix}-backup-role"
  assume_role_policy = data.aws_iam_policy_document.backup_assume.json
  tags               = { Name = "${var.name_prefix}-backup-role" }
}

resource "aws_iam_role_policy_attachment" "backup" {
  role       = aws_iam_role.backup.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSBackupServiceRolePolicyForBackup"
}

resource "aws_iam_role_policy_attachment" "restore" {
  role       = aws_iam_role.backup.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSBackupServiceRolePolicyForRestores"
}
