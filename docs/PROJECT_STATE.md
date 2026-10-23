# Project state

Last verified: 2026-10-23

## Goal

Build a production-oriented marketplace order-processing backend in Go, one milestone at a time, so each distributed-systems reliability mechanism is introduced only after the failure that motivates it is understood.

## Completed milestones

- **M1 — Domain foundation:** Order aggregate, immutable item snapshots, minor-unit money, invariants, state transitions, and unit tests.
- **M2 — Create Order:** Ports and application flow for idempotent order creation, product snapshots, inventory reservation, and concurrency handling.
- **M3 — MongoDB repository:** Durable aggregate mapping, validated rehydration, unique idempotency index, and version-based optimistic concurrency.
- **M4 — Runnable Order API:** Composition root, MongoDB persistence, in-memory Product and Inventory adapters, `POST /v1/orders`, health endpoints, and graceful shutdown.
- **M5A — gRPC contracts:** Versioned Product and Inventory Protobuf APIs, structured Inventory failure details, generated Go packages, and reproducible Buf generation.
- **M5B — gRPC services:** Independently runnable Product and Inventory gRPC processes, Clean Architecture service layers, in-memory repositories, atomic idempotent Inventory reservations, expiration, structured errors, health services, and graceful shutdown.
- **M5C — Order gRPC integration:** Order-side Product and Inventory adapters, structured error mapping, bounded context-aware retries, currency propagation, and long-lived runtime connections. M5 service boundaries are complete.
- **M5D1 — Inventory MongoDB repository:** Durable stock and reservation documents, transactional idempotent reservations, versioned expiration with exactly-once stock restoration, seed-if-absent behavior, and real replica-set integration tests. Runtime wiring was delivered in M5D2.
- **M5D2 — Inventory MongoDB runtime:** Runnable Inventory composition root backed only by the durable repository, fail-fast MongoDB initialization, index and insert-only stock setup, bounded lifecycle management, isolated service databases, and restart persistence verification. M5D is complete.
- **M6A — Inventory reservation lookup:** Versioned `GetReservationByOrderID` contract, durable repository lookup, focused application query, explicit gRPC status mapping, and restart-safe black-box coverage. Inventory now exposes authoritative `RESERVED`, `EXPIRED`, and not-found outcomes without mutating stock or reservations.

## Current architecture

- Go 1.25 (`go 1.25.0` toolchain directives) with a workspace containing the shared contracts and Order, Product, and Inventory service modules.
- Clean Architecture / Ports and Adapters: domain and application policy are isolated from delivery and persistence implementations.
- HTTP endpoints: `POST /v1/orders`, `GET /health/live`, and MongoDB-backed `GET /health/ready`.
- MongoDB Go Driver v2 persists Order aggregates as embedded documents.
- The Order runtime uses outbound Product and Inventory gRPC adapters; memory clients remain available only for focused tests.
- Standalone Product and Inventory processes implement the M5A gRPC contracts on `:50051` and `:50052` by default.
- Product follows gRPC adapter → application use case → repository port → in-memory repository adapter. Inventory follows the same boundaries with MongoDB as its runtime repository; its in-memory adapter remains for focused tests.
- The Order, Product, and Inventory command packages are independent composition roots that own their concrete dependency wiring.
- The Order composition root owns two long-lived gRPC connections and closes them during shutdown. Product and Inventory do not participate in Order health endpoints.
- The M5 black-box suite builds all three service binaries, connects over real TCP, and uses an isolated MongoDB 8.0 replica set per run.
- The Inventory composition root owns one long-lived MongoDB client and uses one repository instance for reservation and expiration. Order and Inventory use separate service-owned databases.
- Inventory exposes a read-only lookup by `order_id`; its response includes reservation identity, canonical items, persisted status, and original expiry for future Order intent comparison.

## Decisions and invariants

