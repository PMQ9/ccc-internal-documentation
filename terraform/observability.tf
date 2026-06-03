resource "aws_cloudwatch_log_group" "app" {
  name              = "/${var.name_prefix}/bookstack"
  retention_in_days = var.log_retention_days
  tags              = { Name = "${var.name_prefix}-logs" }
}

resource "aws_sns_topic" "alarms" {
  name = "${var.name_prefix}-alarms"
  # Encrypt alarm notifications at rest with the AWS-managed SNS key (free, no
  # rotation to manage) — consistent with the "encryption at rest everywhere" posture.
  kms_master_key_id = "alias/aws/sns"
  tags              = { Name = "${var.name_prefix}-alarms" }
}

resource "aws_sns_topic_subscription" "email" {
  count     = var.alarm_email == "" ? 0 : 1
  topic_arn = aws_sns_topic.alarms.arn
  protocol  = "email"
  endpoint  = var.alarm_email
}

locals {
  alarm_actions = [aws_sns_topic.alarms.arn]
}

############################
# ALB — RED / golden signals (alert on user-impacting symptoms)
############################
resource "aws_cloudwatch_metric_alarm" "alb_5xx" {
  alarm_name          = "${var.name_prefix}-alb-target-5xx"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 3
  period              = 60
  statistic           = "Sum"
  threshold           = 5
  namespace           = "AWS/ApplicationELB"
  metric_name         = "HTTPCode_Target_5XX_Count"
  dimensions          = { LoadBalancer = aws_lb.this.arn_suffix }
  alarm_description   = "BookStack returning 5xx — see runbooks/ (disk-full, DB, container down)."
  alarm_actions       = local.alarm_actions
  ok_actions          = local.alarm_actions
  treat_missing_data  = "notBreaching"
}

resource "aws_cloudwatch_metric_alarm" "alb_unhealthy" {
  alarm_name          = "${var.name_prefix}-alb-unhealthy-hosts"
  comparison_operator = "GreaterThanOrEqualToThreshold"
  evaluation_periods  = 2
  period              = 60
  statistic           = "Maximum"
  threshold           = 1
  namespace           = "AWS/ApplicationELB"
  metric_name         = "UnHealthyHostCount"
  dimensions          = { LoadBalancer = aws_lb.this.arn_suffix, TargetGroup = aws_lb_target_group.this.arn_suffix }
  alarm_description   = "No healthy BookStack target — site is down or instance is being replaced."
  alarm_actions       = local.alarm_actions
  ok_actions          = local.alarm_actions
  treat_missing_data  = "breaching"
}

resource "aws_cloudwatch_metric_alarm" "alb_latency" {
  alarm_name          = "${var.name_prefix}-alb-latency-p95"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 5
  period              = 60
  extended_statistic  = "p95"
  threshold           = 3
  namespace           = "AWS/ApplicationELB"
  metric_name         = "TargetResponseTime"
  dimensions          = { LoadBalancer = aws_lb.this.arn_suffix }
  alarm_description   = "p95 response time > 3s."
  alarm_actions       = local.alarm_actions
  ok_actions          = local.alarm_actions
  treat_missing_data  = "notBreaching"
}

############################
# EC2 (ASG-aggregated) — burstable saturation + instance health
############################
resource "aws_cloudwatch_metric_alarm" "cpu_credits" {
  alarm_name          = "${var.name_prefix}-ec2-low-cpu-credits"
  comparison_operator = "LessThanThreshold"
  evaluation_periods  = 3
  period              = 300
  statistic           = "Average"
  threshold           = 30
  namespace           = "AWS/EC2"
  metric_name         = "CPUCreditBalance"
  dimensions          = { AutoScalingGroupName = aws_autoscaling_group.this.name }
  alarm_description   = "Burstable CPU credits low — sustained CPU; consider stepping up the instance/DB class."
  alarm_actions       = local.alarm_actions
  treat_missing_data  = "notBreaching"
}

