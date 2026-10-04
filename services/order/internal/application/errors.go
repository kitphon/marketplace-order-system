package application

import "errors"

var (
	ErrInvalidCreateOrderRequest = errors.New("invalid create order request")
	ErrIdempotencyKeyReused      = errors.New("idempotency key reused with a different request")
	ErrProductNotFound           = errors.New("product not found")
	ErrProductInactive           = errors.New("product is inactive")
	ErrUnexpectedProduct         = errors.New("product service returned an unexpected product")
)
