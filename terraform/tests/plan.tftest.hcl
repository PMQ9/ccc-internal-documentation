# =============================================================================
# Terraform native test suite — plan-time security & reliability assertions.
#
# Maps to docs/test-plans/bookstack-platform.md TF-001..TF-017.
#
# WHY mocked providers: every assertion here is a property of the *plan* (config
# intent), not of a live AWS account. `mock_provider` lets `terraform test` run
# with ZERO AWS credentials and zero network — so it runs on every PR in seconds.
# The mock fills provider-*computed* values (ARNs, IDs, the rendered IAM policy
# JSON) with generated stand-ins; values we set in HCL (multi_az, encrypted,
# http_tokens, ssl_policy, ...) flow through unchanged and ARE asserted exactly.
#
# Deep IAM-wildcard / policy-content checks live in trivy+checkov (they parse the
# real HCL); see .github and the test plan's L-IAC gate. Here we assert structure
# and the config-set attributes.
#
# Run:  cd terraform && terraform init -backend=false && terraform test
# =============================================================================

mock_provider "aws" {
  # aws_availability_zones is sliced to var.az_count (=2) — it must return >=2 names
  # or `slice(...)` blows up at plan time.
  mock_data "aws_availability_zones" {
    defaults = {
      names    = ["us-east-1a", "us-east-1b", "us-east-1c"]
      zone_ids = ["use1-az1", "use1-az2", "use1-az4"]
    }
  }

  # aws_iam_policy_document.json is provider-computed; the mock would return a non-JSON
  # stub, which fails aws_iam_role's "valid JSON policy" check at plan. Return a valid
  # (empty) policy. We don't assert on policy *content* here — IAM least-privilege /
  # wildcard checks are delegated to checkov + trivy (they parse the real HCL). See L-IAC.
  mock_data "aws_iam_policy_document" {
    defaults = {
      json = "{\"Version\":\"2012-10-17\",\"Statement\":[]}"
    }
  }

  # master_user_secret is RDS-managed (computed). It's dereferenced as [0].secret_arn in
  # both compute.tf (user-data) and iam.tf — give it one element so plan doesn't index-error.
  mock_resource "aws_db_instance" {
    defaults = {
      master_user_secret = [{
        secret_arn    = "arn:aws:secretsmanager:us-east-1:123456789012:secret:rds!mock-AbCdEf"
        kms_key_id    = "arn:aws:kms:us-east-1:123456789012:key/00000000-0000-0000-0000-000000000000"
        secret_status = "active"
      }]
    }
  }
}

# random computes locally and needs no creds; mocking it just makes values known (not unknown)
# at plan so nothing downstream goes unknown unexpectedly.
mock_provider "random" {}

# Common "valid production-shaped" inputs reused by most runs.
variables {
  vpn_ingress_cidrs = ["129.59.0.0/16", "10.0.0.0/8"]
  certificate_arn   = "arn:aws:acm:us-east-1:123456789012:certificate/11111111-2222-3333-4444-555555555555"
  alarm_email       = "ccc-ops@vanderbilt.edu"
}

# -----------------------------------------------------------------------------
# TF-002 — SEC-002: fail closed when no VPN CIDRs are supplied.
# The single most important security property: a misconfigured apply must NOT
# expose the ALB. Empty var => zero prefix-list entries => ALB admits nobody.
# -----------------------------------------------------------------------------
run "tf002_fail_closed_when_no_vpn_cidrs" {
  command = plan

  variables {
    vpn_ingress_cidrs = []
    certificate_arn   = "arn:aws:acm:us-east-1:123456789012:certificate/empty-test"
    alarm_email       = ""
  }

  assert {
    condition     = length(aws_ec2_managed_prefix_list.vpn.entry) == 0
    error_message = "FAIL-OPEN RISK: empty vpn_ingress_cidrs must yield a prefix list with zero entries (ALB closed to all)."
  }
}

