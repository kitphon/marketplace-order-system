package grpcserver

import (
	"math"
	"strconv"
	"testing"
	"time"

	inventoryv1 "github.com/kitphon/marketplace-order-system/contracts/inventory/v1"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestReservationStatusRejectsUnknownState(t *testing.T) {
	value, err := reservationStatus(domain.ReservationStatus("LOST"))
	if status.Code(err) != codes.Internal {
		t.Fatalf("error code = %v, want Internal", status.Code(err))
	}
	if value != 0 {
		t.Fatalf("value = %v, expected zero only alongside an error", value)
	}
}

func TestReservationResponseRejectsNilReservation(t *testing.T) {
	response, err := reservationResponse(nil)
	if response != nil || status.Code(err) != codes.Internal {
		t.Fatalf("reservationResponse(nil) = (%+v, %v), want (nil, Internal)", response, err)
	}
}

func TestReservationResponseRejectsQuantityOutsideProtoRange(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("int cannot represent a quantity greater than int32 on this platform")
	}
	item, err := domain.NewReservationItem("product-001", int(math.MaxInt32)+1)
	if err != nil {
		t.Fatalf("NewReservationItem() error = %v", err)
	}
	reservation := newTestReservation(t, []domain.ReservationItem{item}, time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC))
	response, err := reservationResponse(reservation)
	if response != nil || status.Code(err) != codes.Internal {
		t.Fatalf("reservationResponse() = (%+v, %v), want (nil, Internal)", response, err)
	}
}

func TestReservationResponseRejectsInvalidProtoTimestamp(t *testing.T) {
	item, err := domain.NewReservationItem("product-001", 1)
	if err != nil {
		t.Fatalf("NewReservationItem() error = %v", err)
	}
	reservation := newTestReservation(t, []domain.ReservationItem{item}, time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC))
	response, err := reservationResponse(reservation)
	if response != nil || status.Code(err) != codes.Internal {
		t.Fatalf("reservationResponse() = (%+v, %v), want (nil, Internal)", response, err)
	}
}

func TestReservationResponseRejectsInvalidItems(t *testing.T) {
	valid, err := domain.NewReservationItem("product-001", 1)
	if err != nil {
		t.Fatalf("NewReservationItem() error = %v", err)
	}
	blank, err := domain.NewReservationItem(" ", 1)
	if err != nil {
		t.Fatalf("NewReservationItem() error = %v", err)
	}
	for _, test := range []struct {
		name  string
		items []domain.ReservationItem
	}{
		{name: "blank product", items: []domain.ReservationItem{blank}},
		{name: "non-positive quantity", items: []domain.ReservationItem{{}}},
		{name: "duplicate product", items: []domain.ReservationItem{valid, valid}},
	} {
		t.Run(test.name, func(t *testing.T) {
			reservation := newTestReservation(t, test.items, time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC))
			response, err := reservationResponse(reservation)
			if response != nil || status.Code(err) != codes.Internal {
				t.Fatalf("reservationResponse() = (%+v, %v), want (nil, Internal)", response, err)
			}
		})
	}
}

func TestReservationResponseMapsValidStatusesWithoutUnspecified(t *testing.T) {
	item, err := domain.NewReservationItem("product-001", 2)
	if err != nil {
		t.Fatalf("NewReservationItem() error = %v", err)
	}
	for _, test := range []struct {
		name       string
		expire     bool
		wantStatus inventoryv1.ReservationStatus
	}{
		{name: "reserved", wantStatus: inventoryv1.ReservationStatus_RESERVATION_STATUS_RESERVED},
		{name: "expired", expire: true, wantStatus: inventoryv1.ReservationStatus_RESERVATION_STATUS_EXPIRED},
	} {
		t.Run(test.name, func(t *testing.T) {
			reservation := newTestReservation(t, []domain.ReservationItem{item}, time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC))
			if test.expire {
				reservation.Expire()
			}
			response, err := reservationResponse(reservation)
			if err != nil {
				t.Fatalf("reservationResponse() error = %v", err)
			}
			if response.Status != test.wantStatus || response.Status == inventoryv1.ReservationStatus_RESERVATION_STATUS_UNSPECIFIED {
				t.Fatalf("status = %v, want %v and never UNSPECIFIED", response.Status, test.wantStatus)
			}
		})
	}
}

func newTestReservation(t *testing.T, items []domain.ReservationItem, expiresAt time.Time) *domain.Reservation {
	t.Helper()
	reservation, err := domain.NewReservation("reservation-001", "order-001", items, expiresAt)
	if err != nil {
		t.Fatalf("NewReservation() error = %v", err)
	}
	return reservation
}
