# M5B: Runnable Product and Inventory gRPC services

## Goal

M5B implements independently runnable Product and Inventory processes behind
the versioned M5A contracts. It does not connect the Order Service to either
process. The M4 Order runtime and `CreateOrder.Execute` continue to use the
existing in-process adapters until M5C.

## Service architecture

Both services preserve the same dependency direction:

```text
gRPC adapter
    -> application use case
    -> repository port
    -> in-memory repository adapter
```

Domain and application packages contain no gRPC or generated-Protobuf imports.
The gRPC adapters validate transport values, translate messages to application
commands, invoke the use cases, and translate results or errors back to the M5A
wire contract.

The Product process serves `marketplace.product.v1.ProductService` on
`PRODUCT_GRPC_ADDR` (default `:50051`). Its in-memory catalog is seeded with the
same example keyboard and mouse snapshots used by the M4 Order process.

The Inventory process serves `marketplace.inventory.v1.InventoryService` on
`INVENTORY_GRPC_ADDR` (default `:50052`). Its in-memory stock is seeded with 100
units of each M4 example product. `INVENTORY_EXPIRATION_INTERVAL` controls the
expiration pass interval and defaults to `5s`.

## Running locally

Start each process in a separate terminal from the repository root:

```bash
cd services/product
go run ./cmd/grpc
```

```bash
cd services/inventory
go run ./cmd/grpc
```

Both processes register the standard gRPC health service and shut down
gracefully on `SIGINT` or `SIGTERM`. They intentionally use plaintext transport
for local development. Production deployments require TLS or mTLS. Server
reflection is not enabled.

## Product behavior

`GetProducts` rejects an empty ID list, blank IDs, and duplicate IDs. Found
products are returned once in deterministic request order; missing IDs are
omitted so a caller can detect the missing snapshots. Prices remain `int64`
minor-unit values throughout the domain, application, and protobuf mapping.

## Inventory idempotency and expiration

`order_id` is the reservation idempotency identity. Reservation items are
validated for nonblank product IDs, positive quantities, and uniqueness, then
sorted by product ID before comparison. Request item order therefore has no
effect on idempotency.

- A new order with sufficient stock decrements all requested stock and stores a
  `RESERVED` reservation.
- An identical retry returns the original reservation ID and expiry without a
  second stock decrement. A retry-supplied expiry is not part of the payload and
  does not extend the original reservation.
- Reusing an order ID with different items returns a reservation conflict and
  leaves stock unchanged.
- Insufficient stock fails before any item is decremented, so a multi-item
  request is all-or-nothing.
- An expired reservation transitions once to `EXPIRED`, restores its stock
  once, and remains stored as an idempotency tombstone. Every later retry for
  that order ID returns reservation-expired.

The periodic expiration loop stops when the process context is cancelled. A
request can also discover and expire its own existing reservation before the
next periodic pass.

## Atomic correctness boundary

The in-memory Inventory repository uses one `sync.Mutex` critical section for
existing-reservation lookup, expiry detection, canonical payload comparison,
availability checks, all stock decrements, and reservation storage. Expiration
and stock restoration use that same critical section. This atomic state
transition—not a Distributed Lock—is the correctness mechanism, and concurrent
passes cannot restore stock more than once.

This guarantee is deliberately limited to one Inventory process. An in-memory
repository cannot coordinate multiple replicas or survive restarts. A
production multi-replica implementation needs shared durable storage and
transactional or conditional updates that preserve the same atomic invariant.
Adding a Distributed Lock would not replace those storage guarantees.

## gRPC errors

Invalid requests map to `InvalidArgument`. Inventory business failures use the
required gRPC status code and attach `InventoryErrorDetail` with a stable enum:

| Failure | gRPC code | Structured reason |
| --- | --- | --- |
| Insufficient stock | `FailedPrecondition` | `INVENTORY_FAILURE_REASON_INSUFFICIENT_STOCK` |
| Conflicting payload | `AlreadyExists` | `INVENTORY_FAILURE_REASON_RESERVATION_CONFLICT` |
| Expired reservation | `FailedPrecondition` | `INVENTORY_FAILURE_REASON_RESERVATION_EXPIRED` |

Unexpected errors map to `Internal` without being mislabeled as a business
failure. Human-readable status messages are not a machine contract.

## Deferred to M5C

M5C will add Order-side Product and Inventory gRPC client adapters, deadlines,
status mapping, and composition-root wiring. Retry policies, ambiguous-result
reconciliation, durable Product or Inventory persistence, Kafka, Redis, outbox,
and inbox behavior remain outside M5B.
