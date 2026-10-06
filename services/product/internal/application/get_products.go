package application

import (
	"context"
	"errors"
	"strings"

	"github.com/kitphon/marketplace-order-system/services/product/internal/domain"
	"github.com/kitphon/marketplace-order-system/services/product/internal/ports"
)

var ErrInvalidGetProductsRequest = errors.New("invalid get products request")

type GetProducts struct {
	products ports.ProductRepository
}

func NewGetProducts(products ports.ProductRepository) *GetProducts {
	return &GetProducts{products: products}
}

func (useCase *GetProducts) Execute(ctx context.Context, productIDs []string) ([]domain.Product, error) {
	if len(productIDs) == 0 {
		return nil, ErrInvalidGetProductsRequest
	}
	seen := make(map[string]struct{}, len(productIDs))
	for _, productID := range productIDs {
		if strings.TrimSpace(productID) == "" {
			return nil, ErrInvalidGetProductsRequest
		}
		if _, exists := seen[productID]; exists {
			return nil, ErrInvalidGetProductsRequest
		}
		seen[productID] = struct{}{}
	}
	return useCase.products.GetByIDs(ctx, productIDs)
}
