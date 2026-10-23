package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kitphon/marketplace-order-system/services/inventory/internal/domain"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/ports"
)

func TestFindReservationByOrderID(t *testing.T) {
	now := time.Date(2026, time.October, 23, 9, 0, 0, 0, time.UTC)
	stock, _ := domain.NewStock("product-001", 10)
	item, _ := domain.NewReservationItem("product-001", 2)
	repository := NewInventoryRepository([]domain.Stock{stock})
	reserved, err := repository.Reserve(context.Background(), ports.ReserveRequest{
		ReservationID: "reservation-001",
		OrderID:       "order-001",
		Items:         []domain.ReservationItem{item},
		ExpiresAt:     now.Add(time.Hour),
		Now:           now,
	})
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}

	first, err := repository.FindReservationByOrderID(context.Background(), "order-001")
	if err != nil {
		t.Fatalf("FindReservationByOrderID() error = %v", err)
	}
	if first.ID() != reserved.ID() || first.Status() != domain.ReservationStatusReserved {
		t.Fatalf("reservation = %+v, want RESERVED %q", first, reserved.ID())
	}
	items := first.Items()
	items[0] = domain.ReservationItem{}
	first.Expire()
	second, err := repository.FindReservationByOrderID(context.Background(), "order-001")
	if err != nil {
		t.Fatalf("second FindReservationByOrderID() error = %v", err)
	}
	if second.Items()[0].ProductID() != "product-001" || second.Status() != domain.ReservationStatusReserved {
		t.Fatal("lookup exposed its internal item slice")
	}

	if count, err := repository.ExpireReservations(context.Background(), now.Add(time.Hour)); err != nil || count != 1 {
		t.Fatalf("ExpireReservations() = (%d, %v), want (1, nil)", count, err)
	}
	expired, err := repository.FindReservationByOrderID(context.Background(), "order-001")
	if err != nil {
		t.Fatalf("expired FindReservationByOrderID() error = %v", err)
	}
	if expired.Status() != domain.ReservationStatusExpired {
		t.Fatalf("status = %v, want EXPIRED", expired.Status())
	}
	if repository.Available("product-001") != 10 {
		t.Fatalf("available = %d, want 10", repository.Available("product-001"))
	}
}

func TestFindReservationByOrderIDMissingAndContext(t *testing.T) {
	repository := NewInventoryRepository(nil)
	if _, err := repository.FindReservationByOrderID(context.Background(), "missing"); !errors.Is(err, ports.ErrReservationNotFound) {
		t.Fatalf("missing error = %v, want ErrReservationNotFound", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := repository.FindReservationByOrderID(ctx, "order-001"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v, want context.Canceled", err)
	}
}
