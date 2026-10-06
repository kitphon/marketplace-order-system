package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	productv1 "github.com/kitphon/marketplace-order-system/contracts/product/v1"
	"github.com/kitphon/marketplace-order-system/services/product/internal/adapters/grpcserver"
	"github.com/kitphon/marketplace-order-system/services/product/internal/adapters/memory"
	"github.com/kitphon/marketplace-order-system/services/product/internal/application"
	"github.com/kitphon/marketplace-order-system/services/product/internal/domain"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

const defaultAddress = ":50051"

func main() {
	if err := run(); err != nil {
		slog.Error("product service stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	address := envOrDefault("PRODUCT_GRPC_ADDR", defaultAddress)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()

	products := memory.NewProductRepository(seedProducts())
	service := grpcserver.New(application.NewGetProducts(products))
	grpcServer := grpc.NewServer()
	productv1.RegisterProductServiceServer(grpcServer, service)
	healthServer := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus(productv1.ProductService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_SERVING)

	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("product gRPC service listening", "address", address, "transport", "plaintext-local-development")
		serverErrors <- grpcServer.Serve(listener)
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-serverErrors:
		if errors.Is(err, grpc.ErrServerStopped) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	healthServer.Shutdown()
	gracefulStop(grpcServer, 10*time.Second)
	return nil
}

func seedProducts() []domain.Product {
	return []domain.Product{
		mustProduct("product-001", "seller-001", "Mechanical Keyboard", 200_000, "THB", true),
		mustProduct("product-002", "seller-001", "Wireless Mouse", 90_000, "THB", true),
	}
}

func mustProduct(id, sellerID, name string, price int64, currency string, active bool) domain.Product {
	product, err := domain.NewProduct(id, sellerID, name, price, currency, active)
	if err != nil {
		panic(err)
	}
	return product
}

func gracefulStop(server *grpc.Server, timeout time.Duration) {
	stopped := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(timeout):
		server.Stop()
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
