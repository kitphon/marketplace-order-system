package ports

import "errors"

var (
	ErrOrderNotFound          = errors.New("order not found")
	ErrDuplicateIdempotency   = errors.New("duplicate idempotency identity")
	ErrOrderIDConflict        = errors.New("order id already exists")
	ErrVersionConflict        = errors.New("order version conflict")
	ErrInsufficientStock      = errors.New("insufficient stock")
	ErrReservationConflict    = errors.New("inventory reservation conflicts with an existing reservation")
)
