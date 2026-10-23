package mongodb

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kitphon/marketplace-order-system/services/inventory/internal/domain"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/ports"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

const (
	stocksCollection           = "stocks"
	reservationsCollection     = "reservations"
	reservationOrderIndex      = "uq_reservations_order_id"
	reservationExpirationIndex = "ix_reservations_status_expires_at"
	defaultExpirationBatchSize = int64(100)
	defaultTransactionTimeout  = 10 * time.Second
	initialDocumentVersion     = int64(1)
)

type InventoryRepository struct {
	client              *mongo.Client
	stocks              *mongo.Collection
	reservations        *mongo.Collection
	transactionOptions  *options.TransactionOptionsBuilder
	expirationBatchSize int64
	transactionTimeout  time.Duration
}

var _ ports.InventoryRepository = (*InventoryRepository)(nil)

func NewInventoryRepository(database *mongo.Database) *InventoryRepository {
	return &InventoryRepository{
		client:       database.Client(),
		stocks:       database.Collection(stocksCollection),
		reservations: database.Collection(reservationsCollection),
		transactionOptions: options.Transaction().
			SetReadConcern(readconcern.Snapshot()).
			SetReadPreference(readpref.Primary()).
			SetWriteConcern(writeconcern.Majority()),
		expirationBatchSize: defaultExpirationBatchSize,
		transactionTimeout:  defaultTransactionTimeout,
	}
}

func (repository *InventoryRepository) EnsureIndexes(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	models := []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "order_id", Value: 1}},
			Options: options.Index().
				SetName(reservationOrderIndex).
				SetUnique(true),
		},
		{
			Keys: bson.D{
				{Key: "status", Value: 1},
				{Key: "expires_at", Value: 1},
			},
			Options: options.Index().SetName(reservationExpirationIndex),
		},
	}
	if _, err := repository.reservations.Indexes().CreateMany(ctx, models); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		return repositoryFailure("create inventory reservation indexes", err)
	}
	return nil
}

// SeedStocks inserts only products that do not already have a stock document.
// Existing availability and versions are never reset.
func (repository *InventoryRepository) SeedStocks(ctx context.Context, stocks []domain.Stock) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	canonical := append([]domain.Stock(nil), stocks...)
	sort.Slice(canonical, func(left, right int) bool {
		return canonical[left].ProductID() < canonical[right].ProductID()
	})
	for index, stock := range canonical {
		if strings.TrimSpace(stock.ProductID()) == "" || stock.Available() < 0 {
			return fmt.Errorf("invalid seed stock at index %d", index)
		}
		if index > 0 && canonical[index-1].ProductID() == stock.ProductID() {
			return fmt.Errorf("duplicate seed stock product %q", stock.ProductID())
		}
	}

	now := time.Now().UTC()
	for _, stock := range canonical {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := repository.stocks.UpdateOne(
			ctx,
			bson.D{{Key: "_id", Value: stock.ProductID()}},
			bson.D{{Key: "$setOnInsert", Value: stockDocument{
				ProductID: stock.ProductID(),
				Available: stock.Available(),
				Version:   initialDocumentVersion,
				CreatedAt: now,
				UpdatedAt: now,
			}}},
			options.UpdateOne().SetUpsert(true),
		)
		if err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return contextErr
			}
			if mongo.IsDuplicateKeyError(err) {
				// Another service instance won the upsert race. Confirm the
				// winner now exists and preserve its quantity and version.
				findErr := repository.stocks.FindOne(
					ctx,
					bson.D{{Key: "_id", Value: stock.ProductID()}},
				).Err()
				if findErr == nil {
					continue
				}
				if contextErr := ctx.Err(); contextErr != nil {
					return contextErr
				}
			}
			return repositoryFailure(fmt.Sprintf("seed stock %q", stock.ProductID()), err)
		}
	}
	return nil
}

