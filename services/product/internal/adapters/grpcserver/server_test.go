package grpcserver_test

import (
	"context"
	"fmt"
	"net"
	"testing"

	productv1 "github.com/kitphon/marketplace-order-system/contracts/product/v1"
	"github.com/kitphon/marketplace-order-system/services/product/internal/adapters/grpcserver"
	"github.com/kitphon/marketplace-order-system/services/product/internal/adapters/memory"
	"github.com/kitphon/marketplace-order-system/services/product/internal/application"
	"github.com/kitphon/marketplace-order-system/services/product/internal/domain"
	"github.com/kitphon/marketplace-order-system/services/product/internal/ports"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestGetProductsMapping(t *testing.T) {
	product, err := domain.NewProduct("product-001", "seller-001", "Keyboard", 200_000, "THB", true)
	if err != nil {
		t.Fatalf("NewProduct() error = %v", err)
	}
	client := newClient(t, []domain.Product{product})

	response, err := client.GetProducts(context.Background(), &productv1.GetProductsRequest{
		ProductIds: []string{"missing", "product-001"},
	})
	if err != nil {
		t.Fatalf("GetProducts() error = %v", err)
	}
	if len(response.Products) != 1 {
		t.Fatalf("len(products) = %d, want 1", len(response.Products))
	}
	got := response.Products[0]
	if got.ProductId != "product-001" || got.SellerId != "seller-001" || got.Name != "Keyboard" || got.UnitPriceMinor != 200_000 || got.Currency != "THB" || !got.Active {
		t.Fatalf("unexpected product: %+v", got)
	}
}

func TestGetProductsInvalidRequestStatus(t *testing.T) {
	client := newClient(t, nil)
	_, err := client.GetProducts(context.Background(), &productv1.GetProductsRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("status = %v, want InvalidArgument; error=%v", status.Code(err), err)
	}
}

func TestGetProductsMapsRepositoryContextErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode codes.Code
	}{
		{name: "canceled", err: fmt.Errorf("repository query: %w", context.Canceled), wantCode: codes.Canceled},
		{name: "deadline exceeded", err: context.DeadlineExceeded, wantCode: codes.DeadlineExceeded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := grpcserver.New(application.NewGetProducts(failingRepository{err: test.err}))
			_, err := server.GetProducts(context.Background(), &productv1.GetProductsRequest{
				ProductIds: []string{"product-001"},
			})
			if status.Code(err) != test.wantCode {
				t.Fatalf("status = %v, want %v; error=%v", status.Code(err), test.wantCode, err)
			}
		})
	}
}

func newClient(t *testing.T, products []domain.Product) productv1.ProductServiceClient {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	repository := memory.NewProductRepository(products)
	productv1.RegisterProductServiceServer(server, grpcserver.New(application.NewGetProducts(repository)))
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(server.Stop)
	t.Cleanup(func() { _ = listener.Close() })

	connection, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient() error = %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return productv1.NewProductServiceClient(connection)
}

type failingRepository struct{ err error }

func (repository failingRepository) GetByIDs(context.Context, []string) ([]domain.Product, error) {
	return nil, repository.err
}

var _ ports.ProductRepository = failingRepository{}
