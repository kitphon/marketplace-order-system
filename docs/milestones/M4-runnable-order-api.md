# M4: Runnable Order HTTP API

## Goal

Wire the existing Clean Architecture layers into a runnable process while
keeping infrastructure choices in the composition root.

## Endpoints

- `POST /v1/orders`
- `GET /health/live`
- `GET /health/ready`

## Current runtime boundary

- Orders persist in MongoDB.
- Product catalog and inventory are in memory and reset on process restart.
- Inventory reservations are idempotent by `order_id` but do not expire yet.
- An order can remain `PENDING` after an ambiguous inventory result; retry and
  reconciliation are deliberately deferred to the reliability milestone.
- Product and Inventory will become independent gRPC services in later
  milestones without changing the CreateOrder use case.

## Composition root

`cmd/api/main.go` owns concrete dependency creation and injects implementations
into the application use case. HTTP handlers do not import MongoDB, and the
application package does not import HTTP or MongoDB.
