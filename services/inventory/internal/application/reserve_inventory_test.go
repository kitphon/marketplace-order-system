package application_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kitphon/marketplace-order-system/services/inventory/internal/adapters/memory"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/application"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/domain"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/ports"
)

func TestReserveInventorySuccess(t *testing.T) {
	harness := newHarness(t, map[string]int{"product-001": 10})
	reservation, err := harness.reserve.Execute(context.Background(), harness.command(
		"order-001",
		[]application.ReserveInventoryItem{{ProductID: "product-001", Quantity: 2}},
	))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if reservation.Status() != domain.ReservationStatusReserved || harness.repository.Available("product-001") != 8 {
		t.Fatalf("reservation=%+v available=%d", reservation, harness.repository.Available("product-001"))
	}
}

func TestSameOrderAndCanonicalPayloadReturnsExistingReservation(t *testing.T) {
	harness := newHarness(t, map[string]int{"product-001": 10, "product-002": 10})
	first, err := harness.reserve.Execute(context.Background(), harness.command(
		"order-001",
		[]application.ReserveInventoryItem{
			{ProductID: "product-001", Quantity: 2},
			{ProductID: "product-002", Quantity: 3},
		},
	))
	if err != nil {
		t.Fatalf("first Execute() error = %v", err)
	}
	secondCommand := harness.command("order-001", []application.ReserveInventoryItem{
		{ProductID: "product-002", Quantity: 3},
		{ProductID: "product-001", Quantity: 2},
	})
	secondCommand.ExpiresAt = secondCommand.ExpiresAt.Add(time.Hour)
	second, err := harness.reserve.Execute(context.Background(), secondCommand)
	if err != nil {
		t.Fatalf("second Execute() error = %v", err)
	}
	if second.ID() != first.ID() || !second.ExpiresAt().Equal(first.ExpiresAt()) {
		t.Fatalf("second reservation changed identity or expiry: first=%+v second=%+v", first, second)
	}
}

func TestDuplicateRetryDoesNotDecrementStockTwice(t *testing.T) {
	harness := newHarness(t, map[string]int{"product-001": 10})
	command := harness.command("order-001", []application.ReserveInventoryItem{{ProductID: "product-001", Quantity: 2}})
	if _, err := harness.reserve.Execute(context.Background(), command); err != nil {
		t.Fatalf("first Execute() error = %v", err)
	}
	if _, err := harness.reserve.Execute(context.Background(), command); err != nil {
		t.Fatalf("second Execute() error = %v", err)
	}
	if got := harness.repository.Available("product-001"); got != 8 {
		t.Fatalf("available = %d, want 8", got)
	}
}

func TestSameOrderDifferentPayloadReturnsConflict(t *testing.T) {
	harness := newHarness(t, map[string]int{"product-001": 10})
	if _, err := harness.reserve.Execute(context.Background(), harness.command(
		"order-001",
		[]application.ReserveInventoryItem{{ProductID: "product-001", Quantity: 2}},
	)); err != nil {
		t.Fatalf("first Execute() error = %v", err)
	}
	_, err := harness.reserve.Execute(context.Background(), harness.command(
		"order-001",
		[]application.ReserveInventoryItem{{ProductID: "product-001", Quantity: 3}},
	))
	if !errors.Is(err, ports.ErrReservationConflict) {
		t.Fatalf("Execute() error = %v, want ErrReservationConflict", err)
	}
	if got := harness.repository.Available("product-001"); got != 8 {
		t.Fatalf("available = %d, want 8", got)
	}
}

func TestInsufficientStockChangesNoStock(t *testing.T) {
	harness := newHarness(t, map[string]int{"product-001": 1})
	_, err := harness.reserve.Execute(context.Background(), harness.command(
		"order-001",
		[]application.ReserveInventoryItem{{ProductID: "product-001", Quantity: 2}},
	))
	if !errors.Is(err, ports.ErrInsufficientStock) {
		t.Fatalf("Execute() error = %v, want ErrInsufficientStock", err)
	}
	if got := harness.repository.Available("product-001"); got != 1 {
		t.Fatalf("available = %d, want 1", got)
	}
}

