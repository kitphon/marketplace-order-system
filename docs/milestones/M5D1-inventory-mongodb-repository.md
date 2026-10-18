# M5D1: Durable MongoDB Inventory repository

## Goal

M5D1 added a durable MongoDB adapter for the existing Inventory repository port.
At this milestone boundary the Inventory runtime still used the in-memory
adapter; M5D2 subsequently wired the durable adapter into the composition root.
No protobuf contract or Order behavior changed in M5D1.

The in-memory repository is correct only inside one process. Restarting loses
stock and idempotency tombstones, while multiple replicas have independent
mutexes and can each decrement their own copy of stock. MongoDB provides shared
durable state and atomic transitions across service instances.

## Collections and indexes

Inventory owns two collections:

- `stocks`, keyed by product ID, stores available quantity, version, and audit
  timestamps;
- `reservations`, keyed by reservation ID, stores the canonical item snapshot,
  status, expiry, version, and audit timestamps.

Reservations have the stable indexes:

- `uq_reservations_order_id`, a unique index enforcing the `order_id`
  idempotency identity;
- `ix_reservations_status_expires_at`, supporting bounded oldest-expiry scans.

The built-in `_id` index supplies stock-ID and reservation-ID uniqueness.

## Reservation transaction

Each reservation attempt runs in a MongoDB multi-document transaction using
snapshot read concern, primary read preference, and majority write concern.
Snapshot reads give one consistent transaction view, primary reads are required
for transactions, and majority commits make acknowledged Inventory state
durable across the replica set.

Items are sorted by product ID before writes. Every stock decrement uses an
atomic filter requiring `available >= quantity`, then increments the stock
version. Missing and insufficient stock therefore share the typed insufficient
stock result and abort the complete transaction without partial decrements or a
reservation document.

The transaction first reads by `order_id` and compares the canonical payload
before considering reservation status or expiry. A different payload always
returns a reservation conflict, whether the stored reservation is active,
elapsed but still `RESERVED`, or already `EXPIRED`; that conflict never changes
reservation or stock state. An identical active retry returns the persisted
reservation ID and expiry without touching stock. The unique database index is
the final race arbiter: if concurrent transactions both initially see no
reservation, only one can commit, and the loser loads and classifies the
committed winner in a fresh transaction.

## Expiration and restoration

Expiration queries at most 100 `RESERVED` candidates at a time, ordered by
oldest `expires_at`. Each candidate is processed in its own transaction. The
transaction re-reads the document and requires the expected version, reserved
status, and elapsed expiry before changing it to `EXPIRED`, incrementing its
version, and restoring every stock item with stock-version increments.

Concurrent workers can discover the same candidate, but only one matching
version/status transition commits and restores stock. Later workers observe the
new version or `EXPIRED` status and perform an idempotent no-op. Expired records
remain permanent `order_id` tombstones.

For an identical payload, Reserve also performs lazy expiration when it finds
an elapsed reservation. The callback records an expired business outcome as its
result and returns no error, allowing expiration and restoration to commit.
Only after `WithTransaction` succeeds does the adapter return
`ErrReservationExpired`.

## Transaction retry behavior

The MongoDB Go Driver v2 transaction callback may rerun for
`TransientTransactionError` and may retry commit for
`UnknownTransactionCommitResult`. Callback bodies contain MongoDB operations
only and have no external side effects. Repository transactions derive a
10-second bound from the caller context, so caller cancellation or an earlier
deadline stops driver retries and crosses the repository boundary unchanged.
There is no unbounded custom retry loop.

## Seed-if-absent

`SeedStocks` validates and sorts all input before writing. It uses `_id`-based
upserts with `$setOnInsert`, so it creates missing products without resetting
existing availability, versions, or timestamps. Duplicate-key races from
concurrent service instances are classified by confirming the committed stock
winner.

## Mapping and validation

Persisted reservations are reconstructed with `domain.NewReservation`; expired
documents then use the domain `Expire` transition. The mapper rejects blank
identities, empty or duplicate items, invalid quantities or expiry, unknown
statuses, invalid versions, and inconsistent audit timestamps. Returned domain
values retain their defensive item-slice copying.

## Verification and deferred work

Build-tagged integration tests use a real MongoDB replica set and a unique
database per run. They cover seeding, transaction atomicity, cross-instance
idempotency and races, expiration, exactly-once stock restoration, tombstones,
indexes, context cancellation, and corrupt documents.

M5D2 wires this adapter, index creation, and seeding into the runnable Inventory
process. M6 still owns Order reconciliation for ambiguous Inventory outcomes.
Product persistence, Kafka, outbox/inbox, and other reliability mechanisms
remain deferred.
