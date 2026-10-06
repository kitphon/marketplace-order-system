package memory

import (
	"context"
	"sync"
	"time"

	"github.com/kitphon/marketplace-order-system/services/inventory/internal/domain"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/ports"
)

// InventoryRepository is safe inside one process. Its single mutex makes each
// stock/reservation transition atomic within that process.
type InventoryRepository struct {
	mu           sync.Mutex
	available    map[string]int
	reservations map[string]*domain.Reservation
}

func NewInventoryRepository(stock []domain.Stock) *InventoryRepository {
	available := make(map[string]int, len(stock))
	for _, item := range stock {
		available[item.ProductID()] = item.Available()
	}
	return &InventoryRepository{
		available:    available,
		reservations: make(map[string]*domain.Reservation),
	}
}

func (repository *InventoryRepository) Reserve(
	ctx context.Context,
	request ports.ReserveRequest,
) (*domain.Reservation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	repository.mu.Lock()
	defer repository.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if existing, exists := repository.reservations[request.OrderID]; exists {
		if existing.Status() == domain.ReservationStatusReserved && !request.Now.Before(existing.ExpiresAt()) {
			repository.expireLocked(existing)
		}
		if existing.Status() == domain.ReservationStatusExpired {
			return nil, ports.ErrReservationExpired
		}
		if !sameItems(existing.Items(), request.Items) {
			return nil, ports.ErrReservationConflict
		}
		return existing.Clone(), nil
	}

	for _, item := range request.Items {
		if repository.available[item.ProductID()] < item.Quantity() {
			return nil, &ports.InsufficientStockError{ProductID: item.ProductID()}
		}
	}
	for _, item := range request.Items {
		repository.available[item.ProductID()] -= item.Quantity()
	}
	reservation, err := domain.NewReservation(
		request.ReservationID,
		request.OrderID,
		request.Items,
		request.ExpiresAt,
	)
	if err != nil {
		for _, item := range request.Items {
			repository.available[item.ProductID()] += item.Quantity()
		}
		return nil, err
	}
	repository.reservations[request.OrderID] = reservation
	return reservation.Clone(), nil
}

func (repository *InventoryRepository) ExpireReservations(
	ctx context.Context,
	now time.Time,
) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	expired := 0
	for _, reservation := range repository.reservations {
		if reservation.Status() == domain.ReservationStatusReserved && !now.Before(reservation.ExpiresAt()) {
			repository.expireLocked(reservation)
			expired++
		}
	}
	return expired, nil
}

func (repository *InventoryRepository) Available(productID string) int {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.available[productID]
}

func (repository *InventoryRepository) expireLocked(reservation *domain.Reservation) {
	if !reservation.Expire() {
		return
	}
	for _, item := range reservation.Items() {
		repository.available[item.ProductID()] += item.Quantity()
	}
}

func sameItems(left, right []domain.ReservationItem) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].ProductID() != right[index].ProductID() || left[index].Quantity() != right[index].Quantity() {
			return false
		}
	}
	return true
}