func TestReserveInventoryRejectsInvalidRequests(t *testing.T) {
	harness := newHarness(t, map[string]int{"product-001": 10})
	validItem := application.ReserveInventoryItem{ProductID: "product-001", Quantity: 1}
	tests := []struct {
		name    string
		command application.ReserveInventoryCommand
	}{
		{name: "blank order id", command: harness.command("  ", []application.ReserveInventoryItem{validItem})},
		{name: "empty items", command: harness.command("order-001", nil)},
		{name: "blank product id", command: harness.command("order-001", []application.ReserveInventoryItem{{ProductID: "  ", Quantity: 1}})},
		{name: "non-positive quantity", command: harness.command("order-001", []application.ReserveInventoryItem{{ProductID: "product-001", Quantity: 0}})},
		{name: "duplicate product", command: harness.command("order-001", []application.ReserveInventoryItem{validItem, validItem})},
		{name: "expiry is now", command: application.ReserveInventoryCommand{OrderID: "order-001", Items: []application.ReserveInventoryItem{validItem}, ExpiresAt: harness.clock.Now()}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := harness.reserve.Execute(context.Background(), test.command)
			if !errors.Is(err, application.ErrInvalidReserveInventoryRequest) {
				t.Fatalf("Execute() error = %v, want ErrInvalidReserveInventoryRequest", err)
			}
			if got := harness.repository.Available("product-001"); got != 10 {
				t.Fatalf("available = %d, want 10", got)
			}
		})
	}
}

func TestMultiItemReservationIsAllOrNothing(t *testing.T) {
	harness := newHarness(t, map[string]int{"product-001": 10, "product-002": 1})
	_, err := harness.reserve.Execute(context.Background(), harness.command(
		"order-001",
		[]application.ReserveInventoryItem{
			{ProductID: "product-001", Quantity: 5},
			{ProductID: "product-002", Quantity: 2},
		},
	))
	if !errors.Is(err, ports.ErrInsufficientStock) {
		t.Fatalf("Execute() error = %v, want ErrInsufficientStock", err)
	}
	if harness.repository.Available("product-001") != 10 || harness.repository.Available("product-002") != 1 {
		t.Fatal("stock changed after failed multi-item reservation")
	}
}

func TestExpiredReservationRestoresStockExactlyOnce(t *testing.T) {
	harness := newHarness(t, map[string]int{"product-001": 10})
	if _, err := harness.reserve.Execute(context.Background(), harness.command(
		"order-001",
		[]application.ReserveInventoryItem{{ProductID: "product-001", Quantity: 2}},
	)); err != nil {
		t.Fatalf("Reserve Execute() error = %v", err)
	}
	harness.clock.Set(harness.clock.Now().Add(2 * time.Hour))
	first, err := harness.expire.Execute(context.Background())
	if err != nil {
		t.Fatalf("first Expire Execute() error = %v", err)
	}
	second, err := harness.expire.Execute(context.Background())
	if err != nil {
		t.Fatalf("second Expire Execute() error = %v", err)
	}
	if first != 1 || second != 0 || harness.repository.Available("product-001") != 10 {
		t.Fatalf("expired counts=(%d,%d), available=%d", first, second, harness.repository.Available("product-001"))
	}
}

func TestRetryExpiredReservationReturnsExpired(t *testing.T) {
	harness := newHarness(t, map[string]int{"product-001": 10})
	command := harness.command("order-001", []application.ReserveInventoryItem{{ProductID: "product-001", Quantity: 2}})
	if _, err := harness.reserve.Execute(context.Background(), command); err != nil {
		t.Fatalf("first Execute() error = %v", err)
	}
	harness.clock.Set(harness.clock.Now().Add(2 * time.Hour))
	retry := harness.command("order-001", command.Items)
	_, err := harness.reserve.Execute(context.Background(), retry)
	if !errors.Is(err, ports.ErrReservationExpired) {
		t.Fatalf("retry Execute() error = %v, want ErrReservationExpired", err)
	}
	if got := harness.repository.Available("product-001"); got != 10 {
		t.Fatalf("available = %d, want 10", got)
	}
}

