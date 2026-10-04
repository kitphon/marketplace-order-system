package domain

import (
	"fmt"
	"time"
)

const (
	CurrencyTHB   = "THB"
	maxOrderItems = 100
)

type Order struct {
	id             string
	customerID     string
	idempotencyKey string
	requestHash    string
	items          []OrderItem
	totalAmount    Money
	currency       string
	status         OrderStatus
	cancelReason   string
	version        int64
	createdAt      time.Time
	updatedAt      time.Time
}

// OrderSnapshot is the complete persisted state needed to rehydrate an Order.
// State-changing behavior remains on Order; this type is only a boundary DTO.
type OrderSnapshot struct {
	ID             string
	CustomerID     string
	IdempotencyKey string
	RequestHash    string
	Items          []OrderItem
	TotalAmount    Money
	Currency       string
	Status         OrderStatus
	CancelReason   string
	Version        int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func NewOrder(
	id string,
	customerID string,
	idempotencyKey string,
	requestHash string,
	items []OrderItem,
	currency string,
	now time.Time,
) (*Order, error) {
	if id == "" {
		return nil, ErrMissingOrderID
	}
	if customerID == "" {
		return nil, ErrMissingCustomerID
	}
	if idempotencyKey == "" {
		return nil, ErrMissingIdempotencyKey
	}
	if requestHash == "" {
		return nil, ErrMissingRequestHash
	}
	if len(items) == 0 {
		return nil, ErrEmptyOrder
	}
	if len(items) > maxOrderItems {
		return nil, ErrTooManyItems
	}
	if currency != CurrencyTHB {
		return nil, ErrUnsupportedCurrency
	}

	seenProducts := make(map[string]struct{}, len(items))
	var total Money
	for _, item := range items {
		if err := item.validate(); err != nil {
			return nil, err
		}
		if _, exists := seenProducts[item.ProductID()]; exists {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateProduct, item.ProductID())
		}
		seenProducts[item.ProductID()] = struct{}{}

		var err error
		total, err = AddMoney(total, item.Subtotal())
		if err != nil {
			return nil, err
		}
	}

	return &Order{
		id:             id,
		customerID:     customerID,
		idempotencyKey: idempotencyKey,
		requestHash:    requestHash,
		items:          append([]OrderItem(nil), items...),
		totalAmount:    total,
		currency:       currency,
		status:         OrderStatusPending,
		version:        1,
		createdAt:      now.UTC(),
		updatedAt:      now.UTC(),
	}, nil
}

func RehydrateOrder(snapshot OrderSnapshot) (*Order, error) {
	if snapshot.Version < 1 {
		return nil, ErrInvalidOrderVersion
	}
	if snapshot.CreatedAt.IsZero() || snapshot.UpdatedAt.IsZero() || snapshot.UpdatedAt.Before(snapshot.CreatedAt) {
		return nil, ErrInvalidOrderTimestamp
	}
	if !validOrderStatus(snapshot.Status) {
		return nil, ErrInvalidOrderStatus
	}
	if snapshot.Status == OrderStatusCancelled && snapshot.CancelReason == "" {
		return nil, ErrInvalidCancelReason
	}
	if snapshot.Status != OrderStatusCancelled && snapshot.CancelReason != "" {
		return nil, ErrInvalidCancelReason
	}

	base, err := NewOrder(
		snapshot.ID,
		snapshot.CustomerID,
		snapshot.IdempotencyKey,
		snapshot.RequestHash,
		snapshot.Items,
		snapshot.Currency,
		snapshot.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if base.totalAmount != snapshot.TotalAmount {
		return nil, ErrOrderTotalMismatch
	}

	base.status = snapshot.Status
	base.cancelReason = snapshot.CancelReason
	base.version = snapshot.Version
	base.createdAt = snapshot.CreatedAt.UTC()
	base.updatedAt = snapshot.UpdatedAt.UTC()
	return base, nil
}

func (o *Order) MarkInventoryReserved(now time.Time) error {
	return o.transition(OrderStatusPending, OrderStatusInventoryReserved, now)
}

func (o *Order) StartPayment(now time.Time) error {
	return o.transition(OrderStatusInventoryReserved, OrderStatusPaymentProcessing, now)
}

func (o *Order) Confirm(now time.Time) error {
	return o.transition(OrderStatusPaymentProcessing, OrderStatusConfirmed, now)
}

func (o *Order) Cancel(reason string, now time.Time) error {
	if reason == "" {
		return ErrInvalidCancelReason
	}
	if o.status == OrderStatusCancelled {
		return nil
	}
	if o.status == OrderStatusConfirmed {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidStateTransition, o.status, OrderStatusCancelled)
	}

	o.status = OrderStatusCancelled
	o.cancelReason = reason
	o.updatedAt = now.UTC()
	o.version++
	return nil
}

func (o *Order) transition(from, to OrderStatus, now time.Time) error {
	if o.status == to {
		return nil
	}
	if o.status != from {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidStateTransition, o.status, to)
	}

	o.status = to
	o.updatedAt = now.UTC()
	o.version++
	return nil
}

func (o *Order) ID() string             { return o.id }
func (o *Order) CustomerID() string     { return o.customerID }
func (o *Order) IdempotencyKey() string { return o.idempotencyKey }
func (o *Order) RequestHash() string    { return o.requestHash }
func (o *Order) TotalAmount() Money     { return o.totalAmount }
func (o *Order) Currency() string       { return o.currency }
func (o *Order) Status() OrderStatus    { return o.status }
func (o *Order) CancelReason() string   { return o.cancelReason }
func (o *Order) Version() int64         { return o.version }
func (o *Order) CreatedAt() time.Time   { return o.createdAt }
func (o *Order) UpdatedAt() time.Time   { return o.updatedAt }

func (o *Order) Items() []OrderItem {
	return append([]OrderItem(nil), o.items...)
}

// Clone returns a deep copy suitable for crossing repository boundaries.
// Callers cannot mutate the aggregate stored by an in-memory repository by
// retaining a pointer returned from another operation.
func (o *Order) Clone() *Order {
	clone := *o
	clone.items = append([]OrderItem(nil), o.items...)
	return &clone
}

func (o *Order) Snapshot() OrderSnapshot {
	return OrderSnapshot{
		ID:             o.id,
		CustomerID:     o.customerID,
		IdempotencyKey: o.idempotencyKey,
		RequestHash:    o.requestHash,
		Items:          append([]OrderItem(nil), o.items...),
		TotalAmount:    o.totalAmount,
		Currency:       o.currency,
		Status:         o.status,
		CancelReason:   o.cancelReason,
		Version:        o.version,
		CreatedAt:      o.createdAt,
		UpdatedAt:      o.updatedAt,
	}
}

func validOrderStatus(status OrderStatus) bool {
	switch status {
	case OrderStatusPending,
		OrderStatusInventoryReserved,
		OrderStatusPaymentProcessing,
		OrderStatusConfirmed,
		OrderStatusCancelled:
		return true
	default:
		return false
	}
}