- Currency amounts use `int64` minor units; the current supported currency is THB.
- Order items preserve the product name, seller, and unit-price snapshot used at checkout.
- `(customer_id, idempotency_key)` is unique. Reusing the identity with a different canonical request hash is rejected.
- MongoDB updates compare `_id` and the expected `version`; successful domain transitions increment the version by exactly one.
- Persisted documents are reconstructed through domain validation rather than decoded into private aggregate fields.
- Order IDs are cryptographically random RFC 4122 version 4 UUIDs.
- Product and Inventory dependencies are accessed through interfaces in `internal/ports`.
- Inventory reservation uses `order_id` as its idempotency identity.
- Inventory business failures are represented by versioned enums and structured details rather than parsed error strings.
- Inventory compares a canonical, order-independent item payload. An identical retry preserves the original reservation ID and expiry and never decrements stock twice.
- The retained in-memory Inventory repository serializes reservation creation, expiration, and stock restoration with one process-local mutex and is used only by focused tests.
- The runnable Inventory service uses shared durable MongoDB transactions and conditional versioned updates. A Distributed Lock is not the correctness mechanism.
- Product currency crosses the gRPC and port boundaries. Create Order requires one consistent currency, while the domain remains authoritative for the currently supported THB currency.
- Order-side adapters exclusively own bounded retries for `Unavailable` and per-attempt `DeadlineExceeded`; no gRPC service-config retry policy is configured.
- Parent cancellation and deadlines stop retries immediately and cross the adapter boundary unchanged.
- Full-stack tests use unique Compose project and volume names, separate unique Order and Inventory databases, and remove only their own resources after success, failure, interruption, or timeout.
- Durable Inventory reservations use snapshot/primary/majority multi-document transactions. Conditional stock decrements prevent negative availability, while versioned reservation transitions ensure stock is restored once across concurrent expiration workers.
- Inventory stock seeding is insert-only: concurrent `$setOnInsert` upserts never reset existing availability or versions.
- A missing Inventory reservation is gRPC `NotFound`, not a reservation status. Lookup reports persisted status and never renews an elapsed `RESERVED` reservation; the expiration worker owns the durable transition.

## Known reliability gap

Create Order persists `PENDING` before reserving Inventory. If Inventory reserves stock but its result is ambiguous, or Order Service cannot persist `INVENTORY_RESERVED`, the order can remain `PENDING`. M6A provides the authoritative Inventory lookup needed to resolve that ambiguity, but the Order-side adapter, reconciliation fields, and worker remain deferred.

## Explicitly deferred

- Durable Product storage
- Kafka domain events
- Transactional outbox and consumer inbox patterns
- Redis-based concurrency controls
- Order-side Inventory lookup adapter and stale-`PENDING` reconciliation
- Production gRPC TLS or mTLS
- Production observability and load testing

## Next milestone

M6A is complete. M6B and M6C should add Order-side lookup/reconciliation state and the reconciliation worker before asynchronous messaging.

## Last verification

Executed from `services/order` on 2026-10-04 with Go 1.27.1 against `go 1.25.0` directives:

- `go mod tidy` — passed; dependency metadata was resolved and normalized.
- `gofmt -w` on every Go source file — passed; subsequent `git diff --check` passed.
- `go vet ./...` — passed.
- `go test ./...` — passed.
- `go test -race ./...` — passed.
- `MONGODB_URI='mongodb://localhost:27017/?replicaSet=rs0&directConnection=true' go test -tags=integration ./internal/adapters/mongodb/...` — passed against the Docker Compose MongoDB 8.0 single-node replica set.

M5A contract verification on 2026-10-04:

- `make proto` — Buf formatting and linting passed; Go protobuf and gRPC code generation passed.
- `make proto-generate` — a second generation passed and produced byte-identical generated files.
- `go work sync` — passed for the Order Service and contracts workspace.
- `go vet ./...`, `go test ./...`, and `go test -race ./...` — passed from both `contracts` and `services/order`.
- `git diff --check` — passed.

