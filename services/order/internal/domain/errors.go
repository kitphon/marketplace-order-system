package domain

import "errors"

var (
	ErrEmptyOrder             = errors.New("order must contain at least one item")
	ErrTooManyItems           = errors.New("order cannot contain more than 100 items")
	ErrInvalidQuantity        = errors.New("item quantity must be greater than zero")
	ErrMissingProductID       = errors.New("product id is required")
	ErrMissingSellerID        = errors.New("seller id is required")
	ErrMissingProductName     = errors.New("product name is required")
	ErrDuplicateProduct       = errors.New("order cannot contain duplicate products")
	ErrMissingOrderID         = errors.New("order id is required")
	ErrMissingCustomerID      = errors.New("customer id is required")
	ErrMissingIdempotencyKey  = errors.New("idempotency key is required")
	ErrMissingRequestHash     = errors.New("request hash is required")
	ErrUnsupportedCurrency    = errors.New("only THB is supported")
	ErrInvalidCancelReason    = errors.New("cancel reason is required")
	ErrInvalidStateTransition = errors.New("invalid order state transition")
	ErrInvalidOrderStatus     = errors.New("invalid order status")
	ErrInvalidOrderVersion    = errors.New("order version must be greater than zero")
	ErrInvalidOrderTimestamp  = errors.New("invalid order timestamp")
	ErrOrderTotalMismatch     = errors.New("stored order total does not match item subtotals")
)