func (repository *InventoryRepository) Reserve(
	ctx context.Context,
	request ports.ReserveRequest,
) (*domain.Reservation, error) {
	request.Now = request.Now.UTC().Truncate(time.Millisecond)
	request.ExpiresAt = request.ExpiresAt.UTC().Truncate(time.Millisecond)
	canonical, err := validateReserveRequest(request)
	if err != nil {
		return nil, err
	}
	request.Items = canonical

	result, err := repository.reserveTransaction(ctx, request)
	if err != nil && mongo.IsDuplicateKeyError(err) {
		// A concurrent transaction may have committed the unique order_id winner.
		// The failed transaction was aborted, so classifying in a fresh transaction
		// cannot leak its tentative stock decrements.
		result, err = repository.reserveTransaction(ctx, request)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		if errors.Is(err, ports.ErrInsufficientStock) {
			return nil, err
		}
		return nil, repositoryFailure("reserve inventory", err)
	}
	switch result.outcome {
	case reserveOutcomeReserved:
		return result.reservation.Clone(), nil
	case reserveOutcomeConflict:
		return nil, ports.ErrReservationConflict
	case reserveOutcomeExpired:
		return nil, ports.ErrReservationExpired
	default:
		return nil, fmt.Errorf("reserve inventory: unknown transaction outcome")
	}
}

func (repository *InventoryRepository) FindReservationByOrderID(
	ctx context.Context,
	orderID string,
) (*domain.Reservation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var document reservationDocument
	err := repository.reservations.FindOne(
		ctx,
		bson.D{{Key: "order_id", Value: orderID}},
	).Decode(&document)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ports.ErrReservationNotFound
	}
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, repositoryFailure("find reservation by order id", err)
	}
	reservation, err := document.toReservation()
	if err != nil {
		return nil, fmt.Errorf("find reservation by order id: %w", err)
	}
	return reservation.Clone(), nil
}

type reserveOutcome int

const (
	reserveOutcomeReserved reserveOutcome = iota + 1
	reserveOutcomeConflict
	reserveOutcomeExpired
)

type reserveResult struct {
	outcome     reserveOutcome
	reservation *domain.Reservation
}

func (repository *InventoryRepository) reserveTransaction(
	ctx context.Context,
	request ports.ReserveRequest,
) (reserveResult, error) {
	transactionContext, cancel := context.WithTimeout(ctx, repository.transactionTimeout)
	defer cancel()
	if err := transactionContext.Err(); err != nil {
		return reserveResult{}, err
	}
	session, err := repository.client.StartSession()
	if err != nil {
		return reserveResult{}, fmt.Errorf("start inventory transaction: %w", err)
	}
	defer session.EndSession(context.Background())

	value, err := session.WithTransaction(
		transactionContext,
		func(transactionContext context.Context) (any, error) {
			return repository.reserveInTransaction(transactionContext, request)
		},
		repository.transactionOptions,
	)
	if err != nil {
		if contextErr := transactionContext.Err(); contextErr != nil {
			return reserveResult{}, contextErr
		}
		return reserveResult{}, fmt.Errorf("reserve inventory transaction: %w", err)
	}
	result, ok := value.(reserveResult)
	if !ok {
		return reserveResult{}, fmt.Errorf("reserve inventory transaction returned invalid result")
	}
	return result, nil
}

