# Scalability and Performance Plan

No throughput/capacity benchmark has been run. User/customer targets are planning inputs, not current supported limits. Current architectural observations are in [audit section 15](./SYSTEM_ARCHITECTURE_AND_DEVELOPMENT_AUDIT.md#15-scalability-and-performance-assessment).

## Workload matrix

| Workload target | Required test and likely constraints |
|---|---|
| 1,000 / 10,000 / 100,000 organizations | Tenant fairness, auth/session load, gateway and service horizontal scaling, DB pool multiplication, hot-tenant skew, cached authorization invalidation, per-tenant API limits |
| 1M / 10M contacts | Import throughput/memory, dedupe index contention, cursor pagination, segment predicates, list membership indexes, tenant distribution, export streaming and retention |
| 100k recipients per campaign | Snapshot creation duration, DB writes, event/outbox volume, broker lag, recipient payload size, fairness and SMTP sink throughput |
| 1M messages across concurrent campaigns | Multi-tenant scheduling, provider/domain/relay throttles, NATS partition/stream pressure, DB pool/IO, retry storms, failure isolation and queue recovery |
| Millions of tracking events | Ingest concurrency, dedupe cardinality, partition pruning, event retention, aggregation latency, proxy/bot filtering, privacy requirements |

## Test method

- Define dataset shape (tenants, skew, list sizes, suppression rate, message payload, recipient MX distribution), not only row count.
- Run on reproducible hardware/container limits with actual migration and DB roles.
- Include steady state, burst, hot-tenant, slow dependency, broker disconnect, DB restart, worker crash, duplicate event, and recovery scenarios.
- Record environment, dataset generator/seed, Go/Node versions, DB/NATS/SMTP config, request/event rates, p50/p95/p99, error rate, queue lag, pool waits, CPU/memory, storage, replay and consistency outcomes.
- Do not extrapolate linearly without repeated runs and bottleneck evidence.

## SLOs to define before service extraction

For each user-facing service define availability, latency/error budget and data freshness target. For asynchronous paths define outbox age, queue lag, completion time, retry/DLQ rates, event-loss objective and replay RTO. For email distinguish accepted submission, downstream bounce, and observable engagement; none guarantees inbox placement.

## Scaling controls

- Scale stateless API services horizontally behind gateway.
- Scale workers by queue lag while retaining per-tenant and destination-provider fairness.
- Bound DB pools by global Postgres connection capacity; use a pooler if measurements warrant it.
- Batch/stream imports, snapshots, event writes and exports.
- Use partitioned/archive event storage and pre-aggregated analytics after retention/query tests.
- Apply backpressure and bounded retries; shed non-critical analytics before auth/consent/delivery safety.
- Scale SMTP capacity only with IP/domain warm-up, relay acceptance tests, reputation controls and provider-specific throttles.

