# Platform administrator security foundation

Platform roles are independent of workspace roles. Being a workspace owner,
admin, or manager does not grant platform access. New accounts receive
`platform_role = 'none'`; no signup, invitation, or public API can assign a
platform role.

## Role matrix

| Permission | `support_admin` | `super_admin` |
| --- | --- | --- |
| Read overview, users, workspaces, domains, campaigns, delivery metadata | Yes | Yes |
| Resend verification and read audit history | Yes | Yes |
| Export audit history | No | Yes |
| Read or add workspace internal notes | No | Yes, append-only and audited |
| Suspend or resume workspace sending | No | Yes, reasoned, recent-MFA protected, audited, and enforced by campaign and transactional send paths |
| Re-check sending-domain DNS | No | Yes, reasoned and audited |
| Disable or re-enable sending domains | No | Yes, reasoned, recent-MFA protected, audited, and enforced at send time |
| Remove a sending domain and its sender identities from Sendwise | No | Yes, reasoned, recent-MFA protected, and audited |
| Resolve duplicate sending-domain ownership | No | Yes; selects one existing workspace record, with reason, recent MFA, and audit |
| View, add, or remove global suppression records | No | Yes; writes are reasoned, recent-MFA protected, and audited |
| Stop a running campaign | No | Yes, reasoned, recent-MFA protected, and audited |
| Manage platform settings, roles, and customer operations | No | Yes |
| Inspect message content | No | Yes, explicit reason and audited endpoint required |
| Export customer contacts or view secrets/password hashes | Never | Never |

The permission map is enforced by Identity's `CanAdmin` policy. Unknown roles
and permissions are denied. Workspace roles are not considered by that policy.
This is the authorization policy matrix; apart from the session, MFA,
and audit endpoints listed below, a permission being present in the matrix
does not imply its capability route is implemented; available routes are listed
below.

## Bootstrap an operator

Apply the normal Identity migrations first. Then, on the host with access to the
Identity database, promote an existing active, verified user. This is an
operator CLI only; there is no role-promotion HTTP route:

```sh
IDENTITY_DATABASE_URL='<identity database DSN>' \
  go run ./cmd/admin-bootstrap \
  -email operator@example.com \
  -role super_admin \
  -reason 'Initial platform operator'
```

The command accepts `support_admin` or `super_admin`, requires a reason, and
atomically changes the role and appends a `platform_role_bootstrapped` audit
record. Re-running it for a user who already has the requested role is a
no-op. It never prints database credentials or password hashes.

No production changes were made as part of this work. An operator must run this
command on the intended server after reviewing the deployment and database.

## MFA and sessions

Admin API calls are routed explicitly by the Gateway to Identity under
`/api/v1/admin/*`. Every request requires a valid, non-revoked bearer session
and a current database check for an active platform role. All admin endpoints
deny by default; workspace ownership is not sufficient.

An admin's refresh session is limited to one hour. Before any admin data API
can be used, the operator must call `POST /api/v1/admin/mfa/setup`, configure
the returned TOTP secret in an authenticator, and securely save the ten
single-use recovery codes. The setup response is shown once. Then call
`POST /api/v1/admin/mfa/verify` with a current six-digit code. TOTP secrets are
encrypted at rest using a key derived from `JWT_SECRET`; recovery codes are
stored as hashes. Five failed MFA codes lock verification for 15 minutes.
Existing login requests are rate limited, and `ADMIN_IP_ALLOWLIST` can further
restrict admin API access to comma-separated IPs/CIDRs. Leave it empty to
disable only the network restriction; role and MFA checks remain mandatory.

Currently implemented security endpoints are:

- `GET /api/v1/admin/session` — platform role, MFA state, and session expiry.
- `POST /api/v1/admin/mfa/setup` and `/verify` — mandatory TOTP enrollment and
  verification.
- `GET /api/v1/admin/permissions` — the current role's effective permissions.
- `GET /api/v1/admin/overview` — live Identity user counts and recent signups,
  Workspace service total, recent audited admin actions, and API-owned domain,
  campaign, delivery-volume, outcome-rate, and database queue-depth metrics.
  Stalwart/NATS/worker health and top-sender rankings are not included.
- `GET /api/v1/admin/audit?limit=50&offset=0&action=&actor_user_id=` —
  filtered, paginated audit records.
- `POST /api/v1/admin/audit/export` — super_admin-only CSV export (up to 1,000
  records per request), filtered by action/actor and requiring an audited
  reason. Export excludes before/after payloads.
- `GET /api/v1/admin/users?search=&status=&limit=50&offset=0` — paginated
  name/email search and status filtering. The endpoint returns account metadata
  only; password hashes, contact lists, message content, and customer secrets
  are excluded. It requires fresh MFA.
- `GET /api/v1/admin/users/{userID}` — the same privacy-safe account metadata
  for one user, requiring fresh MFA.
