package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kitphon/marketplace-order-system/services/inventory/internal/domain"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/ports"
)

var ErrInvalidGetReservationRequest = errors.New("invalid get reservation request")

type GetReservationByOrderID struct {
	repository ports.InventoryRepository
}

func NewGetReservationByOrderID(repository ports.InventoryRepository) *GetReservationByOrderID {
	return &GetReservationByOrderID{repository: repository}
}

func (useCase *GetReservationByOrderID) Execute(
	ctx context.Context,
	orderID string,
) (*domain.Reservation, error) {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return nil, ErrInvalidGetReservationRequest
	}
	reservation, err := useCase.repository.FindReservationByOrderID(ctx, orderID)
	if err == nil {
		return reservation, nil
	}
	if errors.Is(err, ports.ErrReservationNotFound) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	return nil, fmt.Errorf("get reservation for order %q: %w", orderID, err)
}
