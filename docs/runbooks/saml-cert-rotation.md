# Runbook: SAML certificate rotation

Two certs matter for SSO. Either expiring silently breaks **all** logins at once (anonymous read is
unaffected; break-glass local admin still works — see [break-glass-admin.md](break-glass-admin.md)).

## 1. IdP signing certificate (Vanderbilt's — the silent killer)

We pin the IdP signing cert in `SAML2_IDP_x509` (Secrets Manager `ccc-wiki/saml/idp_x509`) with
`SAML2_AUTOLOAD_METADATA=false` (no per-login remote fetch — a resilience choice). The cost: when
VUIT rotates their IdP signing cert, our pinned copy goes stale and SSO breaks until we update it.

**Stay ahead of it:**
- Subscribe to VUIT's IdP metadata/rotation notices; ask for advance notice of signing-cert changes.
- The `ccc-wiki-cert-expiry` alarm covers the *TLS* cert, not the IdP cert. Track the IdP cert's
  expiry separately (calendar reminder from the metadata `validUntil`).

**Rotate:**
1. Get the new IdP signing cert (bare base64 body, no `-----BEGIN/END-----`, no newlines).
2. Update the secret:
   ```bash
   aws secretsmanager put-secret-value --secret-id ccc-wiki/saml/idp_x509 --secret-string '<bare-base64>'
   ```
3. Roll the ASG so user-data re-reads it: `aws autoscaling start-instance-refresh --auto-scaling-group-name ccc-wiki-asg`.
4. Test an SSO login (and confirm break-glass still works).

> Alternative posture: set `SAML2_AUTOLOAD_METADATA=true` so cert rollover is picked up
> automatically from IdP metadata — accept this **only** with a fetch timeout in mind (per-login
> remote dependency). We default to pinned + alarmed; switch if VUIT rotates frequently.

## 2. SP certificate (ours — only if VUIT requires signed requests)

If VUIT requires signed Authn requests/assertions, we present `SAML2_SP_x509` + `SAML2_SP_x509_KEY`
(`ccc-wiki/saml/sp_x509`, `ccc-wiki/saml/sp_key`). To rotate:
1. Generate a new self-signed SP keypair; update both secrets.
2. Provide the new SP cert to VUIT (update SP metadata) — coordinate the cutover so the IdP trusts
   the new cert before you start presenting it.
3. Roll the ASG; test login.

## 3. TLS certificate (ALB)

- **ACM-managed:** auto-renews; the `ccc-wiki-cert-expiry` alarm is a backstop.
- **Imported (Sectigo/InCommon):** does **not** auto-renew. Before the alarm fires (<30 days),
  obtain a renewed cert, `aws acm import-certificate --certificate-arn <arn> ...` (re-import to the
  same ARN to keep the listener binding), and verify HTTPS.
