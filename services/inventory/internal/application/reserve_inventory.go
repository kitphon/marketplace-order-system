package application

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kitphon/marketplace-order-system/services/inventory/internal/domain"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/ports"
)

var ErrInvalidReserveInventoryRequest = errors.New("invalid reserve inventory request")

type ReserveInventoryItem struct {
	ProductID string
	Quantity  int
}

type ReserveInventoryCommand struct {
	OrderID   string
	Items     []ReserveInventoryItem
	ExpiresAt time.Time
}

type ReserveInventory struct {
	repository ports.InventoryRepository
	clock      ports.Clock
	ids        ports.IDGenerator
}

func NewReserveInventory(
	repository ports.InventoryRepository,
	clock ports.Clock,
	ids ports.IDGenerator,
) *ReserveInventory {
	return &ReserveInventory{repository: repository, clock: clock, ids: ids}
}

func (useCase *ReserveInventory) Execute(
	ctx context.Context,
	command ReserveInventoryCommand,
) (*domain.Reservation, error) {
	now := useCase.clock.Now().UTC()
	items, err := validateAndCanonicalize(command, now)
	if err != nil {
		return nil, err
	}
	reservationID, err := useCase.ids.NewID()
	if err != nil {
		return nil, fmt.Errorf("generate reservation id: %w", err)
	}
	return useCase.repository.Reserve(ctx, ports.ReserveRequest{
		ReservationID: reservationID,
		OrderID:       command.OrderID,
		Items:         items,
		ExpiresAt:     command.ExpiresAt.UTC(),
		Now:           now,
	})
}

func validateAndCanonicalize(
	command ReserveInventoryCommand,
	now time.Time,
) ([]domain.ReservationItem, error) {
	if strings.TrimSpace(command.OrderID) == "" || len(command.Items) == 0 || !command.ExpiresAt.After(now) {
		return nil, ErrInvalidReserveInventoryRequest
	}
	seen := make(map[string]struct{}, len(command.Items))
	items := make([]domain.ReservationItem, 0, len(command.Items))
	for _, requested := range command.Items {
		if strings.TrimSpace(requested.ProductID) == "" || requested.Quantity <= 0 {
			return nil, ErrInvalidReserveInventoryRequest
		}
		if _, exists := seen[requested.ProductID]; exists {
			return nil, ErrInvalidReserveInventoryRequest
		}
		seen[requested.ProductID] = struct{}{}
		item, err := domain.NewReservationItem(requested.ProductID, requested.Quantity)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidReserveInventoryRequest, err)
		}
		items = append(items, item)
	}
	sort.Slice(items, func(left, right int) bool {
		return items[left].ProductID() < items[right].ProductID()
	})
	return items, nil
}