func TestConcurrentSameOrderCreatesOneReservationAndOneStockDecrement(t *testing.T) {
	harness := newHarness(t, map[string]int{"product-001": 10})
	command := harness.command("order-001", []application.ReserveInventoryItem{{ProductID: "product-001", Quantity: 2}})
	const workers = 20
	start := make(chan struct{})
	ids := make(chan string, workers)
	errs := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			reservation, err := harness.reserve.Execute(context.Background(), command)
			if err != nil {
				errs <- err
				return
			}
			ids <- reservation.ID()
		}()
	}
	close(start)
	wait.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Execute() error = %v", err)
	}
	var winner string
	for id := range ids {
		if winner == "" {
			winner = id
		}
		if id != winner {
			t.Fatalf("reservation ID = %q, want %q", id, winner)
		}
	}
	if got := harness.repository.Available("product-001"); got != 8 {
		t.Fatalf("available = %d, want 8", got)
	}
}

func TestConcurrentExpirationPassesRestoreStockOnce(t *testing.T) {
	harness := newHarness(t, map[string]int{"product-001": 10})
	if _, err := harness.reserve.Execute(context.Background(), harness.command(
		"order-001",
		[]application.ReserveInventoryItem{{ProductID: "product-001", Quantity: 2}},
	)); err != nil {
		t.Fatalf("Reserve Execute() error = %v", err)
	}
	harness.clock.Set(harness.clock.Now().Add(2 * time.Hour))
	const workers = 20
	start := make(chan struct{})
	counts := make(chan int, workers)
	errs := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			count, err := harness.expire.Execute(context.Background())
			if err != nil {
				errs <- err
				return
			}
			counts <- count
		}()
	}
	close(start)
	wait.Wait()
	close(counts)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Expire() error = %v", err)
	}
	total := 0
	for count := range counts {
		total += count
	}
	if total != 1 || harness.repository.Available("product-001") != 10 {
		t.Fatalf("expired total=%d available=%d", total, harness.repository.Available("product-001"))
	}
}

type harness struct {
	clock      *testClock
	repository *memory.InventoryRepository
	reserve    *application.ReserveInventory
	expire     *application.ExpireReservations
}

func newHarness(t *testing.T, quantities map[string]int) harness {
	t.Helper()
	stock := make([]domain.Stock, 0, len(quantities))
	for productID, available := range quantities {
		item, err := domain.NewStock(productID, available)
		if err != nil {
			t.Fatalf("NewStock() error = %v", err)
		}
		stock = append(stock, item)
	}
	repository := memory.NewInventoryRepository(stock)
	clock := &testClock{now: time.Date(2026, time.October, 6, 9, 0, 0, 0, time.UTC)}
	return harness{
		clock:      clock,
		repository: repository,
		reserve:    application.NewReserveInventory(repository, clock, &sequenceIDs{}),
		expire:     application.NewExpireReservations(repository, clock),
	}
}

func (harness harness) command(orderID string, items []application.ReserveInventoryItem) application.ReserveInventoryCommand {
	return application.ReserveInventoryCommand{
		OrderID:   orderID,
		Items:     items,
		ExpiresAt: harness.clock.Now().Add(time.Hour),
	}
}

type testClock struct {
	mu  sync.RWMutex
	now time.Time
}

func (clock *testClock) Now() time.Time {
	clock.mu.RLock()
	defer clock.mu.RUnlock()
	return clock.now
}

func (clock *testClock) Set(now time.Time) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = now
}

type sequenceIDs struct{ next atomic.Int64 }

func (ids *sequenceIDs) NewID() (string, error) {
	return fmt.Sprintf("reservation-%03d", ids.next.Add(1)), nil
}
