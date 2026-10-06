package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	inventoryv1 "github.com/kitphon/marketplace-order-system/contracts/inventory/v1"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/adapters/grpcserver"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/adapters/memory"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/adapters/system"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/application"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/domain"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

const (
	defaultAddress            = ":50052"
	defaultExpirationInterval = 5 * time.Second
)

func main() {
	if err := run(); err != nil {
		slog.Error("inventory service stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	address := envOrDefault("INVENTORY_GRPC_ADDR", defaultAddress)
	expirationInterval, err := durationEnv("INVENTORY_EXPIRATION_INTERVAL", defaultExpirationInterval)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()

	clock := system.Clock{}
	repository := memory.NewInventoryRepository(seedStock())
	reserve := application.NewReserveInventory(repository, clock, system.IDGenerator{})
	expire := application.NewExpireReservations(repository, clock)
	service := grpcserver.New(reserve)
	grpcServer := grpc.NewServer()
	inventoryv1.RegisterInventoryServiceServer(grpcServer, service)
	healthServer := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus(inventoryv1.InventoryService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_SERVING)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go application.RunExpirationLoop(ctx, expirationInterval, expire, func(err error) {
		slog.Error("expire inventory reservations", "error", err)
	})

	serverErrors := make(chan error, 1)
	go func() {
		slog.Info(
			"inventory gRPC service listening",
			"address", address,
			"expiration_interval", expirationInterval,
			"transport", "plaintext-local-development",
		)
		serverErrors <- grpcServer.Serve(listener)
	}()

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

func seedStock() []domain.Stock {
	return []domain.Stock{
		mustStock("product-001", 100),
		mustStock("product-002", 100),
	}
}

func mustStock(productID string, available int) domain.Stock {
	stock, err := domain.NewStock(productID, available)
	if err != nil {
		panic(err)
	}
	return stock
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return duration, nil
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
