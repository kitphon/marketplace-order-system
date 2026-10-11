package grpcclient

import (
	"context"
	"errors"
	"fmt"
	"strings"

	productv1 "github.com/kitphon/marketplace-order-system/contracts/product/v1"
	"github.com/kitphon/marketplace-order-system/services/order/internal/domain"
	"github.com/kitphon/marketplace-order-system/services/order/internal/ports"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type ProductClient struct {
	client productv1.ProductServiceClient
	retry  *RetryPolicy
}

func NewProductClient(client productv1.ProductServiceClient, retry *RetryPolicy) *ProductClient {
	return &ProductClient{client: client, retry: retry}
}

func (client *ProductClient) GetByIDs(
	ctx context.Context,
	productIDs []string,
) ([]ports.ProductSnapshot, error) {
	if len(productIDs) == 0 {
		return nil, ports.ErrInvalidProductRequest
	}
	for _, productID := range productIDs {
		if strings.TrimSpace(productID) == "" {
			return nil, ports.ErrInvalidProductRequest
		}
	}

	var response *productv1.GetProductsResponse
	exhausted, err := client.retry.Do(ctx, func(attemptContext context.Context) error {
		var callErr error
		response, callErr = client.client.GetProducts(attemptContext, &productv1.GetProductsRequest{
			ProductIds: productIDs,
		})
		return callErr
	})
	if err != nil {
		return nil, mapProductError(ctx, exhausted, err)
	}
	return productSnapshots(response)
}

func productSnapshots(response *productv1.GetProductsResponse) ([]ports.ProductSnapshot, error) {
	if response == nil {
		return nil, ports.ErrMalformedProductResponse
	}
	snapshots := make([]ports.ProductSnapshot, 0, len(response.Products))
	seen := make(map[string]struct{}, len(response.Products))
	for _, product := range response.Products {
		if product == nil ||
			strings.TrimSpace(product.ProductId) == "" ||
			strings.TrimSpace(product.SellerId) == "" ||
			strings.TrimSpace(product.Name) == "" ||
			strings.TrimSpace(product.Currency) == "" ||
			product.UnitPriceMinor < 0 {
			return nil, ports.ErrMalformedProductResponse
		}
		if _, exists := seen[product.ProductId]; exists {
			return nil, ports.ErrMalformedProductResponse
		}
		seen[product.ProductId] = struct{}{}
		snapshots = append(snapshots, ports.ProductSnapshot{
			ProductID: product.ProductId,
			SellerID:  product.SellerId,
			Name:      product.Name,
			UnitPrice: domain.Money(product.UnitPriceMinor),
			Currency:  product.Currency,
			Active:    product.Active,
		})
	}
	return snapshots, nil
}

func mapProductError(ctx context.Context, exhausted bool, err error) error {
	if parentErr := ctx.Err(); parentErr != nil {
		return parentErr
	}
	if errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled {
		return fmt.Errorf("%w: upstream canceled request", ports.ErrProductUnavailable)
	}
	if exhausted {
		return fmt.Errorf("%w: %v", ports.ErrProductUnavailable, err)
	}
	if status.Code(err) == codes.InvalidArgument {
		return fmt.Errorf("%w: upstream rejected request", ports.ErrInvalidProductRequest)
	}
	return fmt.Errorf("%w: %v", ports.ErrProductUnavailable, err)
}

var _ ports.ProductClient = (*ProductClient)(nil)
