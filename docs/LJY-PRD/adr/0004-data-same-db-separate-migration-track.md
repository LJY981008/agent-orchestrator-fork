# LADO 4. Same database, separate migration track

Date: 2026-10-19
Status: Accepted. Amends the first version of this decision, from the same day, which numbered LADO migrations 9000 and up in upstream's sequence. Hook placement and the fallback consequence were corrected after building step 2.

## Context

LADO needs its own tables (TDD §4) in a database that upstream changes constantly.

Facts at `55b02b848`:

- **Upstream churn.** 186 migration files, latest `0212`, 55 new ones in the last 30 days. They are embedded from `storage/sqlite/migrations/` (`storage/sqlite/db.go:32-33`) and applied with goose `WithAllowMissing` (`storage/sqlite/db.go:357-361`).
- **Rebuilds.** Upstream rebuilds tables by copy and rename. `change_log` has been rebuilt 4 times, each time re-declaring a fixed `event_type` CHECK list and dropping and recreating the CDC triggers it knows about (`0103_review_run_cdc.sql:5-60`).
- **Foreign keys** are enforced (`storage/sqlite/db.go:40`).
- **Retention.** `change_log` is pruned after 7 days or 100,000 rows (`cdc/retention.go:12,22`).
- **Project config.** AO replaces `projects.config` as a whole (`service/project/service.go:754`).
- **Artifacts.** AO keeps 50 versions per path (`artifactversions/capture.go:27-37`) and can delete them on kill (`artifactversions/delete.go:33-60`).
- **Why 9000-range numbers fail:**
  - `migrate_snapshot_test.go:60-68` walks every integer version up to the highest. With 9001 that is about 8,800 extra steps and 9,001 database images in memory.
  - `migrate_burned_versions_test.go:266-274` requires every number in a ledger that upstream appends to with each migration, so it conflicts on most merges.
  - `WithAllowMissing` runs upstream's later migrations after LADO's only on upgraded databases, so fresh and upgraded databases see different orders.

## Decision

- **Same SQLite file.**
- **Separate migration track:**
  - Directory `storage/sqlite/migrations_lado/`, numbered from 0001 inside the track.
  - Applied by a goose v3.27.1 provider with its own version table, `lado_goose_db_version`.
  - On every open, `migrate` in `storage/sqlite/db.go` does four things in order:
    1. drop the LADO CDC triggers, before upstream's first history repair;
    2. run upstream's repairs, migrations and schema reconcile, all of which can rebuild or change upstream tables;
    3. run the LADO track;
    4. recreate the LADO CDC triggers from the list kept in code; migrations never create them (TDD §4.3).
  - sqlc reads both directories, upstream first.
- **Upstream tables are left alone.** LADO uses `lado_*` tables only: no added columns, triggers or CHECK values on upstream tables.
- **References to upstream rows by id, without foreign keys.** Foreign keys connect LADO tables only. Reads handle a missing upstream row.
- **Change events.** They come from triggers on LADO tables and reuse `session_updated` with a payload `kind`, the way `0151_global_agent_model_catalog_cdc.sql:6-28` does.
- **Audit.** `lado_audit_events` is append-only (its own triggers refuse UPDATE and DELETE), never pruned, and hash-chained.
- **Pinned content.** Plans, proposals, knowledge snapshots and setup versions are stored in LADO's blob store under `<state>/lado/blobs/`.
- **Project settings** go in `lado_project_settings`, not in `projects.config`.

## Consequences

- Every database applies migrations in the same order, upstream first.
- No upstream migration test needs an edit, and upstream can rebuild any table without meeting a LADO trigger or foreign key.
- Shared files get small hooks: `storage/sqlite/db.go` (before upstream's repairs and after its schema reconcile) and `backend/sqlc.yaml` (`schema` list).
- An upstream AO binary opening this database ignores the extra tables and the second version table. It does not know the `lado_cdc_*` triggers, though: a pending upstream migration or repair that rebuilds `change_log` by copy and rename, as `0103_review_run_cdc.sql:5-60` does, fails while one exists (TDD §4.3). Before running an upstream binary on a LADO database, drop the `lado_cdc_*` triggers; the fork recreates them on its next open. Step 2 created none, so this applies from the first step whose tables publish change events.
- Backups of `~/.ao` must include `<state>/lado/` together with the database.