resource "aws_cloudwatch_metric_alarm" "status_check" {
  alarm_name          = "${var.name_prefix}-ec2-status-check"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 2
  period              = 60
  statistic           = "Maximum"
  threshold           = 0
  namespace           = "AWS/EC2"
  metric_name         = "StatusCheckFailed"
  dimensions          = { AutoScalingGroupName = aws_autoscaling_group.this.name }
  alarm_description   = "EC2 status check failing."
  alarm_actions       = local.alarm_actions
  treat_missing_data  = "notBreaching"
}

# Root disk (CloudWatch agent custom metric). SEARCH aggregates across the dynamic InstanceId.
resource "aws_cloudwatch_metric_alarm" "disk_high" {
  alarm_name          = "${var.name_prefix}-ec2-disk-high"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 3
  threshold           = 80
  alarm_description   = "Root disk > 80% — BookStack disk-full is a classic outage cause."
  alarm_actions       = local.alarm_actions
  # notBreaching (not breaching) is deliberate here: this is a CloudWatch-agent custom metric keyed
  # on the dynamic InstanceId, so every ASG instance refresh has a startup gap where the SEARCH
  # matches no series. Breaching would false-alarm on each refresh. Disk-full is backstopped by the
  # breaching alb_unhealthy / alb_5xx alarms, which fire on the user-visible symptom. (Unlike the
  # RDS/ALB alarms, this metric is not AWS-managed-continuous, so the comparison isn't apples-to-apples.)
  treat_missing_data = "notBreaching"

  metric_query {
    id          = "disk"
    expression  = "MAX(SEARCH('{${var.name_prefix}/host,InstanceId} MetricName=\"disk_used_percent\"', 'Average', 300))"
    label       = "max-disk-used-percent"
    return_data = true
  }
}

############################
# RDS
############################
resource "aws_cloudwatch_metric_alarm" "rds_cpu" {
  alarm_name          = "${var.name_prefix}-rds-cpu"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 5
  period              = 60
  statistic           = "Average"
  threshold           = 85
  namespace           = "AWS/RDS"
  metric_name         = "CPUUtilization"
  dimensions          = { DBInstanceIdentifier = aws_db_instance.this.identifier }
  alarm_description   = "RDS CPU high."
  alarm_actions       = local.alarm_actions
  treat_missing_data  = "notBreaching"
}

resource "aws_cloudwatch_metric_alarm" "rds_storage" {
  alarm_name          = "${var.name_prefix}-rds-low-storage"
  comparison_operator = "LessThanThreshold"
  evaluation_periods  = 1
  period              = 300
  statistic           = "Average"
  threshold           = 2147483648 # 2 GiB
  namespace           = "AWS/RDS"
  metric_name         = "FreeStorageSpace"
  dimensions          = { DBInstanceIdentifier = aws_db_instance.this.identifier }
  alarm_description   = "RDS free storage < 2 GiB."
  alarm_actions       = local.alarm_actions
  treat_missing_data  = "breaching"
}

resource "aws_cloudwatch_metric_alarm" "rds_connections" {
  alarm_name          = "${var.name_prefix}-rds-connections"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 3
  period              = 60
  statistic           = "Average"
  threshold           = 100
  namespace           = "AWS/RDS"
  metric_name         = "DatabaseConnections"
  dimensions          = { DBInstanceIdentifier = aws_db_instance.this.identifier }
  alarm_description   = "RDS connection count unusually high."
  alarm_actions       = local.alarm_actions
  treat_missing_data  = "notBreaching"
}

############################
# TLS cert expiry (critical for imported certs, which do NOT auto-renew)
############################
resource "aws_cloudwatch_metric_alarm" "cert_expiry" {
  alarm_name          = "${var.name_prefix}-cert-expiry"
  comparison_operator = "LessThanThreshold"
  evaluation_periods  = 1
  period              = 86400
  statistic           = "Minimum"
  threshold           = 30
  namespace           = "AWS/CertificateManager"
  metric_name         = "DaysToExpiry"
  dimensions          = { CertificateArn = local.certificate_arn }
  alarm_description   = "ACM/imported cert expires in < 30 days. Imported (Sectigo) certs do not auto-renew."
  alarm_actions       = local.alarm_actions
  treat_missing_data  = "notBreaching"
}
