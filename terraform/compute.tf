data "aws_ami" "al2023_arm64" {
  most_recent = true
  owners      = ["amazon"]
  filter {
    name   = "name"
    values = ["al2023-ami-2023.*-arm64"]
  }
  filter {
    name   = "architecture"
    values = ["arm64"]
  }
}

resource "aws_launch_template" "this" {
  name_prefix   = "${var.name_prefix}-"
  image_id      = data.aws_ami.al2023_arm64.id
  instance_type = var.instance_type

  iam_instance_profile {
    arn = aws_iam_instance_profile.ec2.arn
  }

  vpc_security_group_ids = [aws_security_group.app.id]

  metadata_options {
    http_endpoint = "enabled"
    http_tokens   = "required" # IMDSv2 only
  }

  block_device_mappings {
    device_name = "/dev/xvda"
    ebs {
      volume_size           = var.root_volume_gb
      volume_type           = "gp3"
      encrypted             = true
      delete_on_termination = true
    }
  }

  user_data = base64encode(templatefile("${path.module}/user-data.sh.tftpl", {
    region            = var.region
    name_prefix       = var.name_prefix
    efs_id            = aws_efs_file_system.this.id
    efs_ap_id         = aws_efs_access_point.config.id
    app_key_arn       = aws_secretsmanager_secret.app_key.arn
    db_secret_arn     = aws_db_instance.this.master_user_secret[0].secret_arn
    saml_idp_x509_arn = aws_secretsmanager_secret.saml["idp_x509"].arn
    timezone          = var.app_timezone
    alb_proxy_cidrs   = join(",", local.public_subnet_cidrs)
    rds_address       = aws_db_instance.this.address
    db_name           = var.db_name
    db_username       = var.db_username
    bookstack_image   = var.bookstack_image
    log_group         = aws_cloudwatch_log_group.app.name
    cw_namespace      = "${var.name_prefix}/host"
  }))

  tag_specifications {
    resource_type = "instance"
    tags          = { Name = "${var.name_prefix}-app" }
  }
  tag_specifications {
    resource_type = "volume"
    tags          = { Name = "${var.name_prefix}-app-root" }
  }

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_autoscaling_group" "this" {
  name                = "${var.name_prefix}-asg"
  min_size            = 1
  max_size            = 1
  desired_capacity    = 1
  vpc_zone_identifier = aws_subnet.private[*].id
  target_group_arns   = [aws_lb_target_group.this.arn]

  # ELB health check so a failed ALB target is replaced — but the check is DB-free (/icon.png),
  # so an RDS failover blip does NOT churn the only instance.
  health_check_type         = "ELB"
  health_check_grace_period = 600 # first boot: EFS mount + image pull + container start

  launch_template {
    id      = aws_launch_template.this.id
    version = "$Latest"
  }

  instance_refresh {
    strategy = "Rolling"
    preferences {
      min_healthy_percentage = 0 # single instance: replace it (brief downtime) on LT/config change
    }
  }

  tag {
    key                 = "Name"
    value               = "${var.name_prefix}-app"
    propagate_at_launch = true
  }
}
