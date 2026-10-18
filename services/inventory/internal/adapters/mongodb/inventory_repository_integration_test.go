//go:build integration

package mongodb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/kitphon/marketplace-order-system/services/inventory/internal/domain"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/ports"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestInventoryRepositoryIntegration(t *testing.T) {
	harness := newIntegrationHarness(t)

	t.Run("seed inserts missing stock and preserves existing state", func(t *testing.T) {
		harness.reset(t)
		harness.seed(t, map[string]int{"product-001": 10})
		assertStock(t, harness.database, "product-001", 10, 1)
		_, err := harness.database.Collection(stocksCollection).UpdateOne(
			context.Background(),
			bson.D{{Key: "_id", Value: "product-001"}},
			bson.D{{Key: "$set", Value: bson.D{{Key: "available", Value: 7}, {Key: "version", Value: 9}}}},
		)
		if err != nil {
			t.Fatalf("modify seeded stock: %v", err)
		}
		harness.seed(t, map[string]int{"product-001": 100, "product-002": 5})
		assertStock(t, harness.database, "product-001", 7, 9)
		assertStock(t, harness.database, "product-002", 5, 1)
	})

	t.Run("concurrent seed creates one stock without resetting it", func(t *testing.T) {
		harness.reset(t)
		stock := mustStock(t, "product-001", 10)
		start := make(chan struct{})
		errorsByWorker := make(chan error, 2)
		var wait sync.WaitGroup
		for range 2 {
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start
				errorsByWorker <- NewInventoryRepository(harness.database).SeedStocks(
					context.Background(),
					[]domain.Stock{stock},
				)
			}()
		}
		close(start)
		wait.Wait()
		close(errorsByWorker)
		for err := range errorsByWorker {
			if err != nil {
				t.Fatalf("concurrent SeedStocks() error = %v", err)
			}
		}
		assertStock(t, harness.database, "product-001", 10, 1)
		count, err := harness.database.Collection(stocksCollection).CountDocuments(context.Background(), bson.D{})
		if err != nil {
			t.Fatalf("count stocks: %v", err)
		}
		if count != 1 {
			t.Fatalf("stock count = %d, want 1", count)
		}
	})

	t.Run("multi-item reservation persists and survives repository recreation", func(t *testing.T) {
		harness.reset(t)
		harness.seed(t, map[string]int{"product-001": 10, "product-002": 8})
		request := reserveRequest(t, "reservation-001", "order-001", harness.now, map[string]int{
			"product-002": 3,
			"product-001": 2,
		})
		request.ExpiresAt = request.ExpiresAt.Add(123456 * time.Nanosecond)
		first, err := harness.repository.Reserve(context.Background(), request)
		if err != nil {
			t.Fatalf("Reserve() error = %v", err)
		}
		if first.ID() != "reservation-001" || first.Status() != domain.ReservationStatusReserved {
			t.Fatalf("reservation = %+v", first)
		}
		assertStock(t, harness.database, "product-001", 8, 2)
		assertStock(t, harness.database, "product-002", 5, 2)
		assertReservationCount(t, harness.database, bson.D{{Key: "order_id", Value: "order-001"}}, 1)

		secondRepository := NewInventoryRepository(harness.database)
		retry := request
		retry.ReservationID = "reservation-loser"
		retry.ExpiresAt = request.ExpiresAt.Add(time.Hour)
		second, err := secondRepository.Reserve(context.Background(), retry)
		if err != nil {
			t.Fatalf("retry Reserve() error = %v", err)
		}
		if second.ID() != first.ID() || !second.ExpiresAt().Equal(first.ExpiresAt()) {
			t.Fatalf("retry = %+v, want original identity and expiry %+v", second, first)
		}
		assertStock(t, harness.database, "product-001", 8, 2)
		assertStock(t, harness.database, "product-002", 5, 2)
	})

	t.Run("same order with different payload conflicts", func(t *testing.T) {
		harness.reset(t)
		harness.seed(t, map[string]int{"product-001": 10})
		if _, err := harness.repository.Reserve(context.Background(), reserveRequest(
			t, "reservation-001", "order-001", harness.now, map[string]int{"product-001": 2},
		)); err != nil {
			t.Fatalf("first Reserve() error = %v", err)
		}
		_, err := harness.repository.Reserve(context.Background(), reserveRequest(
			t, "reservation-002", "order-001", harness.now, map[string]int{"product-001": 3},
		))
		if !errors.Is(err, ports.ErrReservationConflict) {
			t.Fatalf("Reserve() error = %v, want ErrReservationConflict", err)
		}
		assertStock(t, harness.database, "product-001", 8, 2)
	})

	t.Run("insufficient and missing items roll back every stock change", func(t *testing.T) {
		for _, test := range []struct {
			name   string
			stocks map[string]int
			items  map[string]int
			failed string
		}{
			{
				name:   "insufficient",
				stocks: map[string]int{"a-product": 10, "z-product": 1},
				items:  map[string]int{"a-product": 4, "z-product": 2},
				failed: "z-product",
			},
			{
				name:   "missing",
				stocks: map[string]int{"a-product": 10},
				items:  map[string]int{"a-product": 4, "z-missing": 1},
				failed: "z-missing",
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				harness.reset(t)
				harness.seed(t, test.stocks)
				_, err := harness.repository.Reserve(context.Background(), reserveRequest(
					t, "reservation-001", "order-001", harness.now, test.items,
				))
				var insufficient *ports.InsufficientStockError
				if !errors.As(err, &insufficient) || insufficient.ProductID != test.failed {
					t.Fatalf("Reserve() error = %v, want insufficient %q", err, test.failed)
				}
				assertStock(t, harness.database, "a-product", 10, 1)
				assertReservationCount(t, harness.database, bson.D{}, 0)
			})
		}
	})

	t.Run("different orders racing for one unit never make stock negative", func(t *testing.T) {
		harness.reset(t)
		harness.seed(t, map[string]int{"product-001": 1})
		repositories := []*InventoryRepository{
			NewInventoryRepository(harness.database),
			NewInventoryRepository(harness.database),
		}
		start := make(chan struct{})
		errorsByWorker := make(chan error, 2)
		var wait sync.WaitGroup
		for index, repository := range repositories {
			wait.Add(1)
			go func(index int, repository *InventoryRepository) {
				defer wait.Done()
				<-start
				_, err := repository.Reserve(context.Background(), reserveRequest(
					t,
					fmt.Sprintf("reservation-%d", index),
					fmt.Sprintf("order-%d", index),
					harness.now,
					map[string]int{"product-001": 1},
				))
				errorsByWorker <- err
			}(index, repository)
		}
		close(start)
		wait.Wait()
		close(errorsByWorker)
		successes, insufficient := 0, 0
		for err := range errorsByWorker {
			switch {
			case err == nil:
				successes++
			case errors.Is(err, ports.ErrInsufficientStock):
				insufficient++
			default:
				t.Fatalf("concurrent Reserve() error = %v", err)
			}
		}
		if successes != 1 || insufficient != 1 {
			t.Fatalf("successes=%d insufficient=%d, want 1 each", successes, insufficient)
		}
		assertStock(t, harness.database, "product-001", 0, 2)
		assertReservationCount(t, harness.database, bson.D{}, 1)
	})

	t.Run("concurrent identical calls share one reservation and decrement", func(t *testing.T) {
		harness.reset(t)
		harness.seed(t, map[string]int{"product-001": 10})
		repositories := []*InventoryRepository{
			NewInventoryRepository(harness.database),
			NewInventoryRepository(harness.database),
		}
		start := make(chan struct{})
		ids := make(chan string, 2)
		errorsByWorker := make(chan error, 2)
		var wait sync.WaitGroup
		for index, repository := range repositories {
			wait.Add(1)
			go func(index int, repository *InventoryRepository) {
				defer wait.Done()
				<-start
				reservation, err := repository.Reserve(context.Background(), reserveRequest(
					t,
					fmt.Sprintf("reservation-%d", index),
					"order-shared",
					harness.now,
					map[string]int{"product-001": 2},
				))
				if err != nil {
					errorsByWorker <- err
					return
				}
				ids <- reservation.ID()
			}(index, repository)
		}
		close(start)
		wait.Wait()
		close(ids)
		close(errorsByWorker)
		for err := range errorsByWorker {
			t.Fatalf("concurrent Reserve() error = %v", err)
		}
		winner := ""
		for id := range ids {
			if winner == "" {
				winner = id
			}
			if id != winner {
				t.Fatalf("reservation id = %q, want winner %q", id, winner)
			}
		}
		assertStock(t, harness.database, "product-001", 8, 2)
		assertReservationCount(t, harness.database, bson.D{{Key: "order_id", Value: "order-shared"}}, 1)
	})

	t.Run("expiration is durable and restores stock exactly once", func(t *testing.T) {
		harness.reset(t)
		harness.seed(t, map[string]int{"product-001": 10, "product-002": 20})
		request := reserveRequest(t, "reservation-001", "order-001", harness.now, map[string]int{
			"product-001": 2,
			"product-002": 3,
		})
		if _, err := harness.repository.Reserve(context.Background(), request); err != nil {
			t.Fatalf("Reserve() error = %v", err)
		}
		expirationTime := request.ExpiresAt.Add(time.Minute)
		first, err := harness.repository.ExpireReservations(context.Background(), expirationTime)
		if err != nil {
			t.Fatalf("first ExpireReservations() error = %v", err)
		}
		second, err := NewInventoryRepository(harness.database).ExpireReservations(context.Background(), expirationTime)
		if err != nil {
			t.Fatalf("second ExpireReservations() error = %v", err)
		}
		if first != 1 || second != 0 {
			t.Fatalf("expiration counts = (%d, %d), want (1, 0)", first, second)
		}
		assertStock(t, harness.database, "product-001", 10, 3)
		assertStock(t, harness.database, "product-002", 20, 3)
		assertReservationStatus(t, harness.database, "reservation-001", domain.ReservationStatusExpired, 2)

		_, err = NewInventoryRepository(harness.database).Reserve(context.Background(), request)
		if !errors.Is(err, ports.ErrReservationExpired) {
			t.Fatalf("tombstone retry error = %v, want ErrReservationExpired", err)
		}
	})

	t.Run("different payload conflicts after reservation is expired", func(t *testing.T) {
		harness.reset(t)
		harness.seed(t, map[string]int{"product-001": 10})
		request := reserveRequest(t, "reservation-001", "order-001", harness.now, map[string]int{"product-001": 3})
		if _, err := harness.repository.Reserve(context.Background(), request); err != nil {
			t.Fatalf("Reserve() error = %v", err)
		}
		if count, err := harness.repository.ExpireReservations(
			context.Background(),
			request.ExpiresAt.Add(time.Minute),
		); err != nil || count != 1 {
			t.Fatalf("ExpireReservations() = (%d, %v), want (1, nil)", count, err)
		}

		conflict := reserveRequest(
			t,
			"reservation-002",
			"order-001",
			harness.now,
			map[string]int{"product-001": 4},
		)
		_, err := NewInventoryRepository(harness.database).Reserve(context.Background(), conflict)
		if !errors.Is(err, ports.ErrReservationConflict) {
			t.Fatalf("conflicting retry error = %v, want ErrReservationConflict", err)
		}
		assertStock(t, harness.database, "product-001", 10, 3)
		assertReservationStatus(t, harness.database, "reservation-001", domain.ReservationStatusExpired, 2)
	})

	t.Run("different payload conflict does not lazily expire elapsed reservation", func(t *testing.T) {
		harness.reset(t)
		harness.seed(t, map[string]int{"product-001": 10})
		request := reserveRequest(t, "reservation-001", "order-001", harness.now, map[string]int{"product-001": 3})
		if _, err := harness.repository.Reserve(context.Background(), request); err != nil {
			t.Fatalf("Reserve() error = %v", err)
		}

		conflict := reserveRequest(
			t,
			"reservation-002",
			"order-001",
			request.ExpiresAt.Add(time.Minute),
			map[string]int{"product-001": 4},
		)
		_, err := NewInventoryRepository(harness.database).Reserve(context.Background(), conflict)
		if !errors.Is(err, ports.ErrReservationConflict) {
			t.Fatalf("conflicting retry error = %v, want ErrReservationConflict", err)
		}
		assertStock(t, harness.database, "product-001", 7, 2)
		assertReservationStatus(t, harness.database, "reservation-001", domain.ReservationStatusReserved, 1)

		first, err := harness.repository.ExpireReservations(context.Background(), conflict.Now)
		if err != nil {
			t.Fatalf("first ExpireReservations() error = %v", err)
		}
		second, err := harness.repository.ExpireReservations(context.Background(), conflict.Now)
		if err != nil {
			t.Fatalf("second ExpireReservations() error = %v", err)
		}
		if first != 1 || second != 0 {
			t.Fatalf("expiration counts = (%d, %d), want (1, 0)", first, second)
		}
		assertStock(t, harness.database, "product-001", 10, 3)
		assertReservationStatus(t, harness.database, "reservation-001", domain.ReservationStatusExpired, 2)
	})

	t.Run("concurrent expiration workers restore stock once", func(t *testing.T) {
		harness.reset(t)
		harness.seed(t, map[string]int{"product-001": 10})
		request := reserveRequest(t, "reservation-001", "order-001", harness.now, map[string]int{"product-001": 4})
		if _, err := harness.repository.Reserve(context.Background(), request); err != nil {
			t.Fatalf("Reserve() error = %v", err)
		}
		start := make(chan struct{})
		counts := make(chan int, 2)
		errorsByWorker := make(chan error, 2)
		var wait sync.WaitGroup
		for range 2 {
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start
				count, err := NewInventoryRepository(harness.database).ExpireReservations(
					context.Background(),
					request.ExpiresAt.Add(time.Minute),
				)
				if err != nil {
					errorsByWorker <- err
					return
				}
				counts <- count
			}()
		}
		close(start)
		wait.Wait()
		close(counts)
		close(errorsByWorker)
		for err := range errorsByWorker {
			t.Fatalf("concurrent expiration error = %v", err)
		}
		total := 0
		for count := range counts {
			total += count
		}
		if total != 1 {
			t.Fatalf("total expiration count = %d, want 1", total)
		}
		assertStock(t, harness.database, "product-001", 10, 3)
	})

	t.Run("lazy expiration commits before returning expired", func(t *testing.T) {
		harness.reset(t)
		harness.seed(t, map[string]int{"product-001": 10})
		request := reserveRequest(t, "reservation-001", "order-001", harness.now, map[string]int{"product-001": 3})
		if _, err := harness.repository.Reserve(context.Background(), request); err != nil {
			t.Fatalf("Reserve() error = %v", err)
		}
		retry := request
		retry.ReservationID = "reservation-002"
		retry.Now = request.ExpiresAt.Add(time.Minute)
		retry.ExpiresAt = retry.Now.Add(time.Hour)
		_, err := NewInventoryRepository(harness.database).Reserve(context.Background(), retry)
		if !errors.Is(err, ports.ErrReservationExpired) {
			t.Fatalf("lazy retry error = %v, want ErrReservationExpired", err)
		}
		assertStock(t, harness.database, "product-001", 10, 3)
		assertReservationStatus(t, harness.database, "reservation-001", domain.ReservationStatusExpired, 2)
	})

	t.Run("unique order index exists and is enforced", func(t *testing.T) {
		harness.reset(t)
		if err := NewInventoryRepository(harness.database).EnsureIndexes(context.Background()); err != nil {
			t.Fatalf("second EnsureIndexes() error = %v", err)
		}
		cursor, err := harness.database.Collection(reservationsCollection).Indexes().List(context.Background())
		if err != nil {
			t.Fatalf("list indexes: %v", err)
		}
		defer cursor.Close(context.Background())
		foundOrderIndex := false
		foundExpirationIndex := false
		for cursor.Next(context.Background()) {
			var index struct {
				Name   string `bson:"name"`
				Unique bool   `bson:"unique"`
			}
			if err := cursor.Decode(&index); err != nil {
				t.Fatalf("decode index: %v", err)
			}
			if index.Name == reservationOrderIndex && index.Unique {
				foundOrderIndex = true
			}
			if index.Name == reservationExpirationIndex {
				foundExpirationIndex = true
			}
		}
		if !foundOrderIndex {
			t.Fatalf("unique index %q not found", reservationOrderIndex)
		}
		if !foundExpirationIndex {
			t.Fatalf("expiration index %q not found", reservationExpirationIndex)
		}
		document := newReservationDocument(
			"reservation-001", "order-001", reservationItems(t, map[string]int{"product-001": 1}),
			harness.now.Add(time.Hour), harness.now,
		)
		if _, err := harness.database.Collection(reservationsCollection).InsertOne(context.Background(), document); err != nil {
			t.Fatalf("insert first reservation: %v", err)
		}
		document.ID = "reservation-002"
		if _, err := harness.database.Collection(reservationsCollection).InsertOne(context.Background(), document); !mongo.IsDuplicateKeyError(err) {
			t.Fatalf("duplicate order insert error = %v, want duplicate key", err)
		}
	})

	t.Run("context cancellation and deadlines are preserved", func(t *testing.T) {
		harness.reset(t)
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		if err := harness.repository.SeedStocks(canceled, []domain.Stock{mustStock(t, "product-001", 1)}); !errors.Is(err, context.Canceled) {
			t.Fatalf("SeedStocks() error = %v, want context.Canceled", err)
		}
		_, err := harness.repository.Reserve(canceled, reserveRequest(
			t, "reservation-001", "order-001", harness.now, map[string]int{"product-001": 1},
		))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Reserve() error = %v, want context.Canceled", err)
		}
		deadline, deadlineCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer deadlineCancel()
		if _, err := harness.repository.ExpireReservations(deadline, harness.now); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("ExpireReservations() error = %v, want context.DeadlineExceeded", err)
		}
	})

	t.Run("corrupt persisted reservation is rejected", func(t *testing.T) {
		harness.reset(t)
		harness.seed(t, map[string]int{"product-001": 10})
		document := newReservationDocument(
			"reservation-001", "order-001", reservationItems(t, map[string]int{"product-001": 1}),
			harness.now.Add(time.Hour), harness.now,
		)
		document.Version = 0
		if _, err := harness.database.Collection(reservationsCollection).InsertOne(context.Background(), document); err != nil {
			t.Fatalf("insert corrupt reservation: %v", err)
		}
		_, err := harness.repository.Reserve(context.Background(), reserveRequest(
			t, "reservation-002", "order-001", harness.now, map[string]int{"product-001": 1},
		))
		if err == nil || errors.Is(err, ports.ErrReservationConflict) || errors.Is(err, ports.ErrReservationExpired) {
			t.Fatalf("Reserve() error = %v, want corruption error", err)
		}
		assertStock(t, harness.database, "product-001", 10, 1)
	})
}