func (repository *InventoryRepository) reserveInTransaction(
	ctx context.Context,
	request ports.ReserveRequest,
) (reserveResult, error) {
	var existingDocument reservationDocument
	err := repository.reservations.FindOne(
		ctx,
		bson.D{{Key: "order_id", Value: request.OrderID}},
	).Decode(&existingDocument)
	if err == nil {
		existing, mapErr := existingDocument.toReservation()
		if mapErr != nil {
			return reserveResult{}, mapErr
		}
		if !sameReservationItems(existing.Items(), request.Items) {
			return reserveResult{outcome: reserveOutcomeConflict}, nil
		}
		if existing.Status() == domain.ReservationStatusReserved && !request.Now.Before(existing.ExpiresAt()) {
			expired, expireErr := repository.expireOneInTransaction(
				ctx,
				existingDocument,
				request.Now,
			)
			if expireErr != nil {
				return reserveResult{}, expireErr
			}
			if !expired {
				return reserveResult{}, fmt.Errorf("lazy expiration lost reservation version")
			}
			// Return the business outcome as data so WithTransaction commits the
			// expiration and stock restoration before ErrReservationExpired escapes.
			return reserveResult{outcome: reserveOutcomeExpired}, nil
		}
		if existing.Status() == domain.ReservationStatusExpired {
			return reserveResult{outcome: reserveOutcomeExpired}, nil
		}
		return reserveResult{outcome: reserveOutcomeReserved, reservation: existing}, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return reserveResult{}, err
	}

	for _, item := range request.Items {
		result, updateErr := repository.stocks.UpdateOne(
			ctx,
			bson.D{
				{Key: "_id", Value: item.ProductID()},
				{Key: "available", Value: bson.D{{Key: "$gte", Value: item.Quantity()}}},
			},
			bson.D{
				{Key: "$inc", Value: bson.D{
					{Key: "available", Value: -item.Quantity()},
					{Key: "version", Value: 1},
				}},
				{Key: "$set", Value: bson.D{{Key: "updated_at", Value: request.Now.UTC()}}},
			},
		)
		if updateErr != nil {
			return reserveResult{}, updateErr
		}
		if result.MatchedCount != 1 {
			return reserveResult{}, &ports.InsufficientStockError{ProductID: item.ProductID()}
		}
	}

	document := newReservationDocument(
		request.ReservationID,
		request.OrderID,
		request.Items,
		request.ExpiresAt,
		request.Now,
	)
	if _, err := repository.reservations.InsertOne(ctx, document); err != nil {
		return reserveResult{}, err
	}
	reservation, err := document.toReservation()
	if err != nil {
		return reserveResult{}, err
	}
	return reserveResult{outcome: reserveOutcomeReserved, reservation: reservation}, nil
}

func (repository *InventoryRepository) ExpireReservations(
	ctx context.Context,
	now time.Time,
) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	cursor, err := repository.reservations.Find(
		ctx,
		bson.D{
			{Key: "status", Value: string(domain.ReservationStatusReserved)},
			{Key: "expires_at", Value: bson.D{{Key: "$lte", Value: now.UTC()}}},
		},
		options.Find().
			SetSort(bson.D{{Key: "expires_at", Value: 1}, {Key: "_id", Value: 1}}).
			SetLimit(repository.expirationBatchSize),
	)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return 0, contextErr
		}
		return 0, repositoryFailure("find expired reservation candidates", err)
	}
	defer cursor.Close(context.Background())

	var candidates []reservationDocument
	if err := cursor.All(ctx, &candidates); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return 0, contextErr
		}
		return 0, repositoryFailure("decode expired reservation candidates", err)
	}

	expiredCount := 0
	for _, candidate := range candidates {
		if _, err := candidate.toReservation(); err != nil {
			return expiredCount, err
		}
		expired, err := repository.expireCandidate(ctx, candidate, now.UTC())
		if err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return expiredCount, contextErr
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return expiredCount, err
			}
			return expiredCount, repositoryFailure("expire reservations", err)
		}
		if expired {
			expiredCount++
		}
	}
	return expiredCount, nil
}

func (repository *InventoryRepository) expireCandidate(
	ctx context.Context,
	candidate reservationDocument,
	now time.Time,
) (bool, error) {
	transactionContext, cancel := context.WithTimeout(ctx, repository.transactionTimeout)
	defer cancel()
	session, err := repository.client.StartSession()
	if err != nil {
		return false, fmt.Errorf("start expiration transaction: %w", err)
	}
	defer session.EndSession(context.Background())
	value, err := session.WithTransaction(
		transactionContext,
		func(transactionContext context.Context) (any, error) {
			var current reservationDocument
			err := repository.reservations.FindOne(
				transactionContext,
				bson.D{{Key: "_id", Value: candidate.ID}},
			).Decode(&current)
			if errors.Is(err, mongo.ErrNoDocuments) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			if _, err := current.toReservation(); err != nil {
				return false, err
			}
			if current.Version != candidate.Version ||
				current.Status != string(domain.ReservationStatusReserved) ||
				current.ExpiresAt.After(now) {
				return false, nil
			}
			return repository.expireOneInTransaction(transactionContext, current, now)
		},
		repository.transactionOptions,
	)
	if err != nil {
		if contextErr := transactionContext.Err(); contextErr != nil {
			return false, contextErr
		}
		return false, fmt.Errorf("expire reservation %q transaction: %w", candidate.ID, err)
	}
	expired, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("expire reservation %q returned invalid result", candidate.ID)
	}
	return expired, nil
}

