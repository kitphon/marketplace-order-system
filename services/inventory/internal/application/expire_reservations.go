package application

import (
	"context"
	"time"

	"github.com/kitphon/marketplace-order-system/services/inventory/internal/ports"
)

type ExpireReservations struct {
	repository ports.InventoryRepository
	clock      ports.Clock
}

func NewExpireReservations(repository ports.InventoryRepository, clock ports.Clock) *ExpireReservations {
	return &ExpireReservations{repository: repository, clock: clock}
}

func (useCase *ExpireReservations) Execute(ctx context.Context) (int, error) {
	return useCase.repository.ExpireReservations(ctx, useCase.clock.Now().UTC())
}

func RunExpirationLoop(
	ctx context.Context,
	interval time.Duration,
	useCase *ExpireReservations,
	onError func(error),
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := useCase.Execute(ctx); err != nil && ctx.Err() == nil && onError != nil {
				onError(err)
			}
		}
	}
}
