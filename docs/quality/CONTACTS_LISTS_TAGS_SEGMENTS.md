# Contacts, Lists, Tags, and Segments

**Status:** PARTIALLY IMPLEMENTED  
**Updated:** 2026-11-05

## Tags

**Status:** PARTIALLY IMPLEMENTED

- Implemented tenant-scoped `PATCH /api/v1/tags/{id}` and
  `DELETE /api/v1/tags/{id}` endpoints, with role checks, duplicate-name
  handling, color validation, and not-found responses.
- Connected the dashboard Edit and Delete actions. Tag deletion removes tag
  assignments through the existing foreign key cascade and does not delete
  contacts.
- The new API integration test covers create, rename, cross-organization
  deletion denial, and preserving contacts after tag removal. PostgreSQL-backed
  execution is **BLOCKED** because `TEST_DATABASE_URL` is unavailable.
- Contact assignment/removal and bulk tag workflows remain incomplete.

## Lists

**Status:** PARTIALLY IMPLEMENTED

The API and dashboard support listing, creating, and deleting lists; list
membership is used by import and campaign workflows. Editing list details,
list detail/contact management, export, and the dashboard's visible Manage
action are not completed or end-to-end verified.

## Segments

**Status:** PARTIALLY IMPLEMENTED

The Go segment compiler validates condition fields/operators and binds values
as SQL parameters. The current dashboard create form submits `rules: []`, while
the API expects a non-empty rules object; its Edit and Add condition controls
do not implement a functional builder. Preview counts, dynamic list/tag/
engagement predicates, update, and end-to-end campaign/automation use remain
unverified or incomplete.

## Contacts and CSV imports

**Status:** PARTIALLY IMPLEMENTED

Contact list/search/export and basic CRUD exist. The CSV import upload,
mapping, preview, normalized parser, selected-list assignment, and worker
handling changes are described in the
[CSV import investigation](./CSV_IMPORT_INVESTIGATION.md). Row-level database
integration and the reported account journey remain **BLOCKED**.

## Verification

- Go API/worker/parser packages: **FIXED AND VERIFIED** by unit-package tests.
- Tag API integration test: **BLOCKED** by missing PostgreSQL test configuration.
- Frontend typecheck and unit suite: **FIXED AND VERIFIED**.
- Browser journeys for contact/list/tag/segment management: **BLOCKED**; no
  authenticated browser session was available.