func (repository *InventoryRepository) expireOneInTransaction(
	ctx context.Context,
	document reservationDocument,
	now time.Time,
) (bool, error) {
	result, err := repository.reservations.UpdateOne(
		ctx,
		bson.D{
			{Key: "_id", Value: document.ID},
			{Key: "status", Value: string(domain.ReservationStatusReserved)},
			{Key: "expires_at", Value: bson.D{{Key: "$lte", Value: now}}},
			{Key: "version", Value: document.Version},
		},
		bson.D{
			{Key: "$set", Value: bson.D{
				{Key: "status", Value: string(domain.ReservationStatusExpired)},
				{Key: "updated_at", Value: now},
			}},
			{Key: "$inc", Value: bson.D{{Key: "version", Value: 1}}},
		},
	)
	if err != nil {
		return false, err
	}
	if result.MatchedCount != 1 {
		return false, nil
	}
	for _, item := range document.Items {
		result, err := repository.stocks.UpdateOne(
			ctx,
			bson.D{{Key: "_id", Value: item.ProductID}},
			bson.D{
				{Key: "$inc", Value: bson.D{
					{Key: "available", Value: item.Quantity},
					{Key: "version", Value: 1},
				}},
				{Key: "$set", Value: bson.D{{Key: "updated_at", Value: now}}},
			},
		)
		if err != nil {
			return false, err
		}
		if result.MatchedCount != 1 {
			return false, fmt.Errorf("restore reservation %q stock %q: stock document missing", document.ID, item.ProductID)
		}
	}
	return true, nil
}

func validateReserveRequest(request ports.ReserveRequest) ([]domain.ReservationItem, error) {
	if strings.TrimSpace(request.ReservationID) == "" ||
		strings.TrimSpace(request.OrderID) == "" ||
		request.Now.IsZero() ||
		!request.ExpiresAt.After(request.Now) ||
		len(request.Items) == 0 {
		return nil, fmt.Errorf("invalid reserve request")
	}
	items := append([]domain.ReservationItem(nil), request.Items...)
	sort.Slice(items, func(left, right int) bool {
		return items[left].ProductID() < items[right].ProductID()
	})
	for index, item := range items {
		if strings.TrimSpace(item.ProductID()) == "" || item.Quantity() <= 0 {
			return nil, fmt.Errorf("invalid reserve request item at index %d", index)
		}
		if index > 0 && items[index-1].ProductID() == item.ProductID() {
			return nil, fmt.Errorf("invalid reserve request: duplicate product %q", item.ProductID())
		}
	}
	return items, nil
}

func sameReservationItems(left, right []domain.ReservationItem) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy := append([]domain.ReservationItem(nil), left...)
	rightCopy := append([]domain.ReservationItem(nil), right...)
	sort.Slice(leftCopy, func(i, j int) bool { return leftCopy[i].ProductID() < leftCopy[j].ProductID() })
	sort.Slice(rightCopy, func(i, j int) bool { return rightCopy[i].ProductID() < rightCopy[j].ProductID() })
	for index := range leftCopy {
		if leftCopy[index].ProductID() != rightCopy[index].ProductID() ||
			leftCopy[index].Quantity() != rightCopy[index].Quantity() {
			return false
		}
	}
	return true
}

func repositoryFailure(operation string, err error) error {
	// Preserve operational diagnostics without allowing MongoDB-specific error
	// types to become part of the repository port contract.
	return fmt.Errorf("%s: %v", operation, err)
}
