package memory

import (
	"context"
	"sync"

	"github.com/kitphon/marketplace-order-system/services/product/internal/domain"
)

type ProductRepository struct {
	mu       sync.RWMutex
	products map[string]domain.Product
}

func NewProductRepository(products []domain.Product) *ProductRepository {
	repository := &ProductRepository{products: make(map[string]domain.Product, len(products))}
	for _, product := range products {
		repository.products[product.ProductID()] = product
	}
	return repository
}

func (repository *ProductRepository) GetByIDs(
	ctx context.Context,
	productIDs []string,
) ([]domain.Product, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	repository.mu.RLock()
	defer repository.mu.RUnlock()
	products := make([]domain.Product, 0, len(productIDs))
	for _, productID := range productIDs {
		if product, exists := repository.products[productID]; exists {
			products = append(products, product)
		}
	}
	return products, nil
}
