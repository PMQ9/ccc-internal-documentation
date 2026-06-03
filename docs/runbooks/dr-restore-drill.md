# Runbook: disaster-recovery restore drill

**An untested backup is not a backup.** Run this once before go-live and at least annually.
BookStack state spans **two** stores that must be restored to a consistent point **together**:
the **RDS database** (entities, users, revisions, metadata) and the **EFS volume** (images +
attachments). Restoring one without the other leaves dangling references.

Targets: **RTO ≤ 1h, RPO ≤ 24h** (daily AWS Backup + RDS PITR). Record the actuals each drill.

## What backs up what

- **RDS:** automated backups + PITR (35-day window) **and** AWS Backup recovery points.
- **EFS:** AWS Backup recovery points (the `ccc-wiki-vault`). Versioning is not a substitute.

## Drill (in a scratch environment — do NOT overwrite prod)

1. **Pick a recovery point.** Note an RDS PITR timestamp (or snapshot) and the matching EFS
   recovery point from `ccc-wiki-vault` closest to the same time.
2. **Restore RDS** to a new instance:
   ```bash
   aws rds restore-db-instance-to-point-in-time \
     --source-db-instance-identifier ccc-wiki-db \
     --target-db-instance-identifier ccc-wiki-db-dr \
     --restore-time <ISO8601> --db-subnet-group-name ccc-wiki-db-subnets \
     --vpc-security-group-ids <sg-db> --no-publicly-accessible
   ```
3. **Restore EFS** from AWS Backup to a new file system (Backup console / `start-restore-job`),
   targeting the same recovery time.
4. **Stand up a scratch app** (a t4g.small from the launch template) pointed at the restored RDS
   endpoint and the restored EFS id (override the user-data env / SSM in the scratch context).
5. **Verify together:**
   - A known page renders, with its **images and attachments** loading (proves DB↔EFS consistency).
   - **Revision history** is intact on that page.
   - A **break-glass admin** can log in (proves the users table + auth path).
6. **Record** the restore-point timestamps, actual RTO, and actual RPO. Tear down the scratch env.

## Local rehearsal (connor-server)

The local stack rehearses the *together* property end-to-end (DB dump + media archive → wipe →
restore both → fingerprint matches). See [../../deploy/local/README.md](../../deploy/local/README.md) §5.
This was validated green; the AWS drill confirms it against real RDS PITR + AWS Backup EFS.

## If you ever restore prod for real

Restore RDS and EFS to the **same** recovery time, repoint the app (RDS endpoint via SSM/redeploy;
EFS id via launch template), roll the ASG, then run the verification in step 5 before announcing
recovery.
