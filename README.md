# Marketplace Order System

A production-oriented Go backend learning project for marketplace order processing.

The project is intentionally developed in milestones. Each reliability mechanism is
introduced only after reproducing the failure that motivates it.

## Milestones

- [M1 — Order domain model](docs/milestones/M1-domain-model.md)
- [M2 — Create Order application flow](docs/milestones/M2-create-order.md)
- [M3 — MongoDB Order repository](docs/milestones/M3-mongodb-repository.md)
- [M4 — Runnable Order HTTP API](docs/milestones/M4-runnable-order-api.md)
- [M5A — Versioned Product and Inventory gRPC contracts](docs/milestones/M5A-grpc-contracts.md)
- [M5B — Runnable Product and Inventory gRPC services](docs/milestones/M5B-grpc-services.md)
- [M5C — Order-side gRPC integration](docs/milestones/M5C-order-grpc-integration.md)
- [M5D1 — Inventory MongoDB repository](docs/milestones/M5D1-inventory-mongodb-repository.md)
- [M5D2 — Inventory MongoDB runtime](docs/milestones/M5D2-inventory-mongodb-runtime.md)

## Current runnable milestone: M5 — gRPC service boundaries

The Order Service now calls the standalone Product and Inventory services
through versioned gRPC adapters. MongoDB persists both Order aggregates and
Inventory stock and reservations in service-owned databases.

Implemented:

- Order aggregate and immutable order-item snapshots
- Money represented in minor units (`int64`)
- Business invariant validation
- Explicit order state transitions
- Idempotent same-state transitions
- Aggregate version increments for future optimistic concurrency control
- Unit tests for happy paths and invalid transitions
- Application ports for persistence, product lookup, inventory, clock, and IDs
- Create Order use case with canonical request hashing
- In-memory repository that enforces idempotency uniqueness and versions
- Duplicate and concurrent-request tests
- MongoDB persistence mapper and aggregate rehydration
- Compound unique idempotency index
- Atomic optimistic-concurrency updates using `_id + version`
- Docker Compose single-node replica set for local development
- MongoDB integration-test scaffold
- Composition root with explicit dependency injection
- `POST /v1/orders` HTTP endpoint
- Liveness and MongoDB-backed readiness endpoints
- In-memory Product and Inventory adapters for the first runnable slice
- Graceful HTTP shutdown
- Versioned Product and Inventory gRPC contracts and generated Go packages
- Standalone Product and Inventory gRPC processes with in-memory repositories
- Inventory reservation idempotency, atomic stock changes, and expiration
- Standard gRPC health services and graceful gRPC shutdown
- Order-side Product and Inventory gRPC adapters with structured error mapping
- Bounded, context-aware retries owned by the outbound adapters
- Product currency propagation and consistent-currency validation
- Long-lived Product and Inventory gRPC connections in the Order runtime
- Reproducible real-process M5 integration and smoke tests
- Durable transactional MongoDB Inventory repository and runtime wiring
- Restart-safe Inventory idempotency, stock, expiration, and seed-if-absent state

Not implemented yet:

- Kafka, transactional outbox, and inbox
- Redis concurrency controls
- Observability and load testing

## Run tests

The fast repository checks do not require Docker:

```bash
make verify
```

Full-stack checks require Go, Docker Compose v2, and a running Docker daemon:

```bash
make integration  # comprehensive black-box scenarios
make smoke        # minimal full-stack happy path
make verify-all   # fast checks, then both full-stack groups in one stack lifecycle
```

The full-stack runner builds and starts the real Product, Inventory, and Order
binaries over TCP and provisions an isolated MongoDB 8.0 replica set. Every run
uses a unique Compose project and volume plus separate unique Order and
Inventory databases, then removes only those resources. On failure it prints
separate service logs before cleanup.

Test addresses default to Product `127.0.0.1:55051`, Inventory
`127.0.0.1:55052`, Order `127.0.0.1:58080`, and MongoDB port `57017`. Override
them with `M5_TEST_PRODUCT_ADDR`, `M5_TEST_INVENTORY_ADDR`,
`M5_TEST_ORDER_ADDR`, and `M5_TEST_MONGODB_PORT`; set the overall bound with
`M5_TEST_TIMEOUT_SECONDS`. Database names can be overridden with
`M5_TEST_ORDER_MONGODB_DATABASE` and
`M5_TEST_INVENTORY_MONGODB_DATABASE`. Occupied ports fail early with a clear
error.

Run the MongoDB integration tests:

```bash
docker compose -f deploy/docker-compose.yml up -d
cd services/order
MONGODB_URI='mongodb://localhost:27017/?replicaSet=rs0&directConnection=true' \
  go test -tags=integration ./internal/adapters/mongodb/...
```

Run the Product and Inventory services in separate terminals:

```bash
go -C services/product run ./cmd/grpc
```

```bash
MONGODB_URI='mongodb://localhost:27017/?replicaSet=rs0&directConnection=true' \
  go -C services/inventory run ./cmd/grpc
```

Then run the Order API:

```bash
MONGODB_URI='mongodb://localhost:27017/?replicaSet=rs0&directConnection=true' \
  go -C services/order run ./cmd/api
```

Product listens on `:50051`, Inventory on `:50052`, and Order on `:8080` by
default. Local gRPC transport is plaintext; production TLS or mTLS is deferred.
See the [M5C milestone](docs/milestones/M5C-order-grpc-integration.md) for retry
configuration and the [M5D2 milestone](docs/milestones/M5D2-inventory-mongodb-runtime.md)
for Inventory persistence configuration and lifecycle behavior.

Create an order:

```bash
curl -i http://localhost:8080/v1/orders \
  -H 'Content-Type: application/json' \
  -H 'X-Customer-ID: customer-001' \
  -H 'Idempotency-Key: checkout-001' \
  -d '{"items":[{"product_id":"product-001","quantity":2}]}'
```

## Planned services

- Order Service
- Product Service
- Inventory Service
- Payment Service
- Notification Service

Architecture decisions are recorded under [`docs/adr`](docs/adr).
