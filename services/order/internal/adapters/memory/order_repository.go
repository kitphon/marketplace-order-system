package memory

import (
	"context"
	"sync"

	"github.com/kitphon/marketplace-order-system/services/order/internal/domain"
	"github.com/kitphon/marketplace-order-system/services/order/internal/ports"
)

type idempotencyIdentity struct {
	customerID string
	key        string
}

type OrderRepository struct {
	mu            sync.RWMutex
	byID          map[string]*domain.Order
	byIdempotency map[idempotencyIdentity]string
}

func NewOrderRepository() *OrderRepository {
	return &OrderRepository{
		byID:          make(map[string]*domain.Order),
		byIdempotency: make(map[idempotencyIdentity]string),
	}
}

func (r *OrderRepository) FindByIdempotencyKey(
	_ context.Context,
	customerID string,
	idempotencyKey string,
) (*domain.Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	identity := idempotencyIdentity{customerID: customerID, key: idempotencyKey}
	orderID, exists := r.byIdempotency[identity]
	if !exists {
		return nil, ports.ErrOrderNotFound
	}
	return r.byID[orderID].Clone(), nil
}

func (r *OrderRepository) Create(_ context.Context, order *domain.Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	identity := idempotencyIdentity{
		customerID: order.CustomerID(),
		key:        order.IdempotencyKey(),
	}
	if _, exists := r.byIdempotency[identity]; exists {
		return ports.ErrDuplicateIdempotency
	}

	r.byID[order.ID()] = order.Clone()
	r.byIdempotency[identity] = order.ID()
	return nil
}

func (r *OrderRepository) Update(
	_ context.Context,
	order *domain.Order,
	expectedVersion int64,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	stored, exists := r.byID[order.ID()]
	if !exists {
		return ports.ErrOrderNotFound
	}
	if stored.Version() != expectedVersion {
		return ports.ErrVersionConflict
	}
	if order.Version() != expectedVersion+1 {
		return ports.ErrVersionConflict
	}

	r.byID[order.ID()] = order.Clone()
	return nil
}
