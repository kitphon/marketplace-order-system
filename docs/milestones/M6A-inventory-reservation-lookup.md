# M6A: Inventory reservation lookup

## Goal

M6A adds an authoritative, durable lookup that lets a future Order reconciliation
worker determine whether a `PENDING` order has an active reservation, an expired
tombstone, or no Inventory resource. The lookup returns the persisted reservation
intent so Order can compare it with its own stored intent after an ambiguous
Reserve Inventory outcome.

## Contract and outcomes

`GetReservationByOrderID` extends the versioned Inventory v1 service. A successful
response contains the original reservation ID and order ID, canonical product and
quantity items, persisted `RESERVED` or `EXPIRED` status, and original `expires_at`.
Items are sorted by product ID at the gRPC boundary for deterministic comparison.

A missing reservation returns gRPC `NotFound`. It is not a reservation status
because no reservation resource exists. Blank order IDs return `InvalidArgument`,
while storage failures and corrupt or unknown persisted state return `Internal`.
`UNSPECIFIED` is retained as protobuf zero for compatibility but is never emitted
in a successful response.

## Persisted status and expiry

Lookup reports persisted Inventory state. It does not renew a reservation, invent
one when none exists, or convert an elapsed `RESERVED` reservation to `EXPIRED`.
The existing expiration worker remains responsible for the durable transition and
exactly-once stock restoration.

The original `expires_at` is returned even for `RESERVED`. A future Order
reconciliation layer must use it to reject or classify a response whose effective
expiry has elapsed but whose expiration transition has not yet been persisted.

## Retry safety and persistence

The memory adapter looks up under its existing mutex and returns a defensive
clone. The MongoDB adapter queries the existing unique `order_id` index, maps a
missing document to the Inventory-owned `ErrReservationNotFound`, reconstructs
the domain value through the existing corruption-checking mapper, and returns a
clone. Neither path changes stock, reservation version, status, expiry, or audit
timestamps. Repeated lookups and process restarts therefore preserve the complete
response.

## Deferred work

M6A does not add an Order-side gRPC lookup adapter, reconciliation persistence,
new Order transitions, claim or lease processing, retry backoff,
`NEEDS_ATTENTION`, or a worker. Those Order-side mechanics remain for M6B and
M6C. Kafka and outbox/inbox delivery also remain deferred.
