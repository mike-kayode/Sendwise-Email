**Decision:** Parse environment values strictly and reject invalid durations, booleans, integers, unknown environment names, and unsafe staging/production secrets/settings during startup. Development-only defaults must never be injected by a production Compose configuration.
**Consequence:** Staging/production deployments must provide a strong `JWT_SECRET`, authenticated SMTP credentials, verified-domain enforcement, and STARTTLS. Only development/test may use a documented development-only secret and plaintext local SMTP.

## ADR-011: Enforce workspace roles at API route boundaries
**Decision:** Authenticate tenant routes first, then apply role authorization to mutations. Audience writes require owner/admin/manager; campaign editing and template creation also permit authors; campaign delivery controls require owner/admin/manager; sending-domain administration requires owner/admin. Authenticated workspace roles may read tenant data.
**Rationale:** Authentication proves a user identity and workspace context, but does not grant every workspace capability. Central route middleware keeps authorization consistent and testable.
**Consequence:** Role changes take effect after short-lived access tokens expire unless token revocation is added; high-risk operations remain limited to manager-level roles.

## ADR-012: Bound password-hash verification parameters
**Decision:** Verify only the platform's configured Argon2id version, memory, iteration, parallelism, salt length, and key length. Reject unknown or malformed encodings before invoking Argon2.
**Rationale:** Hash parameters are stored data and must not be trusted as caller-controlled work factors; unbounded values can turn login into a memory/CPU denial of service.
**Consequence:** Existing password hashes outside the current profile require an explicit migration path before parameters are changed.
# Architecture Decision Records

## ADR-001: Modular monorepo, not premature microservices
**Decision:** Start with a modular monorepo (one Go API binary + one Go worker binary + one Next.js app) with clear package/service boundaries. Extract independently deployable services only when operationally justified.
**Rationale:** Operating dozens of network services before product-market fit adds operational complexity without benefit. Clear contracts (package interfaces, Postgres tables, NATS subjects) allow safe extraction later.
**Consequence:** Services share a database and NATS cluster initially; extraction requires introducing service boundaries over existing contracts.

## ADR-002: Stalwart as initial SMTP transport, abstracted behind Go interface
**Decision:** Use self-hosted Stalwart SMTP as the initial transport. The campaign/delivery layer depends only on `delivery.Transport`, not on Stalwart directly.
**Rationale:** Avoids building a full SMTP server from day one while keeping the platform independent of commercial delivery APIs. The interface makes a custom Go SMTP transport a drop-in replacement.
**Consequence:** DKIM signing is delegated to Stalwart; SMTP acceptance is recorded as `submitted`, not `delivered`; bounce/complaint events must be processed separately.

## ADR-003: PostgreSQL as source of truth; NATS JetStream as work distribution
**Decision:** Delivery jobs live in PostgreSQL (`delivery_jobs`, `campaign_recipients`). NATS JetStream distributes jobs to workers; it is not the source of truth.
**Rationale:** Job durability and exactly-once semantics require the relational store. NATS provides durable, replayable distribution with backoff and dead-letter behavior.
**Consequence:** Worker restarts recover by re-publishing `queued`/`deferred` jobs whose `next_attempt_at <= now()` and have no in-flight NATS message.

## ADR-004: Argon2id password hashing + short-lived JWT access tokens
**Decision:** Passwords hashed with Argon2id (64MB memory, 1 iteration, 4 parallelism). Access tokens are JWT HS256 with 15-minute TTL. Refresh/rotation and MFA are specified for Phase 7.
**Rationale:** Argon2id is the current OWASP-recommended password hash. Short-lived JWTs limit theft impact without requiring a token blacklist in Phase 1.
**Consequence:** No server-side sessions in Phase 1; token rotation and refresh tokens added later.

## ADR-005: Tenant isolation via scoped queries + RLS where practical
**Decision:** Every access token carries `organization_id`. All repository queries scope by `organization_id`. PostgreSQL Row-Level Security policies are documented for adoption when the app connects with a non-owner role.
**Rationale:** Application-level scoping is immediate and testable; RLS adds defense-in-depth.
**Consequence:** Client-supplied organization IDs are never trusted without membership/permission checks.

## ADR-006: Structured JSON email designs rendered server-side
**Decision:** Campaigns store designs as versioned structured JSON blocks (`heading`, `paragraph`, `button`, `image`, `divider`, `spacer`, `footer`, `html`). The Go renderer produces responsive HTML + plain text; MJML equivalence is achieved through the block-to-HTML mapping.
**Rationale:** Structured designs are sanitizable, versionable, and client-safe. Raw HTML is escaped/sanitized; arbitrary scripts are rejected.
**Consequence:** A visual drag-and-drop editor produces the same JSON the renderer consumes.

## ADR-007: Signed, expiring tracking/unsubscribe tokens
**Decision:** Open and click tracking use signed JWT tokens embedded in redirect URLs and 1x1 pixels. Unsubscribe/preference links use signed JWTs with 365-day TTL. The original target URL is inside the signed token, never an unsigned query parameter.
**Rationale:** Prevents open redirect, token forgery, and link tampering; supports one-click unsubscribe (RFC 8058).
**Consequence:** Tokens are opaque to the recipient; signature verification is required before recording events or redirecting.

## ADR-008: No localStorage as system of record
**Decision:** The Next.js frontend reads all data from the Go API via TanStack Query. `localStorage` is used only for the access token (short-lived) and transient UI preferences.
**Rationale:** Satisfies the "real backend persistence" requirement; enables server-side rendering and cache invalidation.
**Consequence:** Auth requires an API round-trip; token expiry is handled with 401 responses.

## ADR-009: Conservative sending defaults + domain verification before sending
**Decision:** Default sending rate 20 emails/sec, max 5 delivery attempts, verified sender domains required before campaigns can send (enforced when `REQUIRE_VERIFIED_DOMAIN=true`). SPF/DKIM/DMARC DNS verification via actual DNS lookups.
**Rationale:** Protects sender reputation and prevents abuse from day one.
**Consequence:** New sending IPs need warm-up, PTR, and reputation monitoring; documented in operations guide.

## ADR-010: Fail closed on invalid runtime configuration
**Decision:** Parse environment values strictly and reject invalid durations, booleans, integers, and unsafe production secrets/settings during startup. Development-only defaults must never be injected by the production Compose configuration.
**Rationale:** Silent fallback can start a worker with unintended rates, retry limits, or credentials. Startup validation is safer than diagnosing a misconfigured live sender.
**Consequence:** Production deployments must provide a strong `JWT_SECRET`, authenticated SMTP credentials, and verified-domain enforcement. Local development may use a documented development-only secret and plaintext local SMTP.
