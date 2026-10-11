# M5C: Order-side gRPC integration

## Goal

M5C replaces the Order process's runtime-only in-memory Product and Inventory
clients with outbound gRPC adapters. The Order domain and application continue
to depend only on interfaces and transport-neutral types from `internal/ports`.
The memory adapters remain available for focused tests.

M5C completes the M5 service-boundary milestone. It does not add durable
Product or Inventory storage, reconciliation, Kafka, Redis, outbox, or inbox
behavior.

## Runtime architecture

```text
Order HTTP adapter
    -> CreateOrder
        -> ProductClient port -> Product gRPC adapter -> Product Service
        -> InventoryClient port -> Inventory gRPC adapter -> Inventory Service
        -> OrderRepository port -> MongoDB adapter
```

The Order composition root creates one long-lived `grpc.ClientConn` for Product
and one for Inventory. Generated clients and the outbound adapters share those
concurrency-safe connections across HTTP requests. Both connections close when
the Order process shuts down. They are not created per request.

Local development uses plaintext credentials. Production TLS or mTLS remains
deferred. Product and Inventory are deliberately not added to Order liveness or
MongoDB-backed readiness checks.

## Product mapping and currency

The Product adapter maps requested IDs to `GetProductsRequest` and returns only
the snapshots present in the response. It preserves product ID, seller ID,
name, active state, currency, and `int64` minor-unit price. It rejects malformed
responses such as negative prices, missing required snapshot fields, or
duplicate returned IDs.

The adapter does not decide whether every requested product exists. That rule
remains in `CreateOrder`, which returns its existing product-not-found error for
a partial Product response.

`ProductSnapshot` now carries currency. `CreateOrder` requires every selected
snapshot to use the same currency and passes that value to `domain.NewOrder`.
The domain remains the final authority for supported currencies; THB remains
the only currently supported value.

## Retry ownership

The two Order-side adapters are the only explicitly configured retry owners.
No gRPC service-config retry policy is installed.

The bounded policy retries only:

- `Unavailable`;
- `DeadlineExceeded` while the caller's parent context is still active.

It does not retry invalid requests, Inventory business failures, cancellations,
or other status codes. `GRPC_RETRY_MAX_ATTEMPTS` includes the first call. Each
attempt has its own timeout, and exponential backoff uses bounded ±20% jitter.
All attempts and waits remain inside the parent context; waiting uses a timer
select rather than an unconditional sleep.

If the parent is canceled or reaches its deadline, the adapter stops before
another attempt and returns `context.Canceled` or `context.DeadlineExceeded`
unchanged. Exhausted retryable Product and Inventory calls map to
`ErrProductUnavailable` and `ErrInventoryUnavailable` respectively.
An upstream `Canceled` response while the parent remains active is different:
it is not retried and maps to the appropriate dependency-unavailable error. It
does not masquerade as caller cancellation.

## Inventory error mapping

The Inventory adapter validates that a successful response is explicitly
`RESERVED` and contains a valid reservation identity and expiry. It maps rich
`InventoryErrorDetail` reasons without parsing human-readable messages:

| Structured reason | Order port error | Retried |
| --- | --- | --- |
| `INSUFFICIENT_STOCK` | `ErrInsufficientStock` | No |
| `RESERVATION_CONFLICT` | `ErrReservationConflict` | No |
| `RESERVATION_EXPIRED` | `ErrReservationExpired` | No |

Missing, unknown, or status-inconsistent business details are treated as a
malformed upstream response. Raw gRPC status errors do not cross the adapter
boundary.

## Local configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `PRODUCT_GRPC_ADDR` | `localhost:50051` | Product Service address |
| `INVENTORY_GRPC_ADDR` | `localhost:50052` | Inventory Service address |
| `GRPC_RETRY_MAX_ATTEMPTS` | `3` | Total attempts, including the first call |
| `GRPC_PER_ATTEMPT_TIMEOUT` | `2s` | Timeout for each RPC attempt |
| `GRPC_RETRY_INITIAL_BACKOFF` | `100ms` | Backoff before the first retry |
| `GRPC_RETRY_MAX_BACKOFF` | `1s` | Maximum jittered backoff |

Start MongoDB and then run each service in a separate terminal:

```bash
docker compose -f deploy/docker-compose.yml up -d
go -C services/product run ./cmd/grpc
go -C services/inventory run ./cmd/grpc
MONGODB_URI='mongodb://localhost:27017/?replicaSet=rs0&directConnection=true' \
  go -C services/order run ./cmd/api
```

## Automated verification

M5C's previously manual smoke verification is now reproducible with one-command
test levels:

```bash
make verify       # vet, unit tests, and race tests; Docker is not required
make integration  # comprehensive full-stack black-box scenarios
make smoke        # minimal full-stack health and happy-path check
make verify-all   # verify, then integration and smoke in one stack lifecycle
```

The integration and smoke commands require Go, Docker Compose v2, and a running
Docker daemon. The runner starts this topology using actual service binaries and
real TCP connections:

```text
black-box Go tests -> Product gRPC :55051
                   -> Inventory gRPC :55052
                   -> Order HTTP :58080 -> Product and Inventory gRPC
                                        -> isolated MongoDB replica set :57017
```

Integration tests cover named gRPC health, Product response mapping, Inventory
reservation idempotency and structured conflict details, the full Create Order
path, HTTP idempotency and conflict behavior, missing products, and insufficient
stock. Smoke waits for both named gRPC health services plus Order liveness and
readiness, creates one order, and verifies its idempotent retry.

Each invocation builds Product, Inventory, and Order, gives MongoDB a unique
Compose project, volume, and database, and cleans up only those resources on
success, failure, interruption, or timeout. Readiness uses bounded polling. A
failed assertion prints separate Product, Inventory, Order, and MongoDB logs
before cleanup. Neither suite needs `grpcurl`, `jq`, or `grpc-health-probe`.

The default test endpoints can be changed when ports are occupied:

| Variable | Default |
| --- | --- |
| `M5_TEST_PRODUCT_ADDR` | `127.0.0.1:55051` |
| `M5_TEST_INVENTORY_ADDR` | `127.0.0.1:55052` |
| `M5_TEST_ORDER_ADDR` | `127.0.0.1:58080` |
| `M5_TEST_MONGODB_PORT` | `57017` |
| `M5_TEST_TIMEOUT_SECONDS` | `180` |

`make verify-all` deliberately starts the full stack once and selects both Go
test groups in that lifecycle, avoiding duplicate integration setup.

## Remaining reliability gap

Bounded transport retries do not resolve every ambiguous Inventory outcome.
Create Order still persists `PENDING` before reservation, and an outcome can be
unknown if communication fails after Inventory commits. Inventory's `order_id`
idempotency prevents duplicate stock decrements during retries, but a later
reliability milestone must add stale-`PENDING` reconciliation.
