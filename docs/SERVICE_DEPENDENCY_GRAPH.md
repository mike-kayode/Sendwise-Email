# Service Dependency Graph

This graph is the intended dependency order, not a fully deployed topology. Gateway, Identity, and Workspace have separate implementations and a Compose configuration; remaining product APIs still route to the legacy monolith, and the stack is not runtime-validated.

```mermaid
flowchart LR
    Web[Next.js web] --> Gateway[API Gateway]
    External[External clients] --> Gateway
    Gateway --> Identity[Identity]
    Gateway --> Workspace[Workspace]
    Gateway --> Audience[Audience]
    Gateway --> Consent[Consent]
    Gateway --> Campaign[Campaign]
    Gateway --> Design[Design and Templates]
    Gateway --> Domain[Domain and Deliverability]
    Gateway --> DeliveryAPI[Delivery APIs]
    Gateway --> Analytics[Tracking and Analytics API]
    Gateway --> Automation[Automation]
    Gateway --> Forms[Forms]
    Gateway --> Billing[Billing]
    Gateway --> Integrations[Integrations and Webhooks]
    Gateway --> Admin[Platform Admin]
    Identity -->|UserRegistered/Verified| Workspace
    Workspace -->|membership policy events| Gateway
    Audience -->|contact/list/segment data contract| Campaign
    Consent -->|eligibility decision/events| Delivery
    Campaign -->|scheduled campaign contract| Orchestrator[Delivery Orchestration]
    Design -->|immutable design version| Orchestrator
    Domain -->|verified sender/limits| Orchestrator
    Orchestrator -->|DeliveryRequested| Worker[Delivery Workers]
    Worker --> Transport[SMTP Transport]
    Transport --> Stalwart[Stalwart SMTP]
    Stalwart --> MX[Recipient MX]
    Bounce[Bounce and Complaint Ingress] -->|trusted events| Consent
    Bounce --> Delivery
    Worker -->|submission outcomes| Delivery
    Tracking[Tracking ingress] --> Analytics
    Delivery -->|delivery events| Analytics
    Consent --> Automation
    Audience --> Automation
    Campaign --> Automation
    Automation -->|commands via owner APIs| Audience
    Automation --> Campaign
    Billing --> Workspace
    Notification[Notifications] --> SMTP
    All[Services] -->|versioned events| NATS[(NATS JetStream)]
    All -->|owned data only| DBs[(Service-owned PostgreSQL schemas/databases)]
```

## Dependency constraints

- No service may synchronously call a consumer that depends on its event, forming a cycle.
- Delivery workers receive immutable minimum-required payloads or call explicit APIs; they do not access Audience, Campaign, Consent, or Design tables directly.
- Campaign scheduling does not block on SMTP. It emits a durable command and reports lifecycle state asynchronously.
- Analytics is a consumer and query service, not a source of truth for transactional business decisions.
- Billing entitlements are locally verifiable with signed/versioned entitlement snapshots or bounded APIs; payment-provider availability must not corrupt core contact data.
- Notification providers are transport abstractions and separate from campaign marketing delivery policies.
