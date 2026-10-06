package ports

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kitphon/marketplace-order-system/services/inventory/internal/domain"
)

var (
	ErrInsufficientStock   = errors.New("insufficient stock")
	ErrReservationConflict = errors.New("reservation conflict")
	ErrReservationExpired  = errors.New("reservation expired")
)

type InsufficientStockError struct {
	ProductID string
}

func (err *InsufficientStockError) Error() string {
	return fmt.Sprintf("%s: %s", ErrInsufficientStock, err.ProductID)
}

func (err *InsufficientStockError) Unwrap() error { return ErrInsufficientStock }

type ReserveRequest struct {
	ReservationID string
	OrderID       string
	Items         []domain.ReservationItem
	ExpiresAt     time.Time
	Now           time.Time
}

type InventoryRepository interface {
	Reserve(ctx context.Context, request ReserveRequest) (*domain.Reservation, error)
	ExpireReservations(ctx context.Context, now time.Time) (int, error)
}
