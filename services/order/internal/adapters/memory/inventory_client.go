package memory

import (
	"context"
	"sync"

	"github.com/kitphon/marketplace-order-system/services/order/internal/ports"
)

type InventoryClient struct {
	mu           sync.Mutex
	available    map[string]int
	reservations map[string]ports.ReserveInventoryRequest
}

func NewInventoryClient(available map[string]int) *InventoryClient {
	stock := make(map[string]int, len(available))
	for productID, quantity := range available {
		stock[productID] = quantity
	}
	return &InventoryClient{
		available:    stock,
		reservations: make(map[string]ports.ReserveInventoryRequest),
	}
}

func (client *InventoryClient) Reserve(
	ctx context.Context,
	request ports.ReserveInventoryRequest,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if existing, exists := client.reservations[request.OrderID]; exists {
		if sameReservation(existing, request) {
			return nil
		}
		return ports.ErrReservationConflict
	}

	for _, item := range request.Items {
		if item.Quantity <= 0 || client.available[item.ProductID] < item.Quantity {
			return ports.ErrInsufficientStock
		}
	}
	for _, item := range request.Items {
		client.available[item.ProductID] -= item.Quantity
	}
	client.reservations[request.OrderID] = cloneReservation(request)
	return nil
}

func sameReservation(left, right ports.ReserveInventoryRequest) bool {
	if left.OrderID != right.OrderID || len(left.Items) != len(right.Items) {
		return false
	}
	for index := range left.Items {
		if left.Items[index] != right.Items[index] {
			return false
		}
	}
	return true
}

func cloneReservation(request ports.ReserveInventoryRequest) ports.ReserveInventoryRequest {
	clone := request
	clone.Items = append([]ports.ReservationItem(nil), request.Items...)
	return clone
}
