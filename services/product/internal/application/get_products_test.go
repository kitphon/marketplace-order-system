package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kitphon/marketplace-order-system/services/product/internal/adapters/memory"
	"github.com/kitphon/marketplace-order-system/services/product/internal/application"
	"github.com/kitphon/marketplace-order-system/services/product/internal/domain"
)

func TestGetProductsReturnsFoundProductsInRequestOrder(t *testing.T) {
	keyboard := mustProduct(t, "product-001", "Keyboard")
	mouse := mustProduct(t, "product-002", "Mouse")
	useCase := application.NewGetProducts(memory.NewProductRepository([]domain.Product{keyboard, mouse}))

	products, err := useCase.Execute(context.Background(), []string{"product-002", "missing", "product-001"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(products) != 2 || products[0].ProductID() != "product-002" || products[1].ProductID() != "product-001" {
		t.Fatalf("products = %#v, want product-002 then product-001", products)
	}
}

func TestGetProductsRejectsInvalidIDs(t *testing.T) {
	useCase := application.NewGetProducts(memory.NewProductRepository(nil))
	tests := []struct {
		name string
		ids  []string
	}{
		{name: "empty", ids: nil},
		{name: "blank", ids: []string{"  "}},
		{name: "duplicate", ids: []string{"product-001", "product-001"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := useCase.Execute(context.Background(), test.ids)
			if !errors.Is(err, application.ErrInvalidGetProductsRequest) {
				t.Fatalf("Execute() error = %v, want ErrInvalidGetProductsRequest", err)
			}
		})
	}
}

func mustProduct(t *testing.T, id, name string) domain.Product {
	t.Helper()
	product, err := domain.NewProduct(id, "seller-001", name, 100, "THB", true)
	if err != nil {
		t.Fatalf("NewProduct() error = %v", err)
	}
	return product
}
