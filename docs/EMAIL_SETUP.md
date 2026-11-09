# Transactional Email Setup

Identity uses Stalwart over SMTP for transactional email. `log` is for
development only. Mailpit is useful for local UI tests, but a message accepted
by Mailpit is not proof of delivery by Stalwart or an external provider.

## SMTP settings

Configure these values for Identity:

```dotenv
MAIL_PROVIDER=smtp
SMTP_ADDR=stalwart:587
SMTP_HOST=mail.example.com
SMTP_TLS_MODE=starttls
SMTP_USERNAME=noreply@example.com
SMTP_PASSWORD=<secret>
SMTP_FROM=noreply@example.com
```

`SMTP_TLS_MODE` is `none`, `starttls`, or `tls`. `starttls` is the recommended
mode on port 587; `tls` is for implicit TLS, commonly port 465. Production
Identity rejects `none` when SMTP is the selected provider. With SMTP selected,
`GET /readyz` performs EHLO and authenticates without sending a message. The
SMTP error response code/text and recipient domain are logged; passwords are
never logged.

Optional platform-mail failover is configured only with both
`PLATFORM_RESEND_API_KEY` and `PLATFORM_FROM_EMAIL`. When either is unset,
Identity uses Stalwart only. Campaign delivery never uses this fallback.

## Stalwart version and configuration API

