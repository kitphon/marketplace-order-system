package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/example/marketplace-order-system/services/order/internal/adapters/httpapi"
	"github.com/example/marketplace-order-system/services/order/internal/adapters/memory"
	mongoadapter "github.com/example/marketplace-order-system/services/order/internal/adapters/mongodb"
	"github.com/example/marketplace-order-system/services/order/internal/adapters/system"
	"github.com/example/marketplace-order-system/services/order/internal/application"
	"github.com/example/marketplace-order-system/services/order/internal/domain"
	"github.com/example/marketplace-order-system/services/order/internal/ports"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

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

	products := memory.NewProductClient([]ports.ProductSnapshot{
		{
			ProductID: "product-001",
			SellerID:  "seller-001",
			Name:      "Mechanical Keyboard",
			UnitPrice: domain.Money(200_000),
			Active:    true,
		},
		{
			ProductID: "product-002",
			SellerID:  "seller-001",
			Name:      "Wireless Mouse",
			UnitPrice: domain.Money(90_000),
			Active:    true,
		},
	})
	inventory := memory.NewInventoryClient(map[string]int{
		"product-001": 100,
		"product-002": 100,
	})
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

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
