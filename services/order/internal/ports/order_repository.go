package ports

import (
	"context"

	"github.com/example/marketplace-order-system/services/order/internal/domain"
)

type OrderRepository interface {
	FindByIdempotencyKey(
		ctx context.Context,
		customerID string,
		idempotencyKey string,
	) (*domain.Order, error)

	Create(ctx context.Context, order *domain.Order) error

	Update(
		ctx context.Context,
		order *domain.Order,
		expectedVersion int64,
	) error
}