M5B service verification on 2026-10-06 with Go 1.27.1 against `go 1.25.0` directives:

- `go mod tidy` — passed from `contracts`, `services/order`, `services/product`, and `services/inventory`.
- `gofmt -w` — completed for all Product and Inventory Go files.
- `go work sync` — passed with all four modules in the workspace.
- `make proto-lint` and `make proto-generate` — passed; generation left all committed protobuf outputs unchanged.
- `go vet ./...`, `go test ./...`, and `go test -race ./...` — passed from all four workspace modules.
- Product and Inventory processes started locally; `grpc-health-probe v0.4.38` reported `SERVING` for both named services.
- `git diff --check` — passed.

M5C integration verification on 2026-10-11 with Go 1.27.1 against `go 1.25.0` directives:

- `gofmt -w` — completed for all Order Go files.
- `go -C services/order mod tidy` and `go work sync` — passed.
- `make proto-lint` and `make proto-generate` — passed; protobuf sources and generated outputs remained unchanged.
- `make verify` — passed repository-wide vet, unit, and race tests in all four Go modules.
- `make integration` — passed the reproducible black-box Product, Inventory, Order, HTTP, gRPC, idempotency, structured-error, and MongoDB scenarios using real binaries and TCP connections.
- `make smoke` — passed the minimal full-stack health, Create Order, and idempotent-retry checks.
- `make verify-all` — passed; it ran `make verify`, then ran the integration and smoke test groups against one newly isolated stack rather than starting the full stack twice.
- `git diff --check` — passed.

M5D1 repository verification on 2026-10-18 with Go 1.27.1 against `go 1.25.0` directives:

- `gofmt -w` — completed for all new Inventory MongoDB adapter and test files.
- `go -C services/inventory mod tidy` and `go work sync` — passed.
- `go -C services/inventory vet ./...` — passed.
- `go -C services/inventory test ./...` — passed.
- `go -C services/inventory test -race ./...` — passed.
- Build-tagged Inventory MongoDB integration tests — passed under the race detector against an isolated MongoDB 8.0 replica set.
- `make verify` — passed repository-wide vet, unit, and race tests in all four Go modules.
- `git diff --check` — passed.

M5D2 runtime verification on 2026-10-18 with Go 1.27.1 against `go 1.25.0` directives:

- `gofmt -w` on the changed Go files, `go work sync`, and `bash -n scripts/test-stack.sh` — passed.
- `make vet`, `make test`, and `make test-race` — passed across all four Go modules.
- Build-tagged Inventory MongoDB integration tests — passed against an isolated MongoDB 8.0 replica set.
- `make integration` — passed all existing black-box scenarios plus persisted Inventory document, index, idempotency, conflict, fail-fast startup, and restart-survival checks.
- `make smoke` — passed the real-process happy path and idempotent retry against durable Inventory storage.
- `make verify-all` — passed; it reran the fast verification and then integration, Inventory restart, and smoke groups in one isolated stack lifecycle.
- `git diff --check` — passed.

M6A verification on 2026-10-23:

- `gofmt -w` on all changed Go files and `go work sync` — passed.
- `make proto-format`, `make proto-lint`, and `make proto-generate` — passed; generated Inventory v1 Go and gRPC contracts were committed.
- `make vet`, `make test`, and `make test-race` — passed across all four Go modules.
- Build-tagged Inventory MongoDB integration tests — passed against an isolated MongoDB 8.0 replica set.
- `make integration` — passed the real-process contract, Create Order, lookup retry-safety, missing lookup, Inventory restart, and persistence scenarios.
- `make smoke` — passed the existing real-process Create Order happy path and idempotent retry.
- `make verify-all` — passed fast verification followed by integration, Inventory restart lookup, and smoke groups in one isolated stack lifecycle.
- `git diff --check` — passed.