Compose pins the Stalwart image by digest; that image reports **Stalwart
v1.0.3**. Provisioning uses the version's JMAP management API at `/jmap/`, not
`stalwart-cli` or guessed REST endpoints. The API methods and object fields
used here are documented in Stalwart's [management API
overview](https://stalw.art/docs/management/), [Domain
reference](https://stalw.art/docs/ref/object/domain),
[NetworkListener](https://stalw.art/docs/ref/object/network-listener),
[Certificate](https://stalw.art/docs/ref/object/certificate),
[DkimSignature](https://stalw.art/docs/ref/object/dkim-signature),
[MtaDeliverySchedule](https://stalw.art/docs/ref/object/mta-delivery-schedule),
[MtaVirtualQueue](https://stalw.art/docs/ref/object/mta-virtual-queue), and
[SystemSettings](https://stalw.art/docs/ref/object/system-settings) references.
The installed version is also exercised by the real-container integration test
in CI.

The local mode provisions a disposable Compose instance with a generated local
certificate. It is not evidence of public DNS or external delivery:

```sh
export STALWART_DOMAIN=sendwise.test
export STALWART_HOSTNAME=mail.sendwise.test
export STALWART_ADMIN_USERNAME=admin
export STALWART_ADMIN_PASSWORD='<local administrator password>'
export STALWART_SUBMIT_PASSWORD='<local submission password>'
./scripts/provision-stalwart.sh --local
```

The live mode uses a mounted certificate instead of Stalwart ACME. Caddy
already owns public ports 80 and 443 in this deployment, so HTTP-01/TLS-ALPN-01
would require a separate challenge-routing design. Install a certificate for
`STALWART_HOSTNAME` and its matching private key in
`$STALWART_CERTS_DIR/fullchain.pem` and `privkey.pem`; Compose mounts that
directory read-only at `/etc/stalwart/certs`. The provisioner validates the
hostname (including valid wildcard SANs), validity period, and key match before
installing the references through JMAP. Certificate renewal remains the host's
responsibility; rerun the provisioner after renewal.

## Live server runbook

Run these steps on the mail server, never from a developer's workstation pointed
at production. The management API is published only on `127.0.0.1:8081`.

1. Back up the Stalwart data volume and install a valid certificate/key pair in
   the mounted certificate directory. Set the SMTP bind address to `0.0.0.0`
   only if the host firewall allows the intended mail ports.
2. Set the required environment variables in the server's protected
   environment. Never put passwords in the repository, shell history, command
   output, or deployment logs:

   ```dotenv
   STALWART_URL=http://127.0.0.1:8081
   STALWART_ADMIN_USERNAME=<management user>
   STALWART_ADMIN_PASSWORD=<management password>
   STALWART_HOSTNAME=mail.example.com
   STALWART_DOMAIN=example.com
   STALWART_SUBMIT_PASSWORD=<strong submission password>
   SPF_INCLUDE=example.com
   STALWART_BIND_ADDRESS=0.0.0.0
   STALWART_CERTS_DIR=./secrets/stalwart/certs
   ```

   Set `SPF_INCLUDE` to the platform domain whose published SPF record
   authorizes Stalwart. Leave it empty only when every campaign sending
   domain's own MX points to the Stalwart host; otherwise the generated `mx`
   mechanism will not authorize Stalwart's outbound IP.

3. Review the non-mutating plan. It reports DNS placeholders without printing
   any configured credential:

   ```sh
   ./scripts/provision-stalwart.sh --live --dry-run
   ```

4. Apply the idempotent JMAP configuration to the running container:

   ```sh
   ./scripts/provision-stalwart.sh --live
   ```

   This sets the server hostname and platform domain, provisions
   `noreply@example.com`, configures authenticated STARTTLS on 587 and implicit
   TLS on 465, installs the mounted certificate, and applies explicit remote
   queue concurrency/retry limits. It preserves any other hosted tenant
   domains and existing DKIM signatures. The script prints the active platform DKIM
   TXT record alongside the MX, SPF, DMARC and PTR values to publish.
5. Publish the exact records from the script. Add an A/AAAA record for
   `mail.example.com`; MX, SPF, DKIM and DMARC are domain-specific and must not
   be copied from an unrelated domain. Ask the IP provider to set PTR/rDNS to
   `mail.example.com`.
6. Confirm DNS and forward-confirmed reverse DNS:

   ```sh
   dig +short A mail.example.com
   dig +short MX example.com
   dig +short TXT example.com
   dig +short TXT <selector>._domainkey.example.com
   dig +short TXT _dmarc.example.com
   dig +short -x <SERVER_PUBLIC_IP>
   ```

7. Check outbound TCP port 25 from the server and confirm with the provider
   that it is not blocked:

   ```sh
   nc -vz gmail-smtp-in.l.google.com 25
   ```

8. Use the smoke command printed by the provisioner with a controlled
   recipient. It confirms authenticated SMTP submission/acceptance only; check
   the recipient mailbox separately to confirm external delivery.

Compose persists Stalwart state under `stalwart-data`, runs a STARTTLS/implicit
TLS submission healthcheck, exposes SMTP ports 25/465/587 according to
`STALWART_BIND_ADDRESS`, and keeps the admin API bound to `127.0.0.1`. Do not
publish port 8081 through a firewall or reverse proxy.

## Domain sending and DKIM ownership

The Domains page checks each required record directly through Cloudflare
(`1.1.1.1`), Google (`8.8.8.8`), and the authoritative nameservers. It resolves
the closest authoritative zone for the sending domain (including delegated
subdomains), discovers that zone's nameservers once per check, and runs the
public-resolver queries concurrently. DNS responses are not cached by the
application; recursive resolver caches and the record TTL can still delay
propagation. The UI shows each source's result and the check timestamp.

The platform `noreply` domain is signed by Stalwart using its single active RSA
DKIM key. When a workspace verifies a campaign domain, the API adds that domain
to Stalwart with DKIM management set to `Manual`. Sendwise stores the workspace
DKIM private key, signs each campaign in the app, and displays the matching
selector/public-key TXT value on the Domains page. Stalwart hosts the domain to
authorize authenticated submission but does not sign it; the integration test
submits an app-signed message from a hosted tenant domain, checks there is
exactly one DKIM signature, and verifies it against the corresponding TXT
value.

Provisioning configures Stalwart's `MtaStageAuth.mustMatchSender` rule for the
dedicated submission identity only: `noreply@<platform-domain>` may submit for
hosted tenant domains, while all other authenticated identities retain
Stalwart's sender-match requirement. This avoids wildcard mailbox aliases and
does not grant the exception to other accounts. The real-container integration
test exercises this policy with an arbitrary local part at a hosted tenant
domain.

The API uses `STALWART_MANAGEMENT_URL`, `STALWART_ADMIN_USERNAME`, and
`STALWART_ADMIN_PASSWORD` to manage verified domains. In Compose, the API uses
the private service address `http://stalwart:8080`; a host-run API uses
`http://127.0.0.1:8081`. The admin API remains unpublished outside loopback.

The local Stalwart integration test registers through Identity, probes
EHLO/STARTTLS/AUTH, reads the verification message from the real Stalwart
mailbox over JMAP, and verifies the platform DKIM signature against the active
DNS TXT value returned by the management API. The pinned local certificate is
test-only; production SMTP verifies the configured server hostname against
system certificate roots.

## Bounce and complaint collection

Campaign messages use a compact signed VERP envelope sender under
`BOUNCE_DOMAIN`. The API can authenticate and process RFC 3464 DSN and RFC 5965
ARF message bodies at `POST /api/v1/webhooks/stalwart-feedback`; it validates
the webhook HMAC, verifies the VERP token against the persisted tenant-scoped
mapping, and deduplicates the raw report before changing delivery or
suppression state.

**Collection from Stalwart is not yet configured.** No Stalwart event
subscription or bounce-mailbox poller currently forwards real DSNs/ARFs to
that endpoint. Until a collector is deployed and exercised end to end, the
application does not automatically learn about downstream delivery outcomes.
Do not treat the local parser/API tests as proof of bounce collection.

## DNS and delivery limits

For each sending domain, publish the records shown in Sendwise's Domains page:
one SPF TXT record, the app-generated DKIM selector/key, and a DMARC policy.
For the platform receiving/sending domain, use the exact MX/SPF/DKIM/DMARC
records emitted by `--live`. Avoid multiple SPF records. Start DMARC at
`p=none`, review reports, and tighten policy only after every legitimate sender
aligns.

`BOUNCE_DOMAIN` also needs an MX record pointing to a Stalwart host and a
configured mailbox/collector that accepts `bounces+<token>@<BOUNCE_DOMAIN>`.
The current provisioner does not yet configure that inbound route or collector;
publishing only the DNS record will not make campaign bounces flow into Sendwise.

Verify inbound port 25 is reachable when Stalwart is expected to receive mail.
A successful SMTP acceptance test does not prove external delivery, inbox
placement, or reputation. The local Stalwart test proves only that Stalwart
accepted and stored the message and that its DKIM signature validates against
the configured key.

After local Identity is configured, run `scripts/smoke-mail.sh <recipient>`
with a new, unregistered mailbox. During deployment, set
`DEPLOY_SMOKE_MAIL_TO` to opt into this loopback-only check; it is skipped when
unset. The unverified smoke account remains in the database, so use a fresh
recipient for each run. The test reports SMTP acceptance, not inbox placement.

## Template images and test messages

The template editor stores uploaded PNG, JPEG, and GIF files in
workspace-associated Postgres records. The public asset URL is served by
Sendwise for use in email; uploads are limited to 8 MiB and 10000 by 10000
pixels. External image URLs are not emitted by the campaign renderer. Bundled
gallery artwork is neutral and created for this application.

Template test messages use the configured SMTP relay and app-managed DKIM key.
The response describes SMTP acceptance only. A local Mailpit run is not proof
of Stalwart delivery. The supplied Beefree-style export has not been seeded
with its artwork: redistribution rights for its design and image assets have
not been confirmed.
