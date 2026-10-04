# ADR-0003: Use MongoDB for service persistence

- Status: Accepted
- Date: 2026-10-03

## Context

Orders are commonly loaded with all of their item snapshots and updated as an
aggregate. The target backend role also requires MongoDB design and tuning.

## Decision

Use MongoDB with embedded order items. Run a single-node replica set in local
development so MongoDB transactions can later atomically update an aggregate
and insert an outbox event.

## Consequences

- An order and its items are read together.
- Aggregate changes can be atomic within a document.
- Indexes must be designed from query patterns.
- Document size and maximum item count must be bounded.

