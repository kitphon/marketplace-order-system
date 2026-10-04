package memory

import (
	"context"
	"sync"

	"github.com/example/marketplace-order-system/services/order/internal/ports"
)

type ProductClient struct {
	mu       sync.RWMutex
	products map[string]ports.ProductSnapshot
}

func NewProductClient(products []ports.ProductSnapshot) *ProductClient {
	client := &ProductClient{products: make(map[string]ports.ProductSnapshot, len(products))}
	for _, product := range products {
		client.products[product.ProductID] = product
	}
	return client
}

func (client *ProductClient) GetByIDs(
	ctx context.Context,
	productIDs []string,
) ([]ports.ProductSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	client.mu.RLock()
	defer client.mu.RUnlock()
	result := make([]ports.ProductSnapshot, 0, len(productIDs))
	for _, productID := range productIDs {
		if product, exists := client.products[productID]; exists {
			result = append(result, product)
		}
	}
	return result, nil
}

