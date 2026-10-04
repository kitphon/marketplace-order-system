# ADR-0004: Use gRPC for synchronous service communication

- Status: Accepted
- Date: 2026-10-03

## Decision

Use versioned Protobuf contracts and gRPC for synchronous calls such as product
snapshot lookup and the initial inventory-reservation implementation.

## Consequences

- Contracts are explicit and code-generated.
- Every call requires a deadline, status mapping, and trace propagation.
- Retries are limited to transient failures and idempotent operations.

