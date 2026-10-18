# M5D2: Inventory MongoDB runtime

## Goal

M5D2 makes the durable MongoDB Inventory repository from M5D1 the only
repository used by the runnable Inventory process. The in-memory adapter remains
available for focused tests, but there is no runtime fallback to volatile state.
Product persistence and Order reconciliation remain deferred.

## Runtime configuration

The Inventory process reads:

| Environment variable | Default | Purpose |
| --- | --- | --- |
| `INVENTORY_GRPC_ADDR` | `:50052` | gRPC listen address |
| `INVENTORY_EXPIRATION_INTERVAL` | `5s` | reservation-expiration scan interval |
| `MONGODB_URI` | `mongodb://localhost:27017/?replicaSet=rs0&directConnection=true` | Inventory MongoDB connection |
| `MONGODB_DATABASE` | `inventory_db` | Inventory-owned database |

The address, interval, URI, and database name are validated before startup.
Local development uses plaintext gRPC; production still requires TLS or mTLS.

## Startup and readiness

Startup is fail-fast and ordered:

1. load and validate configuration;
2. create one long-lived MongoDB client and ping it with a bounded context;
3. create the Inventory repository;
4. create the required indexes;
5. seed the example stock with insert-only upserts;
6. open the gRPC listener and report the Inventory health service as `SERVING`.

If MongoDB cannot be reached or initialization fails, the process exits without
opening its gRPC port. Seed-if-absent behavior means restarts create missing
example products but never reset availability, versions, reservations, or
idempotency tombstones.

## Reservation and expiration wiring

The Reserve Inventory and Expire Reservations use cases share the same MongoDB
repository and client. Reservations, lazy expiration, periodic expiration, and
stock restoration therefore use the transactional and conditional-update
semantics documented in M5D1. A single Inventory process no longer relies on an
in-process mutex for runtime correctness.

The periodic expiration loop starts only after persistence initialization. On
SIGINT or SIGTERM, the process marks both health names `NOT_SERVING`, cancels and
waits for the expiration loop, gracefully stops gRPC, and disconnects MongoDB
with bounded waits.

## Test-stack isolation and restart proof

The full-stack runner creates a unique Docker Compose project and volume for
each run. Order and Inventory receive separate, uniquely named databases through
`M5_TEST_ORDER_MONGODB_DATABASE` and
`M5_TEST_INVENTORY_MONGODB_DATABASE`. Existing address overrides remain
available, and `M5_TEST_MONGODB_PORT` controls the isolated replica-set port.

Black-box integration coverage uses the actual Inventory binary and real TCP
connections. It verifies indexes, MongoDB stock and reservation documents,
idempotent retry, structured conflict behavior, and startup failure with an
unreachable MongoDB instance. The runner then restarts only Inventory against
the same database and proves the reservation identity and original expiry
survive, stock is not decremented twice, and startup seeding does not reset
stock.

Run the checks with:

```bash
make verify
make integration
make smoke
make verify-all
```

`make verify` remains Docker-free. The other targets provision and clean up
only their own isolated MongoDB resources; service and MongoDB logs are printed
automatically on failure.

## Deferred work

M5D is complete after this runtime wiring. M6 will address ambiguous Inventory
outcomes and stale-`PENDING` Order reconciliation. Durable Product storage,
Kafka, outbox/inbox processing, Redis, production transport security,
observability, and load testing remain deferred.