type integrationHarness struct {
	database   *mongo.Database
	repository *InventoryRepository
	now        time.Time
}

func newIntegrationHarness(t *testing.T) *integrationHarness {
	t.Helper()
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
	if err := client.Ping(ctx, nil); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	database := client.Database(fmt.Sprintf("inventory_repository_integration_%d", time.Now().UnixNano()))
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := database.Drop(cleanupContext); err != nil {
			t.Errorf("Drop() error = %v", err)
		}
		if err := client.Disconnect(cleanupContext); err != nil {
			t.Errorf("Disconnect() error = %v", err)
		}
	})
	harness := &integrationHarness{
		database: database,
		now:      time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC),
	}
	harness.reset(t)
	return harness
}

func (harness *integrationHarness) reset(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := harness.database.Drop(ctx); err != nil {
		t.Fatalf("reset Drop() error = %v", err)
	}
	harness.repository = NewInventoryRepository(harness.database)
	if err := harness.repository.EnsureIndexes(ctx); err != nil {
		t.Fatalf("EnsureIndexes() error = %v", err)
	}
}

func (harness *integrationHarness) seed(t *testing.T, values map[string]int) {
	t.Helper()
	stocks := make([]domain.Stock, 0, len(values))
	for productID, available := range values {
		stocks = append(stocks, mustStock(t, productID, available))
	}
	if err := harness.repository.SeedStocks(context.Background(), stocks); err != nil {
		t.Fatalf("SeedStocks() error = %v", err)
	}
}