# -----------------------------------------------------------------------------
# The big baseline run: production-shaped vars, most SEC/OPS/CFG assertions.
# Stays on `command = plan` so the AWS provider doesn't run apply-time ARN/ID
# format validation against mock-generated stubs. Config-set values (multi_az,
# encrypted, ssl_policy, http_tokens, ...) are known at plan and asserted here.
# Two computed ARNs that the AWS Backup assertion needs are made known at plan
# via override_during=plan. (user_data *content* is asserted by the rendered-
# template grep gate — see Makefile `user-data-contract` / the CI lint job —
# which checks the real rendered script, a stronger test than a mocked plan.)
# -----------------------------------------------------------------------------
run "baseline_secure_plan" {
  command = plan

  # Make the RDS + EFS ARNs known at plan so the backup-selection set has a
  # known length (a set of unknown strings has unknown length).
  override_resource {
    target          = aws_db_instance.this
    override_during = plan
    # override_during=plan replaces ALL computed attrs, so master_user_secret
    # must be supplied here too (it's dereferenced [0].secret_arn in several files).
    values = {
      arn                = "arn:aws:rds:us-east-1:123456789012:db:ccc-wiki-db"
      address            = "ccc-wiki-db.example.rds.amazonaws.com"
      master_user_secret = [{ secret_arn = "arn:aws:secretsmanager:us-east-1:123456789012:secret:rds!mock-AbCdEf" }]
    }
  }
  override_resource {
    target          = aws_efs_file_system.this
    override_during = plan
    values          = { arn = "arn:aws:elasticfilesystem:us-east-1:123456789012:file-system/fs-0123456789abcdef0" }
  }

  # ---- TF-002 (positive side): CIDRs populate the prefix list 1:1 ----
  assert {
    condition     = length(aws_ec2_managed_prefix_list.vpn.entry) == 2
    error_message = "Each supplied VPN CIDR must become exactly one managed-prefix-list entry."
  }

  # ---- TF-001 / SEC-001: NO open-CIDR ingress anywhere. ALB ingress is the prefix list;
  #      app/db/efs ingress are SG-referenced. Every ingress rule must have cidr_ipv4 == null. ----
  assert {
    condition = alltrue([
      aws_vpc_security_group_ingress_rule.alb_https.cidr_ipv4 == null,
      aws_vpc_security_group_ingress_rule.alb_http_redirect.cidr_ipv4 == null,
      aws_vpc_security_group_ingress_rule.app_from_alb.cidr_ipv4 == null,
      aws_vpc_security_group_ingress_rule.db_from_app.cidr_ipv4 == null,
      aws_vpc_security_group_ingress_rule.efs_from_app.cidr_ipv4 == null,
    ])
    error_message = "SEC-001: no ingress rule may use an open/literal CIDR — ALB via prefix list, others via SG reference only."
  }

  # (The ALB rules' prefix_list_id is a computed reference — unknown at plan — so we
  #  assert the *absence* of an open CIDR above; the prefix-list entry-count asserts
  #  prove the list itself is populated from the VUIT CIDRs.)

  # ---- TF-010 / SEC-011: SG segmentation. app/db/efs ingress carry neither a CIDR
  #      nor a prefix list (both null/known at plan) -> the only remaining source is a
  #      referenced security group. That's the defense-in-depth tiering. ----
  assert {
    condition = alltrue([
      aws_vpc_security_group_ingress_rule.app_from_alb.cidr_ipv4 == null,
      aws_vpc_security_group_ingress_rule.app_from_alb.prefix_list_id == null,
      aws_vpc_security_group_ingress_rule.db_from_app.cidr_ipv4 == null,
      aws_vpc_security_group_ingress_rule.db_from_app.prefix_list_id == null,
      aws_vpc_security_group_ingress_rule.efs_from_app.cidr_ipv4 == null,
      aws_vpc_security_group_ingress_rule.efs_from_app.prefix_list_id == null,
    ])
    error_message = "SEC-011: app<-alb, db<-app, efs<-app ingress must reference a security group (no CIDR, no prefix list)."
  }

  # ---- TF-003 / SEC-003: TLS posture ----
  assert {
    condition     = can(regex("^ELBSecurityPolicy-TLS13", aws_lb_listener.https.ssl_policy))
    error_message = "SEC-003: HTTPS listener must use a TLS 1.3/1.2 SSL policy."
  }
  assert {
    condition = (
      aws_lb_listener.http_redirect.default_action[0].type == "redirect" &&
      aws_lb_listener.http_redirect.default_action[0].redirect[0].port == "443" &&
      aws_lb_listener.http_redirect.default_action[0].redirect[0].status_code == "HTTP_301"
    )
    error_message = "SEC-003: port 80 must 301-redirect to HTTPS/443."
  }
  assert {
    condition     = aws_lb.this.drop_invalid_header_fields == true
    error_message = "SEC-003: ALB must drop invalid header fields."
  }

  # ---- TF-004 / SEC-004: encryption at rest on every store ----
  assert {
    # aws_launch_template represents ebs.encrypted as a STRING ("true"/"false") in
    # the provider schema — normalize with tobool before comparing.
    condition     = tobool(aws_launch_template.this.block_device_mappings[0].ebs[0].encrypted) == true
    error_message = "SEC-004: EC2 root EBS volume must be encrypted."
  }
  assert {
    condition     = aws_db_instance.this.storage_encrypted == true
    error_message = "SEC-004: RDS storage must be encrypted."
  }
  assert {
    condition     = aws_efs_file_system.this.encrypted == true
    error_message = "SEC-004: EFS file system must be encrypted."
  }

  # ---- TF-005 / SEC-005: IMDSv2 only ----
  assert {
    condition     = aws_launch_template.this.metadata_options[0].http_tokens == "required"
    error_message = "SEC-005: launch template must require IMDSv2 (http_tokens = required)."
  }

  # ---- TF-006 / SEC-006: RDS is private ----
  assert {
    condition     = aws_db_instance.this.publicly_accessible == false
    error_message = "SEC-006: RDS must not be publicly accessible."
  }

  # ---- TF-008 / SEC-008: no secret material baked in; RDS-managed master password ----
  assert {
    condition     = aws_db_instance.this.manage_master_user_password == true
    error_message = "SEC-008: RDS master password must be RDS-managed (no plaintext password)."
  }
  assert {
    condition     = aws_db_instance.this.password == null
    error_message = "SEC-008: RDS must not set a literal master password."
  }
  # NB: user_data no-baked-secrets content is asserted by the rendered-template
  # grep gate (Makefile `user-data-contract`), which inspects the real rendered
  # script — see tests/lib/render_user_data.sh. (user_data is unknown at plan.)

  # ---- TF-009 / SEC-009: destructive-action guards ----
  assert {
    condition     = aws_lb.this.enable_deletion_protection == true
    error_message = "SEC-009: ALB deletion protection must be enabled."
  }
  assert {
    condition = (
      aws_db_instance.this.deletion_protection == true &&
      aws_db_instance.this.skip_final_snapshot == false
    )
    error_message = "SEC-009: RDS must have deletion protection on and take a final snapshot."
  }

  # ---- TF-011 / OPS-001: Multi-AZ RDS ----
  assert {
    condition     = aws_db_instance.this.multi_az == true
    error_message = "OPS-001: RDS must be Multi-AZ."
  }

  # ---- TF-012 / OPS-002: DB-free health check + adequate first-boot grace ----
  assert {
    condition = (
      aws_lb_target_group.this.health_check[0].path == "/icon.png" &&
      aws_lb_target_group.this.health_check[0].matcher == "200"
    )
    error_message = "OPS-002: ALB health check must hit the DB-free /icon.png expecting 200."
  }
  assert {
    condition = (
      aws_autoscaling_group.this.health_check_type == "ELB" &&
      aws_autoscaling_group.this.health_check_grace_period >= 300
    )
    error_message = "OPS-002: ASG must use ELB health checks with a grace period >= 300s for first boot."
  }

  # ---- TF-013 / OPS-003: AWS Backup covers BOTH RDS and EFS ----
  assert {
    condition     = length(aws_backup_selection.this.resources) == 2
    error_message = "OPS-003: AWS Backup selection must include exactly the RDS instance and the EFS file system."
  }
  assert {
    # rule is a SET (no positional index) — match via a for-expression.
    condition     = anytrue([for r in aws_backup_plan.this.rule : one(r.lifecycle).delete_after == var.backup_retention_days])
    error_message = "OPS-003: backup rule must set a retention (delete_after)."
  }

  # ---- TF-014 / OPS-004: golden-signal + cert-expiry alarms exist and notify SNS ----
  assert {
    condition = (
      aws_cloudwatch_metric_alarm.alb_5xx.metric_name == "HTTPCode_Target_5XX_Count" &&
      aws_cloudwatch_metric_alarm.alb_unhealthy.metric_name == "UnHealthyHostCount" &&
      aws_cloudwatch_metric_alarm.alb_latency.extended_statistic == "p95" &&
      aws_cloudwatch_metric_alarm.rds_storage.metric_name == "FreeStorageSpace" &&
      aws_cloudwatch_metric_alarm.cert_expiry.metric_name == "DaysToExpiry"
    )
    error_message = "OPS-004: ALB 5xx / unhealthy / p95-latency, RDS storage, and cert-expiry alarms must all exist."
  }
  assert {
    condition     = length(aws_cloudwatch_metric_alarm.alb_5xx.alarm_actions) >= 1
    error_message = "OPS-004: alarms must notify the SNS topic."
  }

  # ---- TF-015 / OPS-005: EFS mount target per AZ + access-point ownership 1000:1000 ----
  assert {
    condition     = length(aws_efs_mount_target.this) == var.az_count
    error_message = "OPS-005: EFS must have one mount target per AZ."
  }
  assert {
    condition = (
      aws_efs_access_point.config.posix_user[0].uid == 1000 &&
      aws_efs_access_point.config.posix_user[0].gid == 1000
    )
    error_message = "OPS-005: EFS access point must pin uid/gid to 1000 (BookStack PUID/PGID)."
  }

  # ---- TF-016 / CFG-002: user-data render is asserted by the rendered-template grep
  #      gate (Makefile `user-data-contract` / CI lint job), not here — user_data
  #      interpolates computed values and is unknown at plan. ----

  # ---- TF-017 / CFG-003: image pin is a real version, never floating ----
  assert {
    condition     = strcontains(var.bookstack_image, "latest") == false
    error_message = "CFG-003: bookstack_image must not be :latest."
  }
  assert {
    condition     = can(regex(":v[0-9]+\\.[0-9]+-ls[0-9]+$", var.bookstack_image))
    error_message = "CFG-003: bookstack_image must be pinned to a vXX.YY-lsNNN tag."
  }

  # ====================================================================
  # part 2 additions (Claude Opus) — TF-020..TF-023
  # Extra plan-time invariants the original suite didn't lock down.
  # ====================================================================

  # ---- TF-020 / OPS: subnet fan-out + ALB exposure + single-node sizing ----
  assert {
    condition = (
      length(aws_subnet.public) == var.az_count &&
      length(aws_subnet.private) == var.az_count
    )
    error_message = "TF-020: one public and one private subnet must be planned per AZ (az_count)."
  }
  assert {
    # The ALB is internet-facing on purpose; its REACHABILITY is constrained by the
    # VPN prefix list (asserted in TF-001/002), not by making the LB internal.
    condition     = aws_lb.this.internal == false
    error_message = "TF-020: the ALB is internet-facing (VPN-gated via the prefix list, not 'internal')."
  }
  assert {
    # The whole design is ASG(1) — a single node (see the test plan's 'multi-instance
    # out of scope' note). Pin it so a stray autoscale change is caught at plan.
    condition = (
      aws_autoscaling_group.this.min_size == 1 &&
      aws_autoscaling_group.this.max_size == 1 &&
      aws_autoscaling_group.this.desired_capacity == 1
    )
    error_message = "TF-020: the ASG must stay single-instance (min=max=desired=1) — multi-writer DB contention is out of scope."
  }

  # ---- TF-021 / SEC+OPS: launch-template IMDS endpoint + ALB->app contract ----
  assert {
    condition     = aws_launch_template.this.metadata_options[0].http_endpoint == "enabled"
    error_message = "TF-021: IMDS endpoint must be enabled (with http_tokens=required => IMDSv2-only)."
  }
  assert {
    condition = (
      aws_lb_target_group.this.port == 80 &&
      aws_lb_target_group.this.protocol == "HTTP"
    )
    error_message = "TF-021: the target group must forward to the app on HTTP/80 (TLS terminates at the ALB)."
  }

  # ---- TF-022 / OPS: RDS engine + backup-retention wiring ----
  assert {
    condition = (
      aws_db_instance.this.engine == "mysql" &&
      aws_db_instance.this.engine_version == var.db_engine_version
    )
    error_message = "TF-022: RDS must run the configured MySQL engine/version."
  }
  assert {
    condition     = aws_db_instance.this.backup_retention_period == var.db_backup_retention_days
    error_message = "TF-022: RDS automated-backup retention must track var.db_backup_retention_days (PITR window)."
  }

  # ---- TF-023 / OPS: app log group retention is bounded (not infinite) ----
  assert {
    condition     = aws_cloudwatch_log_group.app.retention_in_days == var.log_retention_days
    error_message = "TF-023: the BookStack log group must set a finite retention (var.log_retention_days), never 0/never-expire."
  }
}

