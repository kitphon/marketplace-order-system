package application_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/marketplace-order-system/services/order/internal/adapters/memory"
	"github.com/example/marketplace-order-system/services/order/internal/application"
	"github.com/example/marketplace-order-system/services/order/internal/domain"
	"github.com/example/marketplace-order-system/services/order/internal/ports"
)

func TestCreateOrderSuccess(t *testing.T) {
	harness := newHarness()

	order, err := harness.useCase.Execute(context.Background(), defaultCommand())
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := order.Status(), domain.OrderStatusInventoryReserved; got != want {
		t.Fatalf("Status() = %q, want %q", got, want)
	}
	if got, want := order.TotalAmount().MinorUnits(), int64(400_000); got != want {
		t.Fatalf("TotalAmount() = %d, want %d", got, want)
	}
	if got, want := harness.inventory.calls.Load(), int64(1); got != want {
		t.Fatalf("Reserve() calls = %d, want %d", got, want)
	}
}

func TestSameIdempotencyKeyAndPayloadReturnsExistingOrder(t *testing.T) {
	harness := newHarness()
	command := defaultCommand()

	first, err := harness.useCase.Execute(context.Background(), command)
	if err != nil {
		t.Fatalf("first Execute() error = %v", err)
	}
	second, err := harness.useCase.Execute(context.Background(), command)
	if err != nil {
		t.Fatalf("second Execute() error = %v", err)
	}

	if first.ID() != second.ID() {
		t.Fatalf("duplicate request created different orders: %q and %q", first.ID(), second.ID())
	}
	if got, want := harness.inventory.calls.Load(), int64(1); got != want {
		t.Fatalf("Reserve() calls = %d, want %d", got, want)
	}
}

func TestSameIdempotencyKeyWithDifferentPayloadIsRejected(t *testing.T) {
	harness := newHarness()
	first := defaultCommand()
	if _, err := harness.useCase.Execute(context.Background(), first); err != nil {
		t.Fatalf("first Execute() error = %v", err)
	}

	second := defaultCommand()
	second.Items[0].Quantity = 3
	_, err := harness.useCase.Execute(context.Background(), second)
	if !errors.Is(err, application.ErrIdempotencyKeyReused) {
		t.Fatalf("second Execute() error = %v, want ErrIdempotencyKeyReused", err)
	}
}

func TestInsufficientStockCancelsOrder(t *testing.T) {
	harness := newHarness()
	harness.inventory.err = ports.ErrInsufficientStock

	order, err := harness.useCase.Execute(context.Background(), defaultCommand())
	if !errors.Is(err, ports.ErrInsufficientStock) {
		t.Fatalf("Execute() error = %v, want ErrInsufficientStock", err)
	}
	if got, want := order.Status(), domain.OrderStatusCancelled; got != want {
		t.Fatalf("Status() = %q, want %q", got, want)
	}
	if got, want := order.CancelReason(), "INSUFFICIENT_STOCK"; got != want {
		t.Fatalf("CancelReason() = %q, want %q", got, want)
	}
}

func TestConcurrentDuplicateRequestsCreateOneOrder(t *testing.T) {
	harness := newHarness()
	command := defaultCommand()

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
			order, err := harness.useCase.Execute(context.Background(), command)
			if err != nil {
				errs <- err
				return
			}
			ids <- order.ID()
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
			t.Fatalf("created more than one order: winner=%q, got=%q", winner, id)
		}
	}
	if got, want := harness.inventory.calls.Load(), int64(1); got != want {
		t.Fatalf("Reserve() calls = %d, want %d", got, want)
	}
}

type harness struct {
	useCase   *application.CreateOrder
	inventory *fakeInventoryClient
}

func newHarness() harness {
	repository := memory.NewOrderRepository()
	inventory := &fakeInventoryClient{}
	products := fakeProductClient{
		products: map[string]ports.ProductSnapshot{
			"product-001": {
				ProductID: "product-001",
				SellerID:  "seller-001",
				Name:      "Mechanical Keyboard",
				UnitPrice: domain.Money(200_000),
				Active:    true,
			},
		},
	}
	clock := fixedClock{now: time.Date(2026, time.October, 3, 9, 0, 0, 0, time.UTC)}
	ids := &sequenceIDGenerator{}

	return harness{
		useCase:   application.NewCreateOrder(repository, products, inventory, ids, clock),
		inventory: inventory,
	}
}

func defaultCommand() application.CreateOrderCommand {
	return application.CreateOrderCommand{
		CustomerID:     "customer-001",
		IdempotencyKey: "checkout-001",
		Items: []application.CreateOrderItem{
			{ProductID: "product-001", Quantity: 2},
		},
	}
}

type fakeProductClient struct {
	products map[string]ports.ProductSnapshot
}

func (f fakeProductClient) GetByIDs(
	_ context.Context,
	productIDs []string,
) ([]ports.ProductSnapshot, error) {
	result := make([]ports.ProductSnapshot, 0, len(productIDs))
	for _, id := range productIDs {
		if product, exists := f.products[id]; exists {
			result = append(result, product)
		}
	}
	return result, nil
}

type fakeInventoryClient struct {
	calls atomic.Int64
	err   error
}

func (f *fakeInventoryClient) Reserve(
	_ context.Context,
	_ ports.ReserveInventoryRequest,
) error {
	f.calls.Add(1)
	return f.err
}

type fixedClock struct {
	now time.Time
}

func (f fixedClock) Now() time.Time {
	return f.now
}

type sequenceIDGenerator struct {
	next atomic.Int64
}

func (g *sequenceIDGenerator) NewID() (string, error) {
	return fmt.Sprintf("order-%03d", g.next.Add(1)), nil
}
