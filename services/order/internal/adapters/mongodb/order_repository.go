package mongodb

import (
	"context"
	"errors"
	"fmt"

	"github.com/example/marketplace-order-system/services/order/internal/domain"
	"github.com/example/marketplace-order-system/services/order/internal/ports"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const ordersCollection = "orders"

type OrderRepository struct {
	collection *mongo.Collection
}

func NewOrderRepository(database *mongo.Database) *OrderRepository {
	return &OrderRepository{collection: database.Collection(ordersCollection)}
}

func (repository *OrderRepository) EnsureIndexes(ctx context.Context) error {
	models := []mongo.IndexModel{
		{
			Keys: bson.D{
				{Key: "customer_id", Value: 1},
				{Key: "idempotency_key", Value: 1},
			},
			Options: options.Index().
				SetName("uq_customer_idempotency_key").
				SetUnique(true),
		},
		{
			Keys: bson.D{
				{Key: "status", Value: 1},
				{Key: "updated_at", Value: 1},
			},
			Options: options.Index().SetName("ix_status_updated_at"),
		},
	}
	if _, err := repository.collection.Indexes().CreateMany(ctx, models); err != nil {
		return fmt.Errorf("create order indexes: %w", err)
	}
	return nil
}

func (repository *OrderRepository) FindByIdempotencyKey(
	ctx context.Context,
	customerID string,
	idempotencyKey string,
) (*domain.Order, error) {
	filter := bson.D{
		{Key: "customer_id", Value: customerID},
		{Key: "idempotency_key", Value: idempotencyKey},
	}
	var document orderDocument
	if err := repository.collection.FindOne(ctx, filter).Decode(&document); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ports.ErrOrderNotFound
		}
		return nil, fmt.Errorf("find order by idempotency key: %w", err)
	}
	order, err := document.toOrder()
	if err != nil {
		return nil, fmt.Errorf("rehydrate order %s: %w", document.ID, err)
	}
	return order, nil
}

func (repository *OrderRepository) Create(ctx context.Context, order *domain.Order) error {
	document := documentFromOrder(order)
	if _, err := repository.collection.InsertOne(ctx, document); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return repository.classifyDuplicateKey(ctx, order, err)
		}
		return fmt.Errorf("insert order %s: %w", order.ID(), err)
	}
	return nil
}

func (repository *OrderRepository) Update(
	ctx context.Context,
	order *domain.Order,
	expectedVersion int64,
) error {
	if order.Version() != expectedVersion+1 {
		return ports.ErrVersionConflict
	}

	filter := bson.D{
		{Key: "_id", Value: order.ID()},
		{Key: "version", Value: expectedVersion},
	}
	result, err := repository.collection.ReplaceOne(ctx, filter, documentFromOrder(order))
	if err != nil {
		return fmt.Errorf("replace order %s: %w", order.ID(), err)
	}
	if result.MatchedCount == 1 {
		return nil
	}

	existsErr := repository.collection.FindOne(
		ctx,
		bson.D{{Key: "_id", Value: order.ID()}},
	).Err()
	switch {
	case errors.Is(existsErr, mongo.ErrNoDocuments):
		return ports.ErrOrderNotFound
	case existsErr != nil:
		return fmt.Errorf("classify failed order update %s: %w", order.ID(), existsErr)
	default:
		return ports.ErrVersionConflict
	}
}

func (repository *OrderRepository) classifyDuplicateKey(
	ctx context.Context,
	order *domain.Order,
	duplicateErr error,
) error {
	filter := bson.D{
		{Key: "customer_id", Value: order.CustomerID()},
		{Key: "idempotency_key", Value: order.IdempotencyKey()},
	}
	err := repository.collection.FindOne(ctx, filter).Err()
	switch {
	case err == nil:
		return ports.ErrDuplicateIdempotency
	case errors.Is(err, mongo.ErrNoDocuments):
		return ports.ErrOrderIDConflict
	default:
		return fmt.Errorf("classify duplicate key after insert order %s: %v: %w", order.ID(), duplicateErr, err)
	}
}

