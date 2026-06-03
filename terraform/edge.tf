locals {
  create_acm        = var.certificate_arn == ""
  manage_validation = local.create_acm && var.route53_zone_id != ""
}

############################
# TLS certificate
#  - certificate_arn supplied (e.g. imported VUIT Sectigo cert) -> use it.
#  - else request an ACM cert (DNS validation). Auto-validated only when the zone is in THIS
#    account (route53_zone_id set). When VUIT owns DNS, the cert is created PENDING and the
#    validation CNAME is emitted as an output for VUIT to add (see outputs.tf).
############################
resource "aws_acm_certificate" "this" {
  count             = local.create_acm ? 1 : 0
  domain_name       = var.domain_name
  validation_method = "DNS"
  lifecycle {
    create_before_destroy = true
  }
  tags = { Name = "${var.name_prefix}-cert" }
}

resource "aws_route53_record" "cert_validation" {
  for_each = local.manage_validation ? {
    for dvo in aws_acm_certificate.this[0].domain_validation_options : dvo.domain_name => {
      name   = dvo.resource_record_name
      type   = dvo.resource_record_type
      record = dvo.resource_record_value
    }
  } : {}

  zone_id = var.route53_zone_id
  name    = each.value.name
  type    = each.value.type
  records = [each.value.record]
  ttl     = 60
}

resource "aws_acm_certificate_validation" "this" {
  count                   = local.manage_validation ? 1 : 0
  certificate_arn         = aws_acm_certificate.this[0].arn
  validation_record_fqdns = [for r in aws_route53_record.cert_validation : r.fqdn]
}

locals {
  certificate_arn = coalesce(var.certificate_arn, one(aws_acm_certificate.this[*].arn))
}

############################
# ALB (internet-facing, but ingress SG = VUIT VPN prefix list only)
############################
resource "aws_lb" "this" {
  name                       = "${var.name_prefix}-alb"
  internal                   = false
  load_balancer_type         = "application"
  security_groups            = [aws_security_group.alb.id]
  subnets                    = aws_subnet.public[*].id
  drop_invalid_header_fields = true
  enable_deletion_protection = true
  tags                       = { Name = "${var.name_prefix}-alb" }
}

resource "aws_lb_target_group" "this" {
  name                 = "${var.name_prefix}-tg"
  port                 = 80
  protocol             = "HTTP"
  vpc_id               = aws_vpc.this.id
  target_type          = "instance"
  deregistration_delay = 30

  # DB-free static asset (verified ~0.7ms TTFB, served by nginx without PHP/DB) so RDS failover
  # does not flap the health check and churn the instance.
  health_check {
    path                = "/icon.png"
    matcher             = "200"
    protocol            = "HTTP"
    interval            = 30
    timeout             = 5
    healthy_threshold   = 2
    unhealthy_threshold = 3
  }

  tags = { Name = "${var.name_prefix}-tg" }
}

resource "aws_lb_listener" "http_redirect" {
  load_balancer_arn = aws_lb.this.arn
  port              = 80
  protocol          = "HTTP"

  default_action {
    type = "redirect"
    redirect {
      protocol    = "HTTPS"
      port        = "443"
      status_code = "HTTP_301"
    }
  }
}

resource "aws_lb_listener" "https" {
  load_balancer_arn = aws_lb.this.arn
  port              = 443
  protocol          = "HTTPS"
  ssl_policy        = "ELBSecurityPolicy-TLS13-1-2-2021-06"
  certificate_arn   = local.certificate_arn

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.this.arn
  }
}

############################
# DNS — only when the zone is in this account; otherwise VUIT adds a CNAME/ALIAS to the ALB.
############################
resource "aws_route53_record" "alias" {
  count   = var.route53_zone_id != "" ? 1 : 0
  zone_id = var.route53_zone_id
  name    = var.domain_name
  type    = "A"
  alias {
    name                   = aws_lb.this.dns_name
    zone_id                = aws_lb.this.zone_id
    evaluate_target_health = true
  }
}
