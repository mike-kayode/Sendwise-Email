# Security Architecture

This is the target control set, not a claim that every control exists. Current source-level findings are in [the security audit](./SYSTEM_ARCHITECTURE_AND_DEVELOPMENT_AUDIT.md#12-security-and-data-protection-audit).

## Trust boundaries

1. Public browser/API traffic terminates TLS at the edge gateway. Internal services are not directly Internet-addressable.
2. Gateway identity is not authorization: each service validates identity, organization membership, action and resource scope.
3. Each service DB credential accesses only its owned schema/database. Migration identities are separate from runtime identities.
4. NATS messages are authenticated, authorized by subject, schema-validated and treated as untrusted input.
5. External SMTP/DSN/FBL and billing-provider callbacks are untrusted until signatures, timestamps, replay windows and message correlation are verified.
6. Operator/admin privilege is separate, MFA-protected, least-privileged and audited.

## Identity, sessions and credentials

- Argon2id or a reviewed modern password hash with bounded parameters; generic login errors and login throttling.
- Short-lived access tokens and rotating refresh sessions with reuse detection, revocation and device/session inventory.
- MFA support for operators and customer accounts according to policy; recovery codes hashed and auditable.
- Verification, reset, invitation and one-time tokens are high entropy, stored hashed, single-use, short-lived, purpose-bound and rate-limited.
- API keys are shown once, stored hashed, scoped, expiry/revocation aware, and never confused with user JWTs.
- Browser credential storage migration should consider secure HttpOnly cookies and CSRF controls; localStorage is an XSS token-theft exposure.

## Authorization and tenant boundaries

- Define permission/action matrix by service; deny by default.
- Validate current organization membership and resource ownership. Never accept tenant ID alone as authorization.
- Add tenant-aware composite FKs and RLS defense-in-depth where feasible. Test using actual app DB roles, including `SET LOCAL` lifecycle across pooled connections.
- Revoke stale memberships/tokens promptly; cache invalidation must be bounded and observable.
- Redact personal data and avoid leaking whether an account/email exists where enumeration matters.

## Web, API and service controls

- TLS, HSTS, CSP, secure cookie flags, MIME sniffing protection, strict CORS allowlist, body size/timeouts.
- CSRF protection for cookie-authenticated state-changing requests; avoid state-changing GET endpoints.
- Input schema validation, output encoding, HTML sanitization, SSRF/file upload controls, CSV formula mitigation.
- Per-IP/account/tenant rate limits and abuse detection for login, registration, public forms, exports, password reset and provider callbacks.
- Internal service authentication, mTLS/workload identity, bounded timeouts, circuit breakers and no ambient DB credentials.
- Webhook signatures use constant-time comparison plus timestamp/replay protection and secret rotation.
- Structured logs exclude passwords, bearer tokens, API keys, raw one-time tokens, SMTP credentials and sensitive payloads.

## Data protection and operations

- Central secret manager; rotation procedures and least-privileged access.
- Encryption in transit for database, NATS, SMTP and internal service calls; storage encryption/managed keys for sensitive data.
- Data classification, retention, export/delete requests, legal hold and consent evidence policies.
- Automated dependency/SBOM scanning, static analysis, secret scanning and security review for auth/tenant/data path changes.
- Incident response, abuse suspension, notification and credential/key rotation runbooks.

## Current release blockers

- Caddy is configured HTTP-only; inbound bounce/complaint routes are unsigned; API key authentication/scopes are incomplete; verification link is disconnected; auth tokens are stored in localStorage; CSV export needs spreadsheet safety; no DB least-privilege/RLS policy is present; there is no verified CI/dependency scan pipeline.