- `POST /api/v1/admin/users/{userID}/resend-verification` — support_admin and
  super_admin may request a new verification email for pending, unverified
  accounts. Requires a reason, is rate limited, and is audited.
- `POST /api/v1/admin/users/{userID}/verify-email` — super_admin-only manual
  verification of a pending account with a mandatory reason and MFA
  re-verification within five minutes. The user update, `user_verified` outbox
  event, and audit record commit atomically.
- `POST /api/v1/admin/users/{userID}/password-reset` — super_admin-only,
  rate-limited request to queue a standard one-hour password-reset link for an
  active, verified account. Requires a reason and MFA re-verification within
  five minutes. Only the token hash is stored; the one-time token is delivered
  to the account's verified email and never returned to the console.
- `PATCH /api/v1/admin/users/{userID}/status` — super_admin-only activation or
  suspension with a mandatory reason. Requires MFA re-verification within five
  minutes; suspension revokes all active sessions. State change, session
  revocation, and audit record commit atomically.
- `POST /api/v1/admin/users/{userID}/revoke-sessions` — super_admin-only
  session revocation with a mandatory reason and MFA re-verification within
  five minutes. The audit entry is committed atomically with revocation.
- `PATCH /api/v1/admin/users/{userID}/platform-role` — super_admin-only role
  removal (`platform_role: "none"` only), with a mandatory reason and MFA
  re-verification within five minutes. Promotion remains operator-CLI-only;
  self-demotion and removal of the last active super_admin are rejected.
  Successful demotion atomically revokes the target's sessions and appends an
  audit record.
- `GET /api/v1/admin/workspaces?search=&limit=50&offset=0` — paginated
  workspace name/slug search and member counts, fetched from the Workspace
  service through a service-token-protected internal endpoint.
- `GET /api/v1/admin/workspaces/{workspaceID}` — workspace and membership
  metadata (user IDs, workspace roles, and joined timestamps); contacts and
  message content are not exposed.
- `GET /api/v1/admin/workspaces/{workspaceID}/status` and
  `PATCH /api/v1/admin/workspaces/{workspaceID}/status` — workspace sending
  status. The write accepts `expected_status`, `status: "active" | "suspended"`,
  and a typed reason, requires fresh MFA, is audited, uses compare-and-set to
  avoid racing concurrent operator actions, and refuses changes to closed workspaces.
  Suspension blocks campaign-worker processing and transactional API/SMTP
  submission plus transactional-worker delivery; queued work is deferred rather
  than discarded and can resume after re-enabling. The endpoint integration test
  requires a migrated local Postgres database.
- `GET /api/v1/admin/workspaces/{workspaceID}/notes` and
  `POST /api/v1/admin/workspaces/{workspaceID}/notes` — super_admin-only
  append-only internal notes. Adding a note requires a separate typed reason
  and MFA re-verification within five minutes. Notes are stored in the
  immutable Identity audit log; support_admin audit views redact note text and
  note reasons.
- `GET /api/v1/admin/domains?search=&status=&limit=50&offset=0` — paginated
  sending-domain metadata across workspaces, fetched from the API service over
  a service-token-protected internal endpoint. The response omits DKIM keys,
  verification tokens, DNS resolver details, and customer message/contact data.
- `GET /api/v1/admin/domains/{domainID}` — the same privacy-safe metadata plus
  the recorded per-resolver DNS check results for that domain.
- `POST /api/v1/admin/domains/{domainID}/verify` — super_admin-only DNS
  re-check. Requires a typed reason and MFA re-verification within five
  minutes, and records the request in the Identity audit log before calling the
  sending API. Verified domains are also ensured in Stalwart by the existing
  domain verification flow.
- `PATCH /api/v1/admin/domains/{domainID}/disabled` — super_admin-only
  sending-domain disable/enable. Requires `disabled` and a typed reason plus
  fresh MFA; each transition is audited with the workspace ID. Disabling blocks
  campaign sends at the API and worker, and transactional sends at SMTP
  submission and worker validation, without changing DNS or workspace
  verification settings.
- `DELETE /api/v1/admin/domains/{domainID}` — super_admin-only removal of the
  Sendwise domain record and its sender identities. Requires a typed reason and
  fresh MFA; the audit records workspace, domain, owner-conflict, and disabled
  metadata before deletion. This does not alter DNS or shared Stalwart
  configuration.
- `POST /api/v1/admin/domains/{domainID}/resolve-conflict` — super_admin-only
  resolution for duplicate domain records. Supply `rightful_domain_id` from
  among the existing records. In one API database transaction all same-domain
  records are marked conflicted before the selected record becomes the sole
  owner. Requires reason and fresh MFA; audit captures prior owner/conflict IDs
  and the selected workspace.
- `GET /api/v1/admin/campaigns?search=&status=&workspace_id=&limit=50&offset=0`
  — paginated campaign metadata and delivery counts across workspaces. This
  route is available to support_admin and super_admin; it never returns
  campaign subject/body, message IDs, or recipient addresses.
