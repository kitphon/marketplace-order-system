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

## Current runnable milestone: M5 — gRPC service boundaries

The Order Service now calls the standalone Product and Inventory services
through versioned gRPC adapters. MongoDB remains the Order persistence layer.

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

Not implemented yet:

- Kafka, transactional outbox, and inbox
- Redis concurrency controls
- Observability and load testing

## Run tests

```bash
make verify
```

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
and environment configuration.

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
