# Project state

Last verified: 2026-10-04

## Goal

Build a production-oriented marketplace order-processing backend in Go, one milestone at a time, so each distributed-systems reliability mechanism is introduced only after the failure that motivates it is understood.

## Completed milestones

- **M1 — Domain foundation:** Order aggregate, immutable item snapshots, minor-unit money, invariants, state transitions, and unit tests.
- **M2 — Create Order:** Ports and application flow for idempotent order creation, product snapshots, inventory reservation, and concurrency handling.
- **M3 — MongoDB repository:** Durable aggregate mapping, validated rehydration, unique idempotency index, and version-based optimistic concurrency.
- **M4 — Runnable Order API:** Composition root, MongoDB persistence, in-memory Product and Inventory adapters, `POST /v1/orders`, health endpoints, and graceful shutdown.

## Current architecture

- Go 1.25 (`go 1.25.0` toolchain directives) with a workspace containing the Order Service module.
- Clean Architecture / Ports and Adapters: domain and application policy are isolated from delivery and persistence implementations.
- HTTP endpoints: `POST /v1/orders`, `GET /health/live`, and MongoDB-backed `GET /health/ready`.
- MongoDB Go Driver v2 persists Order aggregates as embedded documents.
- Product lookup and Inventory reservation are in-process memory adapters for M4.
- `cmd/api/main.go` is the composition root and owns concrete dependency wiring.

## Decisions and invariants

- Currency amounts use `int64` minor units; the current supported currency is THB.
- Order items preserve the product name, seller, and unit-price snapshot used at checkout.
- `(customer_id, idempotency_key)` is unique. Reusing the identity with a different canonical request hash is rejected.
- MongoDB updates compare `_id` and the expected `version`; successful domain transitions increment the version by exactly one.
- Persisted documents are reconstructed through domain validation rather than decoded into private aggregate fields.
- Order IDs are cryptographically random RFC 4122 version 4 UUIDs.
- Product and Inventory dependencies are accessed through interfaces in `internal/ports`.

## Known reliability gap

Create Order persists `PENDING` before reserving Inventory. If Inventory reserves stock but its result is ambiguous, or Order Service cannot persist `INVENTORY_RESERVED`, the order can remain `PENDING`. M4 intentionally has no automatic retry or reconciliation; those mechanisms belong to a later reliability milestone.

## Explicitly deferred

- Independent Product and Inventory gRPC services and versioned Protobuf contracts
- Kafka domain events
- Transactional outbox and consumer inbox patterns
- Redis-based concurrency controls
- Automated retry and stale-`PENDING` reconciliation
- Production observability and load testing

## Next milestone

**M5 — Product and Inventory gRPC boundaries.** Replace the M4 in-memory runtime adapters with service boundaries without moving transport concerns into domain or application packages.

## Last verification

Executed from `services/order` on 2026-10-04 with Go 1.27.1 against `go 1.25.0` directives:

- `go mod tidy` — passed; dependency metadata was resolved and normalized.
- `gofmt -w` on every Go source file — passed; subsequent `git diff --check` passed.
- `go vet ./...` — passed.
- `go test ./...` — passed.
- `go test -race ./...` — passed.
- `MONGODB_URI='mongodb://localhost:27017/?replicaSet=rs0&directConnection=true' go test -tags=integration ./internal/adapters/mongodb/...` — passed against the Docker Compose MongoDB 8.0 single-node replica set.
