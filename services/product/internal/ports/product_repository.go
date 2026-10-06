package ports

import (
	"context"

	"github.com/kitphon/marketplace-order-system/services/product/internal/domain"
)

type ProductRepository interface {
	GetByIDs(ctx context.Context, productIDs []string) ([]domain.Product, error)
}
