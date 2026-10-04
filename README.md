# Marketplace Order System

A production-oriented Go backend learning project for marketplace order processing.

The project is intentionally developed in milestones. Each reliability mechanism is
introduced only after reproducing the failure that motivates it.

## Milestones

- [M1 — Order domain model](docs/milestones/M1-domain-model.md)
- [M2 — Create Order application flow](docs/milestones/M2-create-order.md)
- [M3 — MongoDB Order repository](docs/milestones/M3-mongodb-repository.md)
- [M4 — Runnable Order HTTP API](docs/milestones/M4-runnable-order-api.md)

## Current milestone: M4 — Runnable Order HTTP API

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

Not implemented yet:

- Product and inventory gRPC services
- Kafka, transactional outbox, and inbox
- Redis concurrency controls
- Observability and load testing

## Run tests

```bash
cd services/order
go mod tidy
go test ./...
go test -race ./...
```

Run the MongoDB integration tests:

```bash
docker compose -f deploy/docker-compose.yml up -d
cd services/order
MONGODB_URI='mongodb://localhost:27017/?replicaSet=rs0&directConnection=true' \
  go test -tags=integration ./internal/adapters/mongodb/...
```

Run the API:

```bash
cd services/order
MONGODB_URI='mongodb://localhost:27017/?replicaSet=rs0&directConnection=true' \
  go run ./cmd/api
```

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
