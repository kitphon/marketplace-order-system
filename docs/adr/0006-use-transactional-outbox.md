# ADR-0006: Use a transactional outbox

- Status: Accepted
- Date: 2026-10-03

## Decision

Persist an aggregate change and its outbox event in one MongoDB transaction.
An outbox publisher sends pending events to Kafka and marks them as published.

## Consequences

- A committed domain change cannot permanently lose its event.
- Publishing is still at-least-once: a publisher can crash after Kafka accepts
  an event but before the outbox row is marked as published.
- Consumers still require Inbox-based deduplication and business idempotency.

