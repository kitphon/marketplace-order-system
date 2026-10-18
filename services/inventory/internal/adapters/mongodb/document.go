package mongodb

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kitphon/marketplace-order-system/services/inventory/internal/domain"
)

type stockDocument struct {
	ProductID string    `bson:"_id"`
	Available int       `bson:"available"`
	Version   int64     `bson:"version"`
	CreatedAt time.Time `bson:"created_at"`
	UpdatedAt time.Time `bson:"updated_at"`
}

type reservationItemDocument struct {
	ProductID string `bson:"product_id"`
	Quantity  int    `bson:"quantity"`
}

type reservationDocument struct {
	ID        string                    `bson:"_id"`
	OrderID   string                    `bson:"order_id"`
	Items     []reservationItemDocument `bson:"items"`
	Status    string                    `bson:"status"`
	ExpiresAt time.Time                 `bson:"expires_at"`
	Version   int64                     `bson:"version"`
	CreatedAt time.Time                 `bson:"created_at"`
	UpdatedAt time.Time                 `bson:"updated_at"`
}

func newReservationDocument(
	reservationID string,
	orderID string,
	items []domain.ReservationItem,
	expiresAt time.Time,
	now time.Time,
) reservationDocument {
	documentItems := make([]reservationItemDocument, len(items))
	for index, item := range items {
		documentItems[index] = reservationItemDocument{
			ProductID: item.ProductID(),
			Quantity:  item.Quantity(),
		}
	}
	return reservationDocument{
		ID:        reservationID,
		OrderID:   orderID,
		Items:     documentItems,
		Status:    string(domain.ReservationStatusReserved),
		ExpiresAt: expiresAt.UTC(),
		Version:   1,
		CreatedAt: now.UTC(),
		UpdatedAt: now.UTC(),
	}
}

func (document reservationDocument) toReservation() (*domain.Reservation, error) {
	if strings.TrimSpace(document.ID) == "" {
		return nil, fmt.Errorf("corrupt reservation: blank reservation id")
	}
	if strings.TrimSpace(document.OrderID) == "" {
		return nil, fmt.Errorf("corrupt reservation %q: blank order id", document.ID)
	}
	if document.Version <= 0 {
		return nil, fmt.Errorf("corrupt reservation %q: invalid version %d", document.ID, document.Version)
	}
	if document.ExpiresAt.IsZero() {
		return nil, fmt.Errorf("corrupt reservation %q: invalid expiry", document.ID)
	}
	if document.CreatedAt.IsZero() || document.UpdatedAt.IsZero() {
		return nil, fmt.Errorf("corrupt reservation %q: missing timestamps", document.ID)
	}
	if document.UpdatedAt.Before(document.CreatedAt) {
		return nil, fmt.Errorf("corrupt reservation %q: updated_at precedes created_at", document.ID)
	}

	items := make([]domain.ReservationItem, 0, len(document.Items))
	seen := make(map[string]struct{}, len(document.Items))
	for _, stored := range document.Items {
		if strings.TrimSpace(stored.ProductID) == "" || stored.Quantity <= 0 {
			return nil, fmt.Errorf("corrupt reservation %q: invalid item", document.ID)
		}
		if _, exists := seen[stored.ProductID]; exists {
			return nil, fmt.Errorf("corrupt reservation %q: duplicate product %q", document.ID, stored.ProductID)
		}
		seen[stored.ProductID] = struct{}{}
		item, err := domain.NewReservationItem(stored.ProductID, stored.Quantity)
		if err != nil {
			return nil, fmt.Errorf("corrupt reservation %q item: %w", document.ID, err)
		}
		items = append(items, item)
	}
	sort.Slice(items, func(left, right int) bool {
		return items[left].ProductID() < items[right].ProductID()
	})

	reservation, err := domain.NewReservation(
		document.ID,
		document.OrderID,
		items,
		document.ExpiresAt,
	)
	if err != nil {
		return nil, fmt.Errorf("corrupt reservation %q: %w", document.ID, err)
	}
	switch domain.ReservationStatus(document.Status) {
	case domain.ReservationStatusReserved:
		return reservation, nil
	case domain.ReservationStatusExpired:
		reservation.Expire()
		return reservation, nil
	default:
		return nil, fmt.Errorf("corrupt reservation %q: unknown status %q", document.ID, document.Status)
	}
}
