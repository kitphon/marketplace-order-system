# ADR-0001: Use microservices for the learning system

- Status: Accepted
- Date: 2026-10-03

## Context

The project must exercise independent data ownership, synchronous gRPC,
event-driven processing, partial failure, scaling, and observability.

## Decision

Separate Order, Product, Inventory, Payment, and Notification into services.
Keep them in one repository but give each service an independent Go module and
logical database.

## Consequences

- Services can evolve and scale independently.
- Cross-service ACID transactions are unavailable.
- The system must handle retries, idempotency, eventual consistency, and
  distributed tracing.
- Operational complexity is intentionally higher than a modular monolith
  because those trade-offs are learning goals of this project.

