# ADR-0005: Use Kafka for domain events

- Status: Accepted
- Date: 2026-10-03

## Decision

Publish versioned domain-event envelopes to Kafka. Use `order_id` as the message
key for order workflow events so events for one order remain in one partition.

## Consequences

- Ordering is guaranteed only within a partition.
- Consumers must tolerate at-least-once delivery.
- Consumers require idempotency, retry classification, and dead-letter handling.