# -----------------------------------------------------------------------------
# TF-003 (cert paths): ACM behavior branches on certificate_arn.
# -----------------------------------------------------------------------------
run "tf003_acm_requested_when_no_cert_supplied" {
  command = plan

  variables {
    certificate_arn = ""
  }

  assert {
    condition     = length(aws_acm_certificate.this) == 1
    error_message = "When certificate_arn is empty, exactly one ACM certificate must be requested."
  }
}

run "tf003_imported_cert_skips_acm" {
  command = plan

  # certificate_arn comes from the file-level variables block (a real ARN) -> no ACM cert.
  assert {
    condition     = length(aws_acm_certificate.this) == 0
    error_message = "When an imported certificate_arn is supplied, no ACM certificate should be requested."
  }
}

# =============================================================================
# part 2 additions (Claude Opus) — input-validation NEGATIVE tests + AZ scaling.
#
# The variable `validation {}` blocks are themselves load-bearing (a bad VPN CIDR
# or cert ARN is the kind of typo that silently breaks access control). expect_failures
# turns "this input MUST be rejected at plan time" into an asserted property — the
# negative twin of the positive runs above.
# =============================================================================

# ---- TF-018 / SEC-002: a malformed VPN CIDR must be rejected at plan, not at apply ----
run "tf018_malformed_vpn_cidr_rejected" {
  command = plan

  variables {
    vpn_ingress_cidrs = ["not-a-cidr"]
  }

  expect_failures = [var.vpn_ingress_cidrs]
}

# ---- TF-019 / SEC-003: a non-ACM certificate_arn must be rejected at plan ----
run "tf019_bad_certificate_arn_rejected" {
  command = plan

  variables {
    certificate_arn = "this-is-not-an-arn"
  }

  expect_failures = [var.certificate_arn]
}

# ---- TF-024 / OPS-005: subnet + EFS mount-target fan-out scales with az_count ----
# Proves the cidrsubnet() math and the data.aws_availability_zones slice scale a
# 3-AZ apply correctly (one public + one private subnet and one EFS mount target
# per AZ) — the parameter-drives-fan-out edge the default az_count=2 never exercises.
run "tf024_scales_with_az_count" {
  command = plan

  variables {
    az_count = 3
  }

  assert {
    condition     = length(aws_subnet.public) == 3
    error_message = "TF-024: az_count=3 must plan 3 public subnets."
  }
  assert {
    condition     = length(aws_subnet.private) == 3
    error_message = "TF-024: az_count=3 must plan 3 private subnets."
  }
  assert {
    condition     = length(aws_efs_mount_target.this) == 3
    error_message = "TF-024: az_count=3 must plan one EFS mount target per AZ."
  }
}
