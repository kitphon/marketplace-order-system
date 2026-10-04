package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/example/marketplace-order-system/services/order/internal/domain"
	"github.com/example/marketplace-order-system/services/order/internal/ports"
)

const inventoryReservationTTL = 15 * time.Minute

type CreateOrderItem struct {
	ProductID string `json:"product_id"`
	Quantity  int    `json:"quantity"`
}

type CreateOrderCommand struct {
	CustomerID     string
	IdempotencyKey string
	Items          []CreateOrderItem
}

type CreateOrder struct {
	orders    ports.OrderRepository
	products  ports.ProductClient
	inventory ports.InventoryClient
	ids       ports.IDGenerator
	clock     ports.Clock
}

func NewCreateOrder(
	orders ports.OrderRepository,
	products ports.ProductClient,
	inventory ports.InventoryClient,
	ids ports.IDGenerator,
	clock ports.Clock,
) *CreateOrder {
	return &CreateOrder{
		orders:    orders,
		products:  products,
		inventory: inventory,
		ids:       ids,
		clock:     clock,
	}
}

func (uc *CreateOrder) Execute(
	ctx context.Context,
	command CreateOrderCommand,
) (*domain.Order, error) {
	requestHash, err := hashCreateOrderRequest(command.Items)
	if err != nil {
		return nil, err
	}
	if command.CustomerID == "" || command.IdempotencyKey == "" {
		return nil, ErrInvalidCreateOrderRequest
	}

	existing, err := uc.orders.FindByIdempotencyKey(
		ctx,
		command.CustomerID,
		command.IdempotencyKey,
	)
	switch {
	case err == nil:
		return resolveIdempotentOrder(existing, requestHash)
	case !errors.Is(err, ports.ErrOrderNotFound):
		return nil, fmt.Errorf("find order by idempotency key: %w", err)
	}

	productIDs := make([]string, 0, len(command.Items))
	for _, item := range command.Items {
		productIDs = append(productIDs, item.ProductID)
	}

	products, err := uc.products.GetByIDs(ctx, productIDs)
	if err != nil {
		return nil, fmt.Errorf("get product snapshots: %w", err)
	}

	orderItems, err := createOrderItems(command.Items, products)
	if err != nil {
		return nil, err
	}

	now := uc.clock.Now()
	orderID, err := uc.ids.NewID()
	if err != nil {
		return nil, fmt.Errorf("generate order id: %w", err)
	}
	order, err := domain.NewOrder(
		orderID,
		command.CustomerID,
		command.IdempotencyKey,
		requestHash,
		orderItems,
		domain.CurrencyTHB,
		now,
	)
	if err != nil {
		return nil, fmt.Errorf("create order aggregate: %w", err)
	}

	if err := uc.orders.Create(ctx, order); err != nil {
		if errors.Is(err, ports.ErrDuplicateIdempotency) {
			return uc.loadConcurrentWinner(ctx, command, requestHash)
		}
		return nil, fmt.Errorf("persist pending order: %w", err)
	}

	reservation := ports.ReserveInventoryRequest{
		OrderID:  order.ID(),
		Items:    reservationItems(command.Items),
		ExpireAt: now.Add(inventoryReservationTTL),
	}
	if err := uc.inventory.Reserve(ctx, reservation); err != nil {
		if errors.Is(err, ports.ErrInsufficientStock) {
			return uc.cancelForInsufficientStock(ctx, order, now)
		}
		// The PENDING order is intentionally retained. A later milestone adds
		// retry/reconciliation for an ambiguous inventory result.
		return order, fmt.Errorf("reserve inventory: %w", err)
	}

	expectedVersion := order.Version()
	if err := order.MarkInventoryReserved(uc.clock.Now()); err != nil {
		return nil, fmt.Errorf("mark inventory reserved: %w", err)
	}
	if err := uc.orders.Update(ctx, order, expectedVersion); err != nil {
		return order, fmt.Errorf("persist inventory-reserved order: %w", err)
	}

	return order, nil
}

func (uc *CreateOrder) loadConcurrentWinner(
	ctx context.Context,
	command CreateOrderCommand,
	requestHash string,
) (*domain.Order, error) {
	winner, err := uc.orders.FindByIdempotencyKey(
		ctx,
		command.CustomerID,
		command.IdempotencyKey,
	)
	if err != nil {
		return nil, fmt.Errorf("load concurrent idempotency winner: %w", err)
	}
	return resolveIdempotentOrder(winner, requestHash)
}

func (uc *CreateOrder) cancelForInsufficientStock(
	ctx context.Context,
	order *domain.Order,
	now time.Time,
) (*domain.Order, error) {
	expectedVersion := order.Version()
	if err := order.Cancel("INSUFFICIENT_STOCK", now); err != nil {
		return nil, fmt.Errorf("cancel order: %w", err)
	}
	if err := uc.orders.Update(ctx, order, expectedVersion); err != nil {
		return order, fmt.Errorf("persist cancelled order: %w", err)
	}
	return order, ports.ErrInsufficientStock
}

func resolveIdempotentOrder(order *domain.Order, requestHash string) (*domain.Order, error) {
	if order.RequestHash() != requestHash {
		return nil, ErrIdempotencyKeyReused
	}
	return order, nil
}

func hashCreateOrderRequest(items []CreateOrderItem) (string, error) {
	if len(items) == 0 || len(items) > 100 {
		return "", ErrInvalidCreateOrderRequest
	}

	canonical := append([]CreateOrderItem(nil), items...)
	seen := make(map[string]struct{}, len(canonical))
	for _, item := range canonical {
		if item.ProductID == "" || item.Quantity <= 0 {
			return "", ErrInvalidCreateOrderRequest
		}
		if _, exists := seen[item.ProductID]; exists {
			return "", fmt.Errorf("%w: duplicate product %s", ErrInvalidCreateOrderRequest, item.ProductID)
		}
		seen[item.ProductID] = struct{}{}
	}

	sort.Slice(canonical, func(i, j int) bool {
		return canonical[i].ProductID < canonical[j].ProductID
	})
	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("marshal canonical order request: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func createOrderItems(
	requested []CreateOrderItem,
	products []ports.ProductSnapshot,
) ([]domain.OrderItem, error) {
	byID := make(map[string]ports.ProductSnapshot, len(products))
	for _, product := range products {
		if _, exists := byID[product.ProductID]; exists {
			return nil, fmt.Errorf("%w: duplicate %s", ErrUnexpectedProduct, product.ProductID)
		}
		byID[product.ProductID] = product
	}

	items := make([]domain.OrderItem, 0, len(requested))
	for _, requestedItem := range requested {
		product, exists := byID[requestedItem.ProductID]
		if !exists {
			return nil, fmt.Errorf("%w: %s", ErrProductNotFound, requestedItem.ProductID)
		}
		if !product.Active {
			return nil, fmt.Errorf("%w: %s", ErrProductInactive, requestedItem.ProductID)
		}
		item, err := domain.NewOrderItem(
			product.ProductID,
			product.SellerID,
			product.Name,
			product.UnitPrice,
			requestedItem.Quantity,
		)
		if err != nil {
			return nil, fmt.Errorf("create order item %s: %w", requestedItem.ProductID, err)
		}
		items = append(items, item)
	}
	return items, nil
}

func reservationItems(items []CreateOrderItem) []ports.ReservationItem {
	result := make([]ports.ReservationItem, 0, len(items))
	for _, item := range items {
		result = append(result, ports.ReservationItem{
			ProductID: item.ProductID,
			Quantity:  item.Quantity,
		})
	}
	return result
}
