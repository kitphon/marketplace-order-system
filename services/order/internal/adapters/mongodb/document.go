package mongodb

import (
	"fmt"
	"time"

	"github.com/kitphon/marketplace-order-system/services/order/internal/domain"
)

type orderDocument struct {
	ID             string              `bson:"_id"`
	CustomerID     string              `bson:"customer_id"`
	IdempotencyKey string              `bson:"idempotency_key"`
	RequestHash    string              `bson:"request_hash"`
	Items          []orderItemDocument `bson:"items"`
	TotalAmount    int64               `bson:"total_amount"`
	Currency       string              `bson:"currency"`
	Status         string              `bson:"status"`
	CancelReason   string              `bson:"cancel_reason,omitempty"`
	Version        int64               `bson:"version"`
	CreatedAt      time.Time           `bson:"created_at"`
	UpdatedAt      time.Time           `bson:"updated_at"`
}

type orderItemDocument struct {
	ProductID string `bson:"product_id"`
	SellerID  string `bson:"seller_id"`
	Name      string `bson:"name"`
	UnitPrice int64  `bson:"unit_price"`
	Quantity  int    `bson:"quantity"`
	Subtotal  int64  `bson:"subtotal"`
}

func documentFromOrder(order *domain.Order) orderDocument {
	snapshot := order.Snapshot()
	items := make([]orderItemDocument, 0, len(snapshot.Items))
	for _, item := range snapshot.Items {
		items = append(items, orderItemDocument{
			ProductID: item.ProductID(),
			SellerID:  item.SellerID(),
			Name:      item.Name(),
			UnitPrice: item.UnitPrice().MinorUnits(),
			Quantity:  item.Quantity(),
			Subtotal:  item.Subtotal().MinorUnits(),
		})
	}

	return orderDocument{
		ID:             snapshot.ID,
		CustomerID:     snapshot.CustomerID,
		IdempotencyKey: snapshot.IdempotencyKey,
		RequestHash:    snapshot.RequestHash,
		Items:          items,
		TotalAmount:    snapshot.TotalAmount.MinorUnits(),
		Currency:       snapshot.Currency,
		Status:         string(snapshot.Status),
		CancelReason:   snapshot.CancelReason,
		Version:        snapshot.Version,
		CreatedAt:      snapshot.CreatedAt,
		UpdatedAt:      snapshot.UpdatedAt,
	}
}

func (document orderDocument) toOrder() (*domain.Order, error) {
	items := make([]domain.OrderItem, 0, len(document.Items))
	for _, storedItem := range document.Items {
		unitPrice, err := domain.NewMoney(storedItem.UnitPrice)
		if err != nil {
			return nil, fmt.Errorf("decode unit price for %s: %w", storedItem.ProductID, err)
		}
		item, err := domain.NewOrderItem(
			storedItem.ProductID,
			storedItem.SellerID,
			storedItem.Name,
			unitPrice,
			storedItem.Quantity,
		)
		if err != nil {
			return nil, fmt.Errorf("decode order item %s: %w", storedItem.ProductID, err)
		}
		if item.Subtotal().MinorUnits() != storedItem.Subtotal {
			return nil, fmt.Errorf("decode order item %s: %w", storedItem.ProductID, domain.ErrOrderTotalMismatch)
		}
		items = append(items, item)
	}

	total, err := domain.NewMoney(document.TotalAmount)
	if err != nil {
		return nil, fmt.Errorf("decode order total: %w", err)
	}
	return domain.RehydrateOrder(domain.OrderSnapshot{
		ID:             document.ID,
		CustomerID:     document.CustomerID,
		IdempotencyKey: document.IdempotencyKey,
		RequestHash:    document.RequestHash,
		Items:          items,
		TotalAmount:    total,
		Currency:       document.Currency,
		Status:         domain.OrderStatus(document.Status),
		CancelReason:   document.CancelReason,
		Version:        document.Version,
		CreatedAt:      document.CreatedAt,
		UpdatedAt:      document.UpdatedAt,
	})
}
