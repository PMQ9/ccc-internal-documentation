# Runbook: break-glass admin access (IdP down)

**Goal:** keep admin access to BookStack when Vanderbilt SSO (the IdP) is unreachable or
misconfigured. BookStack local DB accounts authenticate independently of SAML.

## Why this works

- `AUTH_METHOD=saml2` but `AUTH_AUTO_INITIATE=false` → the local `/login` form stays reachable
  (it does not auto-redirect to the IdP). Local DB users can log in there even with the IdP down.
- We keep **≥2 local admin accounts** so loss/rotation of one credential isn't a full lockout.
- Anonymous **read** keeps working during an IdP outage (public viewing needs no IdP) — the outage
  degrades the site to read-only, which is the desired failure mode.

## Setup (do during Phase 1/2, before go-live)

1. Retrieve the generated passwords:
   ```bash
   aws secretsmanager get-secret-value --secret-id ccc-wiki/breakglass-admins \
     --query SecretString --output text   # JSON: {"admin1":"...","admin2":"..."}
   ```
2. In BookStack (logged in as the seeded admin): Settings → Users → create/confirm **two** admin
   users with those passwords. Change the seeded `admin@admin.com` email + password; store it too.
3. Give them non-personal, durable emails (e.g. `ccc-wiki-admin1@vanderbilt.edu`) so they don't
   collide with SSO-provisioned accounts (which key on `eduPersonPrincipalName`).

## Test BEFORE go-live (mandatory)

With `AUTH_METHOD=saml2` configured, simulate an IdP outage and confirm local login still works:

1. Block egress to the IdP from the instance (temporary SG egress rule, or `/etc/hosts` blackhole
   of the IdP host), OR simply confirm the `/login` form renders and accepts a break-glass admin.
2. Browse to `https://wiki.ccc.vanderbilt.edu/login` → log in as `admin1` → reach Settings.
3. Restore egress.

## During an actual IdP outage

1. Go to `…/login` (NOT the "Vanderbilt SSO" button).
2. Log in with a break-glass admin from Secrets Manager.
3. Communicate that editing is limited to admins until SSO is restored; reading is unaffected.

## After use

Rotate the break-glass password you used (update Secrets Manager + the BookStack user), so the
exposed-in-an-incident credential isn't long-lived.
