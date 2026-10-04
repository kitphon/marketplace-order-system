package ports

import (
	"context"

	"github.com/example/marketplace-order-system/services/order/internal/domain"
)

type ProductSnapshot struct {
	ProductID string
	SellerID  string
	Name      string
	UnitPrice domain.Money
	Active    bool
}

type ProductClient interface {
	GetByIDs(ctx context.Context, productIDs []string) ([]ProductSnapshot, error)
}
