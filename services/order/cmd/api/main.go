package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	inventoryv1 "github.com/kitphon/marketplace-order-system/contracts/inventory/v1"
	productv1 "github.com/kitphon/marketplace-order-system/contracts/product/v1"
	"github.com/kitphon/marketplace-order-system/services/order/internal/adapters/grpcclient"
	"github.com/kitphon/marketplace-order-system/services/order/internal/adapters/httpapi"
	mongoadapter "github.com/kitphon/marketplace-order-system/services/order/internal/adapters/mongodb"
	"github.com/kitphon/marketplace-order-system/services/order/internal/adapters/system"
	"github.com/kitphon/marketplace-order-system/services/order/internal/application"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const defaultRetryJitterFraction = 0.2

func main() {
	if err := run(); err != nil {
		slog.Error("order service stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	mongodbURI := envOrDefault("MONGODB_URI", "mongodb://localhost:27017/?replicaSet=rs0&directConnection=true")
	mongodbDatabase := envOrDefault("MONGODB_DATABASE", "order_db")
	httpAddress := envOrDefault("HTTP_ADDR", ":8080")
	productAddress := envOrDefault("PRODUCT_GRPC_ADDR", "localhost:50051")
	inventoryAddress := envOrDefault("INVENTORY_GRPC_ADDR", "localhost:50052")
	retryConfig, err := retryConfigFromEnvironment()
	if err != nil {
		return err
	}
	productRetry, err := grpcclient.NewRetryPolicy(retryConfig)
	if err != nil {
		return err
	}
	inventoryRetry, err := grpcclient.NewRetryPolicy(retryConfig)
	if err != nil {
		return err
	}

	client, err := mongo.Connect(options.Client().ApplyURI(mongodbURI))
	if err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Disconnect(ctx); err != nil {
			slog.Error("disconnect MongoDB", "error", err)
		}
	}()

	startupContext, cancelStartup := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelStartup()
	if err := client.Ping(startupContext, nil); err != nil {
		return err
	}

	orders := mongoadapter.NewOrderRepository(client.Database(mongodbDatabase))
	if err := orders.EnsureIndexes(startupContext); err != nil {
		return err
	}

	productConnection, err := grpc.NewClient(
		productAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return err
	}
	defer closeGRPCConnection("Product", productConnection)
	inventoryConnection, err := grpc.NewClient(
		inventoryAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return err
	}
	defer closeGRPCConnection("Inventory", inventoryConnection)

	products := grpcclient.NewProductClient(productv1.NewProductServiceClient(productConnection), productRetry)
	inventory := grpcclient.NewInventoryClient(inventoryv1.NewInventoryServiceClient(inventoryConnection), inventoryRetry)
	createOrder := application.NewCreateOrder(
		orders,
		products,
		inventory,
		system.IDGenerator{},
		system.Clock{},
	)
	readiness := func(ctx context.Context) error {
		return client.Ping(ctx, nil)
	}
	handler := httpapi.NewRouter(httpapi.NewOrderHandler(createOrder), readiness)

	server := &http.Server{
		Addr:              httpAddress,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("order service listening", "address", httpAddress)
		serverErrors <- server.ListenAndServe()
	}()

	signals, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-signals.Done():
	}

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	return server.Shutdown(shutdownContext)
}

func retryConfigFromEnvironment() (grpcclient.RetryConfig, error) {
	maximumAttempts, err := positiveIntEnv("GRPC_RETRY_MAX_ATTEMPTS", 3)
	if err != nil {
		return grpcclient.RetryConfig{}, err
	}
	perAttemptTimeout, err := positiveDurationEnv("GRPC_PER_ATTEMPT_TIMEOUT", 2*time.Second)
	if err != nil {
		return grpcclient.RetryConfig{}, err
	}
	initialBackoff, err := positiveDurationEnv("GRPC_RETRY_INITIAL_BACKOFF", 100*time.Millisecond)
	if err != nil {
		return grpcclient.RetryConfig{}, err
	}
	maximumBackoff, err := positiveDurationEnv("GRPC_RETRY_MAX_BACKOFF", time.Second)
	if err != nil {
		return grpcclient.RetryConfig{}, err
	}
	return grpcclient.RetryConfig{
		MaxAttempts:       maximumAttempts,
		PerAttemptTimeout: perAttemptTimeout,
		InitialBackoff:    initialBackoff,
		MaxBackoff:        maximumBackoff,
		JitterFraction:    defaultRetryJitterFraction,
	}, nil
}

func positiveIntEnv(key string, fallback int) (int, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, errors.New(key + " must be a positive integer")
	}
	return parsed, nil
}

func positiveDurationEnv(key string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, errors.New(key + " must be a positive duration")
	}
	return parsed, nil
}

func closeGRPCConnection(name string, connection *grpc.ClientConn) {
	if err := connection.Close(); err != nil {
		slog.Error("close gRPC connection", "service", name, "error", err)
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
