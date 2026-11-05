# CSV Import Investigation

**Status:** PARTIALLY IMPLEMENTED  
**Updated:** 2026-11-05

## Finding

The reported `test3@pipsleague.com` failure could not be tied to a specific local
import record. The address was not present in the accessible API or worker
container logs. A read-only local database check was blocked because the
environment denied Docker socket access to `docker compose exec`; no test
account credentials or production database access were available. The account-
specific failure is therefore **BLOCKED**, not claimed as reproduced or fixed.

The application code did contain reproducible, general CSV workflow defects:

1. Selecting a file never advanced the dialog from upload to column mapping, so
   the mapping and preview steps were unreachable.
2. The browser split CSV text on commas and newlines, corrupting quoted commas
   and multiline values.
3. The browser serialized its header-to-field selections in the reverse shape
   from the API's strict `CSVColumnMapping` contract. Headers such as
   `Email Address` also caused unknown JSON fields and were rejected.
4. The frontend exposed phone/company/job title mappings that were not encoded
   as backend custom-field mappings.
5. The import endpoint did not accept a target list, and the worker only created
   contacts; imported records could not join the selected audience list.
6. Headerless files lost their first record because the parser treated it as a
   header.

The checked-in `email.csv` sample is a headerless, single-column address list.
That format is now detected and previewed without discarding its first row; it
does not identify the reported account's upload.

## Changes implemented

- Added CSV preview decoding for UTF-8/UTF-8 BOM and Windows-1252, quote-aware
  parsing, comma/semicolon delimiter detection, multiline fields, and
  headerless-file preview.
- Connected the upload, mapping, review, and import steps; enforced one mapping
  per contact field and generated the API's explicit `email`, `first_name`,
  `last_name`, `custom_fields`, and `has_header` fields.
- Added optional list selection. The API verifies that the list belongs to the
  authenticated organization; the worker creates idempotent list memberships
  for valid imported rows, including existing contacts, without overwriting
  their contact details.
- Added an additive migration for the selected list reference. It can be rolled
  back after stopping new imports by dropping
  `contact_imports_list_idx` and then `contact_imports.list_id`; this discards
  the target-list metadata and should only be done after queued imports are
  drained or explicitly cancelled.
- Added parser, mapping, encoding, and list-membership regression tests.
- Corrected stale frontend test expectations for existing login error text so
  the unit suite reflects current application behavior.

## Verification

| Check | Status | Evidence |
|---|---|---|
| Go CSV parser and import/API/worker unit packages | FIXED AND VERIFIED | `CGO_ENABLED=0 go test ./internal/contacts ./internal/worker ./internal/api ./internal/db` |
| PostgreSQL import API and worker integration tests | BLOCKED | `TEST_DATABASE_URL` is unset; those tests require PostgreSQL and were skipped |
| Frontend typecheck | FIXED AND VERIFIED | `npm --prefix apps/web run typecheck` |
| Frontend unit tests | FIXED AND VERIFIED | `npm --prefix apps/web test` (10 tests passed) |
| Full Go test suite | BLOCKED | Gateway test listener failed with sandbox `operation not permitted`; other package tests passed |
| Production frontend build | BLOCKED | Turbopack could not create a process/bind a port under the environment policy |
| Local API health | PASS | `http://127.0.0.1:8080/healthz` and `/readyz` returned HTTP 200 |
| Reported address in available logs | BLOCKED | No matching lines in accessible API/worker logs; local DB query could not run |

These checks do not establish end-to-end import completion against PostgreSQL,
the running worker, or the reported account. The 16 MiB upload cap and
PostgreSQL-backed temporary payload remain existing design limits; object
storage streaming, import update policies, tag assignment, retries under worker
restart, and downloadable complete row-error reports still need separate
end-to-end verification.