func reserveRequest(
	t *testing.T,
	reservationID string,
	orderID string,
	now time.Time,
	values map[string]int,
) ports.ReserveRequest {
	t.Helper()
	return ports.ReserveRequest{
		ReservationID: reservationID,
		OrderID:       orderID,
		Items:         reservationItems(t, values),
		ExpiresAt:     now.Add(time.Hour),
		Now:           now,
	}
}

func reservationItems(t *testing.T, values map[string]int) []domain.ReservationItem {
	t.Helper()
	items := make([]domain.ReservationItem, 0, len(values))
	for productID, quantity := range values {
		item, err := domain.NewReservationItem(productID, quantity)
		if err != nil {
			t.Fatalf("NewReservationItem(%q) error = %v", productID, err)
		}
		items = append(items, item)
	}
	return items
}

func mustStock(t *testing.T, productID string, available int) domain.Stock {
	t.Helper()
	stock, err := domain.NewStock(productID, available)
	if err != nil {
		t.Fatalf("NewStock(%q) error = %v", productID, err)
	}
	return stock
}

func assertStock(t *testing.T, database *mongo.Database, productID string, available int, version int64) {
	t.Helper()
	var document stockDocument
	if err := database.Collection(stocksCollection).FindOne(
		context.Background(),
		bson.D{{Key: "_id", Value: productID}},
	).Decode(&document); err != nil {
		t.Fatalf("load stock %q: %v", productID, err)
	}
	if document.Available != available || document.Version != version {
		t.Fatalf("stock %q = available %d version %d, want %d/%d", productID, document.Available, document.Version, available, version)
	}
	if document.Available < 0 {
		t.Fatalf("stock %q became negative: %d", productID, document.Available)
	}
}

func assertReservationCount(t *testing.T, database *mongo.Database, filter bson.D, want int64) {
	t.Helper()
	count, err := database.Collection(reservationsCollection).CountDocuments(context.Background(), filter)
	if err != nil {
		t.Fatalf("CountDocuments() error = %v", err)
	}
	if count != want {
		t.Fatalf("reservation count = %d, want %d", count, want)
	}
}

func assertReservationStatus(
	t *testing.T,
	database *mongo.Database,
	reservationID string,
	status domain.ReservationStatus,
	version int64,
) {
	t.Helper()
	var document reservationDocument
	if err := database.Collection(reservationsCollection).FindOne(
		context.Background(),
		bson.D{{Key: "_id", Value: reservationID}},
	).Decode(&document); err != nil {
		t.Fatalf("load reservation %q: %v", reservationID, err)
	}
	if document.Status != string(status) || document.Version != version {
		t.Fatalf("reservation %q = status %q version %d, want %q/%d", reservationID, document.Status, document.Version, status, version)
	}
}
