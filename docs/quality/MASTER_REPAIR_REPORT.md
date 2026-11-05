# Sendwise Repair Report

**Status:** PARTIALLY IMPLEMENTED  
**Updated:** 2026-11-05

## Scope and evidence

This report records work completed in the current repair pass. It is not a
production-readiness sign-off: no account-specific production access, live
PostgreSQL integration run, end-to-end browser session, or real delivery test
was available.

## CSV import

**Status:** PARTIALLY IMPLEMENTED

Fixed the unreachable frontend mapping/preview workflow, corrected its
frontend/backend mapping contract, improved CSV format handling, added
headerless-file support, and connected optional list selection through the
tenant-scoped API and durable worker job. See
[CSV import investigation](./CSV_IMPORT_INVESTIGATION.md).

The reported `test3@pipsleague.com` could not be verified against a database
record. Accessible API and worker logs had no matching entries, and the local
database check was blocked by the environment's Docker socket policy.

## Authentication and logout

**Status:** PARTIALLY IMPLEMENTED

Added a visible desktop/mobile dashboard Sign out action with progress and
error feedback. Local state is cleared only after the backend logout request
succeeds. The Identity service revokes refresh sessions, but access JWT
middleware does not consult the session revocation state; copied access tokens
remain usable until expiry. See
[authentication and logout verification](./AUTHENTICATION_AND_LOGOUT.md).

## Audience management

**Status:** PARTIALLY IMPLEMENTED

Added tenant-scoped tag rename/delete API routes and connected the dashboard
controls. List and segment pages still have incomplete controls and workflows;
the current segment-create form sends a rules shape rejected by its backend.
See [contacts, lists, tags, and segments](./CONTACTS_LISTS_TAGS_SEGMENTS.md).

## Verification state

- **FIXED AND VERIFIED:** Go CSV parsing, API, worker, and migration unit package
  tests.
- **FIXED AND VERIFIED:** Frontend typecheck and ten frontend unit tests.
- **PARTIALLY IMPLEMENTED:** Dashboard logout control and frontend failure
  handling; immediate access-token invalidation remains open.
- **PARTIALLY IMPLEMENTED:** Tag edit/delete UI and routes; list and segment
  workflows remain incomplete.
- **BLOCKED:** PostgreSQL integration tests (`TEST_DATABASE_URL` is unset).
- **BLOCKED:** Full Go suite's gateway test could not bind a local test port
  under the environment policy.
- **BLOCKED:** Next.js production build could not create a process/bind a port
  under the environment policy.
- **PASS:** Local legacy API health and readiness returned HTTP 200.
- **BLOCKED:** Account-specific import history/records; no available database
  credentials and Docker exec was denied.

## Remaining release work

The complete platform task remains open. Lists, tags, segments, forms,
automations, domains/DNS, campaigns/delivery, authentication/logout, tenant
isolation, all dashboard routes, live migrations, production infrastructure,
and end-to-end journeys have not been comprehensively repaired or verified in
this pass. The existing [verification report](./verification-report.md) and
[development status](../DEVELOPMENT_STATUS.md) describe additional known
failures.
