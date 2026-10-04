# M1: Order domain model

## Scope

This document records the M1 domain behavior that is already implemented in
`services/order/internal/domain`. It must not be interpreted as introducing new
M1 functionality or changing the current aggregate rules.

## Aggregate root

`Order` is the aggregate root. It owns the order identity, customer identity,
idempotency metadata, item snapshots, total, currency, status, cancellation
reason, version, and timestamps. State changes are performed through methods on
the aggregate rather than by mutating fields directly.

The aggregate copies its item slice when it is created, returned, cloned, or
converted to a persistence snapshot. Callers therefore cannot replace items in
the aggregate by retaining or modifying a slice reference.

## Order items and money

`OrderItem` is an immutable snapshot of a product at checkout time. Its private
fields capture:

- product ID;
- seller ID;
- product name;
- unit price;
- quantity; and
- calculated subtotal.

There are no item mutation methods. Later changes to the source product do not
change an existing order's snapshot.

`Money` is represented by an `int64` number of minor currency units. Floating
point values are not used. For example, `200_000` represents THB 2,000.00.
Negative amounts are rejected, and multiplication and addition detect `int64`
overflow. M1 supports THB only.

## Order statuses

The supported statuses are:

- `PENDING`
- `INVENTORY_RESERVED`
- `PAYMENT_PROCESSING`
- `CONFIRMED`
- `CANCELLED`

A newly created order starts in `PENDING`.

## State transitions

The normal forward path is:

```text
PENDING -> INVENTORY_RESERVED -> PAYMENT_PROCESSING -> CONFIRMED
```

Cancellation is allowed from `PENDING`, `INVENTORY_RESERVED`, and
`PAYMENT_PROCESSING` when a non-empty reason is supplied. A `CONFIRMED` order
cannot be cancelled.

The following transition methods reject calls from any other state, except for
their idempotent same-state case:

- `MarkInventoryReserved`: requires `PENDING`.
- `StartPayment`: requires `INVENTORY_RESERVED`.
- `Confirm`: requires `PAYMENT_PROCESSING`.

For example, confirming a `PENDING` order is invalid. A `CANCELLED` order cannot
return to the forward path. Invalid transitions return
`ErrInvalidStateTransition` and do not change aggregate state.

Calling a transition after the aggregate is already in that method's target
state is idempotent: it succeeds without changing `updated_at` or incrementing
the version. Repeating `Cancel` on a `CANCELLED` order is likewise idempotent
when a non-empty reason is supplied; cancellation always validates the supplied
reason first.

## Aggregate version

A new order starts at version `1`. Every successful state change increments the
version by exactly one and updates `updated_at`. Idempotent same-state calls and
rejected transitions do not increment the version. Persistence adapters use
this version for optimistic concurrency control in later milestones.

## Domain invariants

Order creation enforces:

- non-empty order ID, customer ID, idempotency key, and request hash;
- between 1 and 100 items;
- THB as the supported currency;
- no duplicate product IDs;
- valid item snapshots; and
- a total equal to the overflow-checked sum of item subtotals.

Order-item creation enforces:

- non-empty product ID, seller ID, and product name;
- quantity greater than zero;
- a non-negative unit price; and
- a subtotal equal to the overflow-checked unit price multiplied by quantity.

Aggregate rehydration revalidates the creation invariants and also requires:

- a version of at least `1`;
- non-zero timestamps with `updated_at` not before `created_at`;
- a recognized order status;
- a non-empty cancellation reason only for `CANCELLED`; and
- stored item subtotals and the stored order total to match recomputed values.

## Unit tests

The domain behavior is covered in
`services/order/internal/domain/order_test.go`:

- `TestNewOrderCalculatesTotalAndCreatesSnapshot` verifies total calculation,
  initial status and version, and protection of the aggregate's item slice.
- `TestOrderHappyPath` verifies the forward status sequence and one-version
  increment per successful transition.
- `TestConfirmIsIdempotent` verifies that a repeated same-state transition does
  not change the version or timestamp.
- `TestCannotConfirmPendingOrder` verifies rejection of an invalid transition.
- `TestCannotCancelConfirmedOrder` verifies that confirmed orders cannot be
  cancelled.
- `TestNewOrderRejectsDuplicateProduct` verifies product uniqueness within an
  order.
- `TestNewOrderItemRejectsInvalidQuantity` verifies positive item quantities.

These tests describe representative behavior. The constructors and
rehydration path enforce the complete invariant set listed above.
