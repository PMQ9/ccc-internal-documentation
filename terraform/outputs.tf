output "alb_dns_name" {
  description = "ALB DNS name. VUIT points the subdomain (CNAME/ALIAS) here unless route53_zone_id is set."
  value       = aws_lb.this.dns_name
}

output "alb_zone_id" {
  description = "ALB hosted zone ID (for an ALIAS record)."
  value       = aws_lb.this.zone_id
}

output "app_url" {
  value = var.domain_name == "" ? "https://${aws_lb.this.dns_name}" : "https://${var.domain_name}"
}

output "rds_endpoint" {
  value = aws_db_instance.this.address
}

output "rds_master_secret_arn" {
  description = "Secrets Manager ARN of the RDS-managed master credentials."
  value       = aws_db_instance.this.master_user_secret[0].secret_arn
}

output "efs_id" {
  value = aws_efs_file_system.this.id
}

output "vpn_prefix_list_id" {
  description = "Managed prefix list to populate with VUIT VPN egress CIDRs (var.vpn_ingress_cidrs)."
  value       = aws_ec2_managed_prefix_list.vpn.id
}

output "certificate_arn" {
  value = local.certificate_arn
}

output "acm_dns_validation_records" {
  description = "When ACM issues the cert and VUIT owns DNS, add these CNAMEs to validate it."
  value = local.create_acm ? [
    for dvo in aws_acm_certificate.this[0].domain_validation_options : {
      name  = dvo.resource_record_name
      type  = dvo.resource_record_type
      value = dvo.resource_record_value
    }
  ] : []
}

output "saml_sp_urls" {
  description = "Service Provider URLs for VUIT to register the SAML SP."
  value = {
    entity_id = "${var.domain_name == "" ? "https://${aws_lb.this.dns_name}" : "https://${var.domain_name}"}/saml2/metadata"
    acs       = "${var.domain_name == "" ? "https://${aws_lb.this.dns_name}" : "https://${var.domain_name}"}/saml2/acs"
    sls       = "${var.domain_name == "" ? "https://${aws_lb.this.dns_name}" : "https://${var.domain_name}"}/saml2/sls"
  }
}

output "sns_alarm_topic_arn" {
  value = aws_sns_topic.alarms.arn
}

output "breakglass_secret_arn" {
  description = "Secrets Manager ARN holding the two local break-glass admin passwords."
  value       = aws_secretsmanager_secret.breakglass.arn
}

output "ssm_config_prefix" {
  description = "SSM Parameter Store prefix for mutable runtime config (SAML endpoints, auth_method)."
  value       = "/${var.name_prefix}/"
}
