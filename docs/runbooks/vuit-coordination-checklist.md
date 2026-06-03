# Runbook: VUIT coordination checklist

External dependencies that are NOT Terraform-able. Track each to "done" before the relevant phase.

| # | Item | What to request / provide | Feeds | Status |
|---|---|---|---|---|
| 1 | **AWS account + role** | Which VUIT-managed account; IAM role / SSO for `terraform apply`; region | TF init/apply | ☐ |
| 2 | **VPN egress CIDRs** | The VPN's public egress/NAT CIDRs (GlobalProtect). **The access control depends on this.** Maintained as `vpn_ingress_cidrs` → managed prefix list | `sg-alb` | ☐ |
| 3 | **VPC reachability (future)** | Whether campus/VPN can be routed into the VPC (TGW/DX/peering) for the stronger internal-ALB model | security model | ☐ |
| 4 | **Subdomain + DNS** | Reserve `wiki.ccc.vanderbilt.edu` (exact TBD); add CNAME/ALIAS → `alb_dns_name` output; if ACM DNS-validation, add the `acm_dns_validation_records` CNAME(s) | Phase 1/2 | ☐ |
| 5 | **TLS cert** | Confirm whether a `vanderbilt.edu` name *requires* an InCommon/Sectigo cert (→ import into ACM, set `certificate_arn`, **note: imported certs don't auto-renew → cert-expiry alarm**) or ACM-issued is acceptable | Phase 1 | ☐ |
| 6 | **SAML SP registration** | Register the SP using the `saml_sp_urls` output: entityID `…/saml2/metadata`, ACS `…/saml2/acs` (HTTP-POST), SLS `…/saml2/sls`. Exchange SP↔IdP metadata. Provide IdP entityID / SSO / SLO / signing `x509` | Phase 2 | ☐ |
| 7 | **Attribute release (per-SP)** | Release: `mail`, `givenName`+`sn`, a stable `eduPersonPrincipalName` (external ID), and — for role sync — a group attribute (`isMemberOf`/`eduPersonScopedAffiliation`). **Confirm exact released names.** | env mapping | ☐ |
| 8 | **Group provisioning (role sync)** | If using automatic Editor/Admin mapping: have VUIT create a Grouper group released via the group attribute. Until then, promote admins/editors manually (group sync stays OFF) | Decision | ☐ |
| 9 | **AuthnContext / Duo** | Confirm whether forcing `PasswordProtectedTransport` clashes with Duo/MFA; if so set `SAML2_IDP_AUTHNCONTEXT=false` | Phase 2 | ☐ |
| 10 | **Accessibility sign-off** | WCAG 2.2 AA review of the themed deployment (reader + editor surfaces) — run the checklist in [../../deploy/branding/README.md](../../deploy/branding/README.md) | Phase 3 | ☐ |
| 11 | **Brand assets / logo authorization** | Obtain the authorized CCC school lockup (clear-space-correct PNG/SVG, ≥250 px) + favicon from Vanderbilt Brand Communications. Repo ships only a placeholder; marks are for authorized use only | Phase 3 | ☐ |

Where each value lands: IdP endpoints/attribute names → SSM (`/ccc-wiki/saml_*`, `auth_method`);
IdP/SP certs/keys → Secrets Manager (`ccc-wiki/saml/*`); CIDRs → `terraform.tfvars`
(`vpn_ingress_cidrs`); cert → `certificate_arn`; DNS → VUIT zone.
