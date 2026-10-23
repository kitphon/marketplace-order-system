package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kitphon/marketplace-order-system/services/inventory/internal/application"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/domain"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/ports"
)

func TestGetReservationByOrderIDNormalizesAndReturnsReservation(t *testing.T) {
	reservation := mustReservation(t)
	repository := &lookupRepository{reservation: reservation}
	got, err := application.NewGetReservationByOrderID(repository).Execute(context.Background(), "  order-001 \t")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if repository.orderID != "order-001" || got.ID() != reservation.ID() {
		t.Fatalf("lookup order = %q reservation = %+v", repository.orderID, got)
	}
}

func TestGetReservationByOrderIDErrors(t *testing.T) {
	tests := []struct {
		name string
		id   string
		err  error
		want error
	}{
		{name: "blank", id: "  ", want: application.ErrInvalidGetReservationRequest},
		{name: "not found", id: "missing", err: ports.ErrReservationNotFound, want: ports.ErrReservationNotFound},
		{name: "canceled", id: "order-001", err: context.Canceled, want: context.Canceled},
		{name: "deadline", id: "order-001", err: context.DeadlineExceeded, want: context.DeadlineExceeded},
		{name: "storage", id: "order-001", err: errors.New("storage failed")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := application.NewGetReservationByOrderID(&lookupRepository{err: test.err}).Execute(context.Background(), test.id)
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("Execute() error = %v, want %v", err, test.want)
			}
			if test.want == nil && (err == nil || err.Error() == test.err.Error()) {
				t.Fatalf("Execute() error = %v, want contextual wrapper", err)
			}
		})
	}
}

type lookupRepository struct {
	reservation *domain.Reservation
	err         error
	orderID     string
}

func (repository *lookupRepository) Reserve(context.Context, ports.ReserveRequest) (*domain.Reservation, error) {
	panic("unexpected Reserve call")
}

func (repository *lookupRepository) FindReservationByOrderID(_ context.Context, orderID string) (*domain.Reservation, error) {
	repository.orderID = orderID
	return repository.reservation, repository.err
}

func (repository *lookupRepository) ExpireReservations(context.Context, time.Time) (int, error) {
	panic("unexpected ExpireReservations call")
}

func mustReservation(t *testing.T) *domain.Reservation {
	t.Helper()
	item, err := domain.NewReservationItem("product-001", 2)
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := domain.NewReservation("reservation-001", "order-001", []domain.ReservationItem{item}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return reservation
}
