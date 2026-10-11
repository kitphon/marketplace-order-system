package grpcclient

import (
	"context"
	"errors"
	"testing"
	"time"

	productv1 "github.com/kitphon/marketplace-order-system/contracts/product/v1"
	"github.com/kitphon/marketplace-order-system/services/order/internal/ports"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestProductClientMapsResponseAndPreservesPartialResults(t *testing.T) {
	service := &fakeProductServiceClient{responses: []*productv1.GetProductsResponse{{
		Products: []*productv1.Product{{
			ProductId:      "product-001",
			SellerId:       "seller-001",
			Name:           "Keyboard",
			UnitPriceMinor: 200_000,
			Currency:       "THB",
			Active:         true,
		}},
	}}}
	client := NewProductClient(service, testPolicy(t, 3))

	products, err := client.GetByIDs(context.Background(), []string{"product-001", "missing"})
	if err != nil {
		t.Fatalf("GetByIDs() error = %v", err)
	}
	if service.calls != 1 || len(service.requests) != 1 {
		t.Fatalf("calls = %d, requests = %d; want one", service.calls, len(service.requests))
	}
	if got := service.requests[0].ProductIds; len(got) != 2 || got[0] != "product-001" || got[1] != "missing" {
		t.Fatalf("product_ids = %v", got)
	}
	if len(products) != 1 {
		t.Fatalf("products = %v, want one partial result", products)
	}
	product := products[0]
	if product.ProductID != "product-001" || product.SellerID != "seller-001" ||
		product.Name != "Keyboard" || product.UnitPrice.MinorUnits() != 200_000 ||
		product.Currency != "THB" || !product.Active {
		t.Fatalf("product = %+v", product)
	}
}

func TestProductClientRejectsMalformedResponse(t *testing.T) {
	service := &fakeProductServiceClient{responses: []*productv1.GetProductsResponse{{
		Products: []*productv1.Product{{
			ProductId: "product-001", SellerId: "seller-001", Name: "Keyboard",
			UnitPriceMinor: -1, Currency: "THB", Active: true,
		}},
	}}}
	client := NewProductClient(service, testPolicy(t, 3))
	_, err := client.GetByIDs(context.Background(), []string{"product-001"})
	if !errors.Is(err, ports.ErrMalformedProductResponse) {
		t.Fatalf("GetByIDs() error = %v, want ErrMalformedProductResponse", err)
	}
}

func TestProductClientRetriesTransientFailureThenSucceeds(t *testing.T) {
	service := &fakeProductServiceClient{
		responses: []*productv1.GetProductsResponse{nil, {Products: []*productv1.Product{}}},
		errors:    []error{status.Error(codes.Unavailable, "temporary"), nil},
	}
	client := NewProductClient(service, testPolicy(t, 3))
	if _, err := client.GetByIDs(context.Background(), []string{"product-001"}); err != nil {
		t.Fatalf("GetByIDs() error = %v", err)
	}
	if service.calls != 2 {
		t.Fatalf("calls = %d, want 2", service.calls)
	}
}

func TestProductClientRetriesPerAttemptDeadlineWhileParentActive(t *testing.T) {
	service := &fakeProductServiceClient{
		responses: []*productv1.GetProductsResponse{nil, {Products: []*productv1.Product{}}},
		errors:    []error{context.DeadlineExceeded, nil},
	}
	client := NewProductClient(service, testPolicy(t, 3))
	if _, err := client.GetByIDs(context.Background(), []string{"product-001"}); err != nil {
		t.Fatalf("GetByIDs() error = %v", err)
	}
	if service.calls != 2 {
		t.Fatalf("calls = %d, want 2", service.calls)
	}
}

func TestProductClientRetryExhaustionMapsUnavailable(t *testing.T) {
	service := &fakeProductServiceClient{errors: []error{
		status.Error(codes.Unavailable, "one"),
		status.Error(codes.Unavailable, "two"),
		status.Error(codes.Unavailable, "three"),
	}}
	client := NewProductClient(service, testPolicy(t, 3))
	_, err := client.GetByIDs(context.Background(), []string{"product-001"})
	if !errors.Is(err, ports.ErrProductUnavailable) || service.calls != 3 {
		t.Fatalf("GetByIDs() error = %v, calls = %d", err, service.calls)
	}
}

func TestProductClientNonRetryableFailureUsesOneAttempt(t *testing.T) {
	service := &fakeProductServiceClient{errors: []error{status.Error(codes.InvalidArgument, "bad request")}}
	client := NewProductClient(service, testPolicy(t, 3))
	_, err := client.GetByIDs(context.Background(), []string{"product-001"})
	if !errors.Is(err, ports.ErrInvalidProductRequest) || service.calls != 1 {
		t.Fatalf("GetByIDs() error = %v, calls = %d", err, service.calls)
	}
}

func TestProductClientPropagatesCanceledOrExpiredParentWithoutAttempt(t *testing.T) {
	tests := []struct {
		name    string
		context func() (context.Context, context.CancelFunc)
		want    error
	}{
		{name: "canceled", context: func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, func() {}
		}, want: context.Canceled},
		{name: "deadline", context: func() (context.Context, context.CancelFunc) {
			return context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		}, want: context.DeadlineExceeded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeProductServiceClient{}
			client := NewProductClient(service, testPolicy(t, 3))
			ctx, cancel := test.context()
			defer cancel()
			_, err := client.GetByIDs(ctx, []string{"product-001"})
			if !errors.Is(err, test.want) || service.calls != 0 {
				t.Fatalf("GetByIDs() error = %v, calls = %d", err, service.calls)
			}
		})
	}
}

type fakeProductServiceClient struct {
	calls     int
	requests  []*productv1.GetProductsRequest
	responses []*productv1.GetProductsResponse
	errors    []error
}

func (client *fakeProductServiceClient) GetProducts(
	_ context.Context,
	request *productv1.GetProductsRequest,
	_ ...grpc.CallOption,
) (*productv1.GetProductsResponse, error) {
	index := client.calls
	client.calls++
	client.requests = append(client.requests, request)
	var response *productv1.GetProductsResponse
	if index < len(client.responses) {
		response = client.responses[index]
	}
	var err error
	if index < len(client.errors) {
		err = client.errors[index]
	}
	return response, err
}

func testPolicy(t *testing.T, maxAttempts int) *RetryPolicy {
	t.Helper()
	config := defaultTestRetryConfig()
	config.MaxAttempts = maxAttempts
	return newTestRetryPolicy(t, config, &recordingWaiter{}, fixedRandom(0.5))
}