- `GET /api/v1/admin/transactional-messages?search=&limit=50&offset=0`
  — service-owned transactional message metadata search for support_admin and
  super_admin. It returns message/workspace identifiers, status, and timestamp
  only; message content, sender address, and recipient details are excluded.
- `POST /api/v1/admin/campaigns/{campaignID}/stop` — super_admin-only
  cancellation for a stoppable campaign. Requires a typed reason and MFA
  re-verification within five minutes; the request is audited before the
  service-owned API transaction cancels queued deliveries and skips pending
  recipients.
- `GET /api/v1/admin/suppressions?search=&workspace_id=&limit=50&offset=0`
  — paginated global suppression records for super_admin only. Addresses are
  personal data; the route provides no export operation.
- `POST /api/v1/admin/suppressions` — add a workspace-scoped manual suppression.
  Requires `workspace_id`, `email`, a typed reason, and MFA re-verification
  within five minutes. The Identity service audits a digest of the address,
  not its raw value, before calling the sending API.
- `DELETE /api/v1/admin/suppressions/{id}` — remove a suppression record.
  Requires a typed reason and fresh MFA and is audited before the API call.
  This operation does not change the contact's separate consent/status state.

The web console currently exposes an Identity/Workspace-backed overview,
user search/list, account metadata details,
MFA enrollment or verification, verification resend, manual email verification,
password-reset email, and reasoned status/session/role-removal controls under
`/admin/users`; workspace metadata
and membership details under `/admin/workspaces`, including super_admin
suspend/resume controls with a required reason and fresh MFA; and the paginated audit log
at `/admin/audit`, with action and actor filters and a reasoned CSV export for
super_admin. Super admins can add and view append-only workspace notes from
workspace details. Platform admins can search and filter the global sending
domain directory at `/admin/domains`. Admins can open a domain to view its
stored per-resolver DNS results, and super admins can request a reasoned DNS
re-check. Super admins can disable or re-enable sending for a domain from its
detail page; this preserves the DNS record and blocks both campaign and
transactional delivery paths. They can also remove a Sendwise domain record
and its sender identities; DNS and shared Stalwart configuration are not
modified. For duplicate records, super admins can select the rightful existing
owner; the API makes that row the only domain owner transactionally. Platform admins can view and filter global campaign metadata and
delivery counts at `/admin/campaigns`; super admins can stop a campaign there
with a required reason. Super admins can view, add, and remove paginated
workspace-scoped suppression records at `/admin/suppressions`; writes require a
reason and fresh MFA. Message inspection and abuse-queue actions are not yet
available. `/admin/search` searches user email, workspace, domain, campaign,
campaign-delivery IDs, and transactional message IDs by composing the
role-protected metadata endpoints. An account that is not a platform
administrator receives the app's not-found view; the API still independently
checks the role on every request.

Admin audit rows are append-only. A database trigger rejects row updates,
deletes, and truncation in addition to revoking those privileges from `PUBLIC`.
Bootstrap, MFA setup, and successful MFA verification are audited with actor,
role, action, reason, request metadata, and timestamp; failed MFA attempts are
recorded in the Identity authentication audit. Never put secrets, message
bodies, or customer contact data in audit metadata.

## Current implementation boundary

This change provides the separate platform-role model, operator-only
promotion/bootstrap, admin MFA/session enforcement, the permission matrix,
immutable audit storage, explicit Gateway routing, privacy-safe user
search/list/detail, reasoned verification resends, super_admin manual
verification, password-reset email and user status/session/role-removal actions,
workspace metadata search/list and membership detail, super_admin append-only
workspace notes, filtered/reasoned audit export, and global sending-domain
metadata listing/details/re-check plus the privacy-safe campaign directory and
audited campaign cancellation, reasoned global suppression management, and
audited domain disable/enable/removal/owner-conflict resolution. It
does **not** yet provide a complete operational admin overview or full workspace
management (suspension, sending controls, quotas, plan changes, ownership
transfer, or deletion), campaign controls beyond cancellation, delivery operations
beyond campaign aggregate counts, impersonation,
or Playwright admin journeys. Those capabilities must be
delivered in subsequent phases; this partial implementation is not evidence
that the full Super Admin console is complete.

## Verification

The test suite covers role separation, anonymous/workspace-owner denial, MFA
enrollment and enforcement, standard six-digit TOTP behavior, and Gateway
routing. Migration `0004` and the append-only trigger were also verified in an
isolated schema on local Postgres:

```text
$ TEST_DATABASE_URL='<local Identity Postgres URL>' go test ./services/identity -run TestPostgresAdminAuditIsAppendOnly -count=1 -v
=== RUN   TestPostgresAdminAuditIsAppendOnly
--- PASS: TestPostgresAdminAuditIsAppendOnly (0.24s)
PASS
ok  	github.com/khayode/email-platform/services/identity	1.580s
```

No production-server verification is claimed.
