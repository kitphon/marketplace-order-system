package mongodb

import (
	"testing"
	"time"

	"github.com/kitphon/marketplace-order-system/services/inventory/internal/domain"
)

func TestReservationDocumentRehydratesThroughDomainValidation(t *testing.T) {
	now := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	document := validReservationDocument(now)
	document.Status = string(domain.ReservationStatusExpired)

	reservation, err := document.toReservation()
	if err != nil {
		t.Fatalf("toReservation() error = %v", err)
	}
	if reservation.Status() != domain.ReservationStatusExpired {
		t.Fatalf("status = %q, want EXPIRED", reservation.Status())
	}
	items := reservation.Items()
	items[0] = domain.ReservationItem{}
	if reservation.Items()[0].ProductID() != "product-001" {
		t.Fatal("Reservation.Items() exposed its internal slice")
	}
}

func TestReservationDocumentRejectsCorruption(t *testing.T) {
	now := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*reservationDocument)
	}{
		{name: "blank reservation id", mutate: func(document *reservationDocument) { document.ID = " " }},
		{name: "blank order id", mutate: func(document *reservationDocument) { document.OrderID = " " }},
		{name: "unknown status", mutate: func(document *reservationDocument) { document.Status = "LOST" }},
		{name: "zero version", mutate: func(document *reservationDocument) { document.Version = 0 }},
		{name: "zero expiry", mutate: func(document *reservationDocument) { document.ExpiresAt = time.Time{} }},
		{name: "zero created at", mutate: func(document *reservationDocument) { document.CreatedAt = time.Time{} }},
		{name: "updated before created", mutate: func(document *reservationDocument) { document.UpdatedAt = now.Add(-time.Minute) }},
		{name: "empty items", mutate: func(document *reservationDocument) { document.Items = nil }},
		{name: "blank product id", mutate: func(document *reservationDocument) { document.Items[0].ProductID = " " }},
		{name: "invalid quantity", mutate: func(document *reservationDocument) { document.Items[0].Quantity = 0 }},
		{name: "duplicate product", mutate: func(document *reservationDocument) { document.Items = append(document.Items, document.Items[0]) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := validReservationDocument(now)
			test.mutate(&document)
			if _, err := document.toReservation(); err == nil {
				t.Fatal("toReservation() error = nil, want corruption error")
			}
		})
	}
}

func validReservationDocument(now time.Time) reservationDocument {
	return reservationDocument{
		ID:      "reservation-001",
		OrderID: "order-001",
		Items: []reservationItemDocument{{
			ProductID: "product-001",
			Quantity:  2,
		}},
		Status:    string(domain.ReservationStatusReserved),
		ExpiresAt: now.Add(time.Hour),
		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
	}
}
