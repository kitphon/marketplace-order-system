package ports

import (
	"context"
	"time"
)

type ReservationItem struct {
	ProductID string
	Quantity  int
}

type ReserveInventoryRequest struct {
	OrderID  string
	Items    []ReservationItem
	ExpireAt time.Time
}

type InventoryClient interface {
	Reserve(ctx context.Context, request ReserveInventoryRequest) error
}

