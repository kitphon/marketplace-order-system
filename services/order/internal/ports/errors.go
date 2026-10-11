package ports

import "errors"

var (
	ErrOrderNotFound              = errors.New("order not found")
	ErrDuplicateIdempotency       = errors.New("duplicate idempotency identity")
	ErrOrderIDConflict            = errors.New("order id already exists")
	ErrVersionConflict            = errors.New("order version conflict")
	ErrInsufficientStock          = errors.New("insufficient stock")
	ErrReservationConflict        = errors.New("inventory reservation conflicts with an existing reservation")
	ErrReservationExpired         = errors.New("inventory reservation expired")
	ErrProductUnavailable         = errors.New("product service unavailable")
	ErrInventoryUnavailable       = errors.New("inventory service unavailable")
	ErrInvalidProductRequest      = errors.New("invalid product service request")
	ErrInvalidInventoryRequest    = errors.New("invalid inventory service request")
	ErrMalformedProductResponse   = errors.New("malformed product service response")
	ErrMalformedInventoryResponse = errors.New("malformed inventory service response")
)
