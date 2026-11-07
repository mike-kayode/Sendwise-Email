# Identity/workspace backfill runbook

The backfill copies users, organizations, and memberships from the Identity and
Workspace databases into the legacy database. It is idempotent by ID. When an
Identity email is already held by another legacy user ID, the process re-keys
that legacy user and its references when safe; otherwise it records the clash
in `backfill_user_id_map` and skips the user.

## Before running

1. Confirm the target database is the intended environment and that the
   Identity, Workspace, and legacy database URLs all refer to the same
   deployment environment.
2. Take a database backup before the write run. For a Compose deployment, run:

   ```sh
   ./scripts/deploy.sh backup
   ```

   The deploy script stores compressed dumps for the legacy, Identity, and
   Workspace databases under `backups/<timestamp>/`.
3. Use a dedicated operator environment or secret manager for database URLs.
   Do not put credentials in shell history or commit them to `.env`.

## Inspect the proposed changes

Set `IDENTITY_DATABASE_URL`, `WORKSPACE_DATABASE_URL`, and
`LEGACY_DATABASE_URL`, then run:

```sh
go run ./cmd/backfill --dry-run
```

The dry run opens a read-only transaction for legacy catalog inspection and
does not write to any database. It prints each conflicting email, whether the
identity ID is free or occupied, and (for proposed re-keys) the tables and
columns that would be updated. It also lists UUID columns named `user_id`,
`*_user_id`, `owner_id`, `created_by`, or `actor_id` that have no foreign key to
`users(id)`; those columns are included in re-keying, not silently ignored.

Review every `outcome=skip` and every unconstrained reference column before
proceeding. A runtime constraint or trigger can still make a proposed re-key
fail; that user is rolled back to its original state and recorded in
`backfill_user_id_map`.

## Run and verify

After reviewing the dry-run output:

```sh
go run ./cmd/backfill
```

The command reports counts of projected users, organizations, memberships,
and skipped rows. Verify row counts and unresolved clashes using read-only SQL:

```sql
SELECT count(*) FROM users;
SELECT count(*) FROM organizations;
SELECT count(*) FROM organization_members;
SELECT identity_user_id, legacy_user_id, email, reason
FROM backfill_user_id_map
ORDER BY email;
```

Compare counts with the pre-run backup and source databases. Confirm the
application can retrieve lists and create contacts for an affected identity
account. Run the backfill a second time; counts should remain stable and no
duplicate users, organizations, or memberships should appear.

## Roll back

The application changes run transactionally within the legacy database, but a
completed run is not automatically reversible. If verification fails, stop
application writes and restore the backups taken immediately before the run.
For a Compose deployment, inspect `backups/latest` and then use the explicit
destructive restore command:

```sh
./scripts/deploy.sh rollback --yes
```

This restores the legacy, Identity, and Workspace database dumps and restarts
the stack. Confirm the selected backup is the one created before the backfill;
the rollback command replaces current database contents.
