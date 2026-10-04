package domain

import (
	"errors"
	"testing"
	"time"
)

func TestNewOrderCalculatesTotalAndCreatesSnapshot(t *testing.T) {
	now := time.Date(2026, time.October, 3, 9, 0, 0, 0, time.UTC)
	keyboard := mustOrderItem(t, "product-001", "seller-001", "Keyboard", 200_000, 2)
	mouse := mustOrderItem(t, "product-002", "seller-001", "Mouse", 90_000, 1)

	order, err := NewOrder(
		"order-001",
		"customer-001",
		"checkout-001",
		"sha256:request-001",
		[]OrderItem{keyboard, mouse},
		CurrencyTHB,
		now,
	)
	if err != nil {
		t.Fatalf("NewOrder() error = %v", err)
	}

	if got, want := order.TotalAmount().MinorUnits(), int64(490_000); got != want {
		t.Fatalf("TotalAmount() = %d, want %d", got, want)
	}
	if got, want := order.Status(), OrderStatusPending; got != want {
		t.Fatalf("Status() = %q, want %q", got, want)
	}
	if got, want := order.Version(), int64(1); got != want {
		t.Fatalf("Version() = %d, want %d", got, want)
	}

	items := order.Items()
	items[0] = mouse
	if got, want := order.Items()[0].ProductID(), "product-001"; got != want {
		t.Fatalf("Items() exposed aggregate state: got %q, want %q", got, want)
	}
}

func TestOrderHappyPath(t *testing.T) {
	now := time.Date(2026, time.October, 3, 9, 0, 0, 0, time.UTC)
	order := mustOrder(t, now)

	steps := []struct {
		name       string
		transition func(time.Time) error
		wantStatus OrderStatus
		wantVersion int64
	}{
		{"reserve inventory", order.MarkInventoryReserved, OrderStatusInventoryReserved, 2},
		{"start payment", order.StartPayment, OrderStatusPaymentProcessing, 3},
		{"confirm", order.Confirm, OrderStatusConfirmed, 4},
	}

	for i, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			at := now.Add(time.Duration(i+1) * time.Minute)
			if err := step.transition(at); err != nil {
				t.Fatalf("transition error = %v", err)
			}
			if got := order.Status(); got != step.wantStatus {
				t.Fatalf("Status() = %q, want %q", got, step.wantStatus)
			}
			if got := order.Version(); got != step.wantVersion {
				t.Fatalf("Version() = %d, want %d", got, step.wantVersion)
			}
		})
	}
}

func TestConfirmIsIdempotent(t *testing.T) {
	now := time.Date(2026, time.October, 3, 9, 0, 0, 0, time.UTC)
	order := mustOrder(t, now)
	mustTransition(t, order.MarkInventoryReserved, now.Add(time.Minute))
	mustTransition(t, order.StartPayment, now.Add(2*time.Minute))
	mustTransition(t, order.Confirm, now.Add(3*time.Minute))

	version := order.Version()
	updatedAt := order.UpdatedAt()
	if err := order.Confirm(now.Add(4 * time.Minute)); err != nil {
		t.Fatalf("second Confirm() error = %v", err)
	}
	if order.Version() != version {
		t.Fatalf("second Confirm() changed version: got %d, want %d", order.Version(), version)
	}
	if !order.UpdatedAt().Equal(updatedAt) {
		t.Fatal("second Confirm() changed updated_at")
	}
}

func TestCannotConfirmPendingOrder(t *testing.T) {
	order := mustOrder(t, time.Now())
	err := order.Confirm(time.Now())
	if !errors.Is(err, ErrInvalidStateTransition) {
		t.Fatalf("Confirm() error = %v, want ErrInvalidStateTransition", err)
	}
}

func TestCannotCancelConfirmedOrder(t *testing.T) {
	now := time.Now()
	order := mustOrder(t, now)
	mustTransition(t, order.MarkInventoryReserved, now.Add(time.Minute))
	mustTransition(t, order.StartPayment, now.Add(2*time.Minute))
	mustTransition(t, order.Confirm, now.Add(3*time.Minute))

	err := order.Cancel("CUSTOMER_REQUESTED", now.Add(4*time.Minute))
	if !errors.Is(err, ErrInvalidStateTransition) {
		t.Fatalf("Cancel() error = %v, want ErrInvalidStateTransition", err)
	}
}

func TestNewOrderRejectsDuplicateProduct(t *testing.T) {
	item := mustOrderItem(t, "product-001", "seller-001", "Keyboard", 200_000, 1)
	_, err := NewOrder(
		"order-001",
		"customer-001",
		"checkout-001",
		"sha256:request-001",
		[]OrderItem{item, item},
		CurrencyTHB,
		time.Now(),
	)
	if !errors.Is(err, ErrDuplicateProduct) {
		t.Fatalf("NewOrder() error = %v, want ErrDuplicateProduct", err)
	}
}

func TestNewOrderItemRejectsInvalidQuantity(t *testing.T) {
	price, err := NewMoney(200_000)
	if err != nil {
		t.Fatalf("NewMoney() error = %v", err)
	}
	_, err = NewOrderItem("product-001", "seller-001", "Keyboard", price, 0)
	if !errors.Is(err, ErrInvalidQuantity) {
		t.Fatalf("NewOrderItem() error = %v, want ErrInvalidQuantity", err)
	}
}

func mustOrder(t *testing.T, now time.Time) *Order {
	t.Helper()
	item := mustOrderItem(t, "product-001", "seller-001", "Keyboard", 200_000, 1)
	order, err := NewOrder(
		"order-001",
		"customer-001",
		"checkout-001",
		"sha256:request-001",
		[]OrderItem{item},
		CurrencyTHB,
		now,
	)
	if err != nil {
		t.Fatalf("NewOrder() error = %v", err)
	}
	return order
}

func mustOrderItem(
	t *testing.T,
	productID string,
	sellerID string,
	name string,
	minorUnits int64,
	quantity int,
) OrderItem {
	t.Helper()
	price, err := NewMoney(minorUnits)
	if err != nil {
		t.Fatalf("NewMoney() error = %v", err)
	}
	item, err := NewOrderItem(productID, sellerID, name, price, quantity)
	if err != nil {
		t.Fatalf("NewOrderItem() error = %v", err)
	}
	return item
}

func mustTransition(t *testing.T, transition func(time.Time) error, at time.Time) {
	t.Helper()
	if err := transition(at); err != nil {
		t.Fatalf("transition error = %v", err)
	}
}

