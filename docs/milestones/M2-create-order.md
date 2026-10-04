# M2: Create Order application flow

## Goal

Implement the first synchronous vertical slice without infrastructure-specific
code. The use case stores a PENDING order before calling Inventory so an
ambiguous remote result remains traceable.

## Flow

1. Canonicalize and hash the request.
2. Find an existing order by `(customer_id, idempotency_key)`.
3. Return the existing order when its request hash matches.
4. Load trusted price snapshots from Product.
5. Create and persist a PENDING order.
6. Reserve inventory with `order_id` as its idempotency identity.
7. Persist INVENTORY_RESERVED using the expected aggregate version.

## Race-condition rule

The initial read is only an optimization. Correctness comes from the
repository's unique idempotency constraint. If two requests observe no order,
one Create wins and the loser reloads the winner.

## Known failure intentionally retained

Inventory can reserve stock and Order Service can fail before persisting
INVENTORY_RESERVED. A later milestone will reproduce and address this through
idempotent reservation, retry, event-driven processing, and reconciliation.

