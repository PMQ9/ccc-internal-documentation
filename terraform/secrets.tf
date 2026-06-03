############################
# Generated secrets (live in encrypted state + Secrets Manager; never in the launch template)
############################

# BookStack APP_KEY: "base64:" + 32 random bytes. Generated ONCE and stable in state.
# Never rotate after first boot (rotating invalidates all sessions + 2FA).
resource "random_bytes" "app_key" {
  length = 32
}

resource "aws_secretsmanager_secret" "app_key" {
  name        = "${var.name_prefix}/app-key"
  description = "BookStack APP_KEY (do not rotate)"
}

resource "aws_secretsmanager_secret_version" "app_key" {
  secret_id     = aws_secretsmanager_secret.app_key.id
  secret_string = "base64:${random_bytes.app_key.base64}"
}

# Two break-glass local admin passwords (resilience: >=2 accounts that work during IdP outages).
resource "random_password" "breakglass" {
  count   = 2
  length  = 24
  special = false
}

resource "aws_secretsmanager_secret" "breakglass" {
  name        = "${var.name_prefix}/breakglass-admins"
  description = "Local BookStack admin passwords usable when the IdP is down"
}

resource "aws_secretsmanager_secret_version" "breakglass" {
  secret_id = aws_secretsmanager_secret.breakglass.id
  secret_string = jsonencode({
    admin1 = random_password.breakglass[0].result
    admin2 = random_password.breakglass[1].result
  })
}

############################
# VUIT-supplied secrets — created as placeholders; populate during Phase 2 SSO bring-up.
# ignore_changes lets ops update the value out-of-band without Terraform reverting it.
############################
locals {
  saml_secret_names = {
    idp_x509 = "SAML IdP signing cert (bare base64 body, no PEM header/footer)"
    sp_x509  = "SAML SP cert (optional; required only if VUIT demands signed requests)"
    sp_key   = "SAML SP private key (pairs with sp_x509)"
  }
}

resource "aws_secretsmanager_secret" "saml" {
  for_each    = local.saml_secret_names
  name        = "${var.name_prefix}/saml/${each.key}"
  description = each.value
}

resource "aws_secretsmanager_secret_version" "saml" {
  for_each      = local.saml_secret_names
  secret_id     = aws_secretsmanager_secret.saml[each.key].id
  secret_string = "PLACEHOLDER-set-during-VUIT-coordination"
  lifecycle {
    ignore_changes = [secret_string]
  }
}

############################
# Non-secret runtime config (SSM Parameter Store — free; mutable post-launch by ops)
############################
locals {
  ssm_params = {
    "app_url" = var.domain_name == "" ? "" : "https://${var.domain_name}"
    # Launch as standard auth; flip to "saml2" in Phase 2 after VUIT SP registration, then
    # roll the ASG (instance refresh) so user-data re-reads this.
    "auth_method"                  = "standard"
    "saml_idp_entityid"            = "SET-FROM-VUIT"
    "saml_idp_sso"                 = "SET-FROM-VUIT"
    "saml_idp_slo"                 = "SET-FROM-VUIT"
    "saml_email_attribute"         = "mail"
    "saml_display_name_attributes" = "givenName|sn"
    "saml_external_id_attribute"   = "eduPersonPrincipalName"
    # Group->role sync stays OFF until the released attribute is confirmed on the real IdP.
    "saml_user_to_groups"  = "false"
    "saml_group_attribute" = "groups"
  }
}

resource "aws_ssm_parameter" "config" {
  for_each = local.ssm_params
  name     = "/${var.name_prefix}/${each.key}"
  type     = "String"
  value    = each.value == "" ? "UNSET" : each.value
  lifecycle {
    ignore_changes = [value] # ops updates SAML endpoints post-launch
  }
}
