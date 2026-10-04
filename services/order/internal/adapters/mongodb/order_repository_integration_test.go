//go:build integration

package mongodb

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/example/marketplace-order-system/services/order/internal/domain"
	"github.com/example/marketplace-order-system/services/order/internal/ports"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestOrderRepositoryIntegration(t *testing.T) {
	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		t.Skip("MONGODB_URI is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("mongo.Connect() error = %v", err)
	}
	defer func() {
		if err := client.Disconnect(context.Background()); err != nil {
			t.Errorf("Disconnect() error = %v", err)
		}
	}()
	if err := client.Ping(ctx, nil); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}

	database := client.Database("order_repository_integration")
	if err := database.Drop(context.Background()); err != nil {
		t.Fatalf("initial Drop() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Drop(context.Background()); err != nil {
			t.Errorf("cleanup Drop() error = %v", err)
		}
	})

	repository := NewOrderRepository(database)
	if err := repository.EnsureIndexes(ctx); err != nil {
		t.Fatalf("EnsureIndexes() error = %v", err)
	}

	order := integrationOrder(t, "order-001")
	if err := repository.Create(ctx, order); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	duplicate := integrationOrder(t, "order-002")
	if err := repository.Create(ctx, duplicate); !errors.Is(err, ports.ErrDuplicateIdempotency) {
		t.Fatalf("duplicate Create() error = %v, want ErrDuplicateIdempotency", err)
	}

	loaded, err := repository.FindByIdempotencyKey(ctx, "customer-001", "checkout-001")
	if err != nil {
		t.Fatalf("FindByIdempotencyKey() error = %v", err)
	}
	if loaded.ID() != order.ID() {
		t.Fatalf("loaded ID = %q, want %q", loaded.ID(), order.ID())
	}

	stale := loaded.Clone()
	expectedVersion := loaded.Version()
	if err := loaded.MarkInventoryReserved(time.Now()); err != nil {
		t.Fatalf("MarkInventoryReserved() error = %v", err)
	}
	if err := repository.Update(ctx, loaded, expectedVersion); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if err := stale.Cancel("STALE_WORKER", time.Now()); err != nil {
		t.Fatalf("stale Cancel() error = %v", err)
	}
	if err := repository.Update(ctx, stale, expectedVersion); !errors.Is(err, ports.ErrVersionConflict) {
		t.Fatalf("stale Update() error = %v, want ErrVersionConflict", err)
	}
}

func integrationOrder(t *testing.T, id string) *domain.Order {
	t.Helper()
	price, err := domain.NewMoney(200_000)
	if err != nil {
		t.Fatalf("NewMoney() error = %v", err)
	}
	item, err := domain.NewOrderItem(
		"product-001",
		"seller-001",
		"Keyboard",
		price,
		2,
	)
	if err != nil {
		t.Fatalf("NewOrderItem() error = %v", err)
	}
	order, err := domain.NewOrder(
		id,
		"customer-001",
		"checkout-001",
		"sha256:request-001",
		[]domain.OrderItem{item},
		domain.CurrencyTHB,
		time.Now(),
	)
	if err != nil {
		t.Fatalf("NewOrder() error = %v", err)
	}
	return order
}

