package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	inventoryv1 "github.com/kitphon/marketplace-order-system/contracts/inventory/v1"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/adapters/grpcserver"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/adapters/mongodb"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/adapters/system"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/application"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/domain"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

const (
	defaultAddress            = ":50052"
	defaultExpirationInterval = 5 * time.Second
	defaultMongoDBURI         = "mongodb://localhost:27017/?replicaSet=rs0&directConnection=true"
	defaultMongoDBDatabase    = "inventory_db"
	startupTimeout            = 10 * time.Second
	shutdownTimeout           = 10 * time.Second
)

type config struct {
	address            string
	expirationInterval time.Duration
	mongodbURI         string
	mongodbDatabase    string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		slog.Error("inventory service stopped", "error", err)
		os.Exit(1)
	}
}

func loadConfig() (config, error) {
	expirationInterval, err := durationEnv("INVENTORY_EXPIRATION_INTERVAL", defaultExpirationInterval)
	if err != nil {
		return config{}, err
	}
	address := envOrDefault("INVENTORY_GRPC_ADDR", defaultAddress)
	if _, err := net.ResolveTCPAddr("tcp", address); err != nil {
		return config{}, fmt.Errorf("INVENTORY_GRPC_ADDR is invalid: %w", err)
	}
	mongodbURI := strings.TrimSpace(envOrDefault("MONGODB_URI", defaultMongoDBURI))
	if mongodbURI == "" {
		return config{}, fmt.Errorf("MONGODB_URI must not be blank")
	}
	mongodbDatabase := strings.TrimSpace(envOrDefault("MONGODB_DATABASE", defaultMongoDBDatabase))
	if mongodbDatabase == "" {
		return config{}, fmt.Errorf("MONGODB_DATABASE must not be blank")
	}
	return config{
		address:            address,
		expirationInterval: expirationInterval,
		mongodbURI:         mongodbURI,
		mongodbDatabase:    mongodbDatabase,
	}, nil
}

func run(ctx context.Context) error {
	configuration, err := loadConfig()
	if err != nil {
		return err
	}

	client, err := mongo.Connect(options.Client().ApplyURI(configuration.mongodbURI))
	if err != nil {
		return fmt.Errorf("connect to MongoDB: %w", err)
	}
	defer disconnectMongo(client)

	startupContext, cancelStartup := context.WithTimeout(ctx, startupTimeout)
	defer cancelStartup()
	if err := client.Ping(startupContext, nil); err != nil {
		return fmt.Errorf("ping MongoDB: %w", err)
	}
	repository := mongodb.NewInventoryRepository(client.Database(configuration.mongodbDatabase))
	if err := repository.EnsureIndexes(startupContext); err != nil {
		return fmt.Errorf("initialize Inventory indexes: %w", err)
	}
	if err := repository.SeedStocks(startupContext, seedStock()); err != nil {
		return fmt.Errorf("seed Inventory stock: %w", err)
	}
	cancelStartup()

	listener, err := net.Listen("tcp", configuration.address)
	if err != nil {
		return err
	}
	defer listener.Close()

	clock := system.Clock{}
	reserve := application.NewReserveInventory(repository, clock, system.IDGenerator{})
	getReservation := application.NewGetReservationByOrderID(repository)
	expire := application.NewExpireReservations(repository, clock)
	service := grpcserver.New(reserve, getReservation)
	grpcServer := grpc.NewServer()
	inventoryv1.RegisterInventoryServiceServer(grpcServer, service)
	healthServer := health.NewServer()
	healthpb.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus(inventoryv1.InventoryService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_SERVING)

	runtimeContext, stopRuntime := context.WithCancel(ctx)
	defer stopRuntime()
	expirationDone := make(chan struct{})
	go func() {
		defer close(expirationDone)
		application.RunExpirationLoop(runtimeContext, configuration.expirationInterval, expire, func(err error) {
			slog.Error("expire inventory reservations", "error", err)
		})
	}()

	serverErrors := make(chan error, 1)
	go func() {
		slog.Info(
			"inventory gRPC service listening",
			"address", configuration.address,
			"expiration_interval", configuration.expirationInterval,
			"mongodb_database", configuration.mongodbDatabase,
			"transport", "plaintext-local-development",
		)
		serverErrors <- grpcServer.Serve(listener)
	}()

	var serviceErr error
	select {
	case err := <-serverErrors:
		if !errors.Is(err, grpc.ErrServerStopped) {
			serviceErr = err
		}
	case <-ctx.Done():
	}

	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	healthServer.SetServingStatus(
		inventoryv1.InventoryService_ServiceDesc.ServiceName,
		healthpb.HealthCheckResponse_NOT_SERVING,
	)
	healthServer.Shutdown()
	stopRuntime()
	gracefulStop(grpcServer, shutdownTimeout)
	waitForExpirationLoop(expirationDone, shutdownTimeout)
	return serviceErr
}

func waitForExpirationLoop(done <-chan struct{}, timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		slog.Warn("timed out waiting for Inventory expiration loop to stop")
	}
}

func disconnectMongo(client *mongo.Client) {
	shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := client.Disconnect(shutdownContext); err != nil {
		slog.Error("disconnect Inventory MongoDB client", "error", err)
	}
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
