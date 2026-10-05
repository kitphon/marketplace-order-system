# M5A: Versioned Product and Inventory gRPC contracts

## Goal

Define the first versioned synchronous service boundaries for Product and
Inventory without changing the runnable M4 Order Service. M5A contains
Protobuf contracts, generated Go code, and reproducible generation tooling
only. It does not implement servers, Order-side gRPC adapters, persistence, or
reliability behavior.

## Contract layout

The shared `contracts` Go module publishes two canonical packages:

- `github.com/kitphon/marketplace-order-system/contracts/product/v1`
- `github.com/kitphon/marketplace-order-system/contracts/inventory/v1`

Protobuf sources live under `contracts/proto/marketplace`, separate from the
generated Go packages. The schemas use the versioned namespaces
`marketplace.product.v1` and `marketplace.inventory.v1`.

Versioning makes wire compatibility explicit and allows a future incompatible
API to be introduced under a new namespace without silently changing existing
clients. Within a version, field numbers are stable and must never be reused.

## Product contract

`ProductService.GetProducts` accepts repeated product IDs and returns product
snapshots containing product ID, seller ID, name, active state, currency, and
unit price. Money is an `int64` count of currency minor units; floating point is
not used.

## Inventory contract

`InventoryService.ReserveInventory` accepts an order ID, reservation items, and
an expiry timestamp. A successful response returns the reservation ID, its
status, and its expiry timestamp.

`order_id` is the Inventory reservation's idempotency identity. Retrying the
same logical reservation can therefore return the existing result instead of
reserving stock twice. A later server implementation must reject reuse of an
order ID with a conflicting reservation payload.

## Structured business failures

Inventory business failures use `InventoryFailureReason` and
`InventoryErrorDetail`, including an optional relevant product ID. The future
Inventory server will attach this message to rich gRPC status errors. Clients
can then branch on stable enum values rather than parsing human-readable error
strings, which remain unsuitable as a machine contract.

Every enum defines an `UNSPECIFIED = 0` value. Protobuf also permits a newer
server to send an enum number unknown to an older client. Consumers must handle
both `UNSPECIFIED` and unknown numeric values safely: they must not infer
success or apply a more specific business action than the value supports.

## Reproducible generation

The repository uses Buf with a pinned CLI version and pinned Go plugin versions.
From the repository root, run:

```bash
make proto
```

This formats and lints the schemas, then regenerates the committed `.pb.go` and
`_grpc.pb.go` files. Generated files must never be edited by hand.

## Deferred work

- **M5B:** implement the Product and Inventory services behind these contracts,
  including server-side validation and rich Inventory status details.
- **M5C:** implement Order Service gRPC adapters, deadlines and status mapping,
  then replace the M4 in-memory runtime wiring.

Retry policies, ambiguous-result reconciliation, Kafka, Redis, and new
persistence remain outside M5A.
