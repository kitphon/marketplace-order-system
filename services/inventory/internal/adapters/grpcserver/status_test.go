package grpcserver

import (
	"testing"

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
