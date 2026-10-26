package grpcserver_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	inventoryv1 "github.com/kitphon/marketplace-order-system/contracts/inventory/v1"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/adapters/grpcserver"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/adapters/memory"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/application"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/domain"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/ports"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestReserveInventoryMapping(t *testing.T) {
	harness := newGRPCHarness(t, map[string]int{"product-001": 10})
	expiresAt := harness.clock.Now().Add(time.Hour)

	response, err := harness.client.ReserveInventory(context.Background(), request(
		"order-001",
		expiresAt,
		&inventoryv1.ReservationItem{ProductId: "product-001", Quantity: 2},
	))
	if err != nil {
		t.Fatalf("ReserveInventory() error = %v", err)
	}
	if response.ReservationId != "reservation-001" {
		t.Fatalf("reservation_id = %q, want reservation-001", response.ReservationId)
	}
	if response.Status != inventoryv1.ReservationStatus_RESERVATION_STATUS_RESERVED {
		t.Fatalf("status = %v, want RESERVED", response.Status)
	}
	if !response.ExpiresAt.AsTime().Equal(expiresAt) {
		t.Fatalf("expires_at = %v, want %v", response.ExpiresAt.AsTime(), expiresAt)
	}
}

func TestReserveInventoryInvalidRequestStatus(t *testing.T) {
	harness := newGRPCHarness(t, nil)
	_, err := harness.client.ReserveInventory(context.Background(), &inventoryv1.ReserveInventoryRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("status = %v, want InvalidArgument; error=%v", status.Code(err), err)
	}
}

func TestReserveInventoryInsufficientStockDetail(t *testing.T) {
	harness := newGRPCHarness(t, map[string]int{"product-001": 1})
	_, err := harness.client.ReserveInventory(context.Background(), request(
		"order-001",
		harness.clock.Now().Add(time.Hour),
		&inventoryv1.ReservationItem{ProductId: "product-001", Quantity: 2},
	))
	assertInventoryError(
		t,
		err,
		codes.FailedPrecondition,
		inventoryv1.InventoryFailureReason_INVENTORY_FAILURE_REASON_INSUFFICIENT_STOCK,
		"product-001",
	)
}

func TestReserveInventoryConflictDetail(t *testing.T) {
	harness := newGRPCHarness(t, map[string]int{"product-001": 10})
	expiresAt := harness.clock.Now().Add(time.Hour)
	if _, err := harness.client.ReserveInventory(context.Background(), request(
		"order-001",
		expiresAt,
		&inventoryv1.ReservationItem{ProductId: "product-001", Quantity: 1},
	)); err != nil {
		t.Fatalf("first ReserveInventory() error = %v", err)
	}

	_, err := harness.client.ReserveInventory(context.Background(), request(
		"order-001",
		expiresAt,
		&inventoryv1.ReservationItem{ProductId: "product-001", Quantity: 2},
	))
	assertInventoryError(
		t,
		err,
		codes.AlreadyExists,
		inventoryv1.InventoryFailureReason_INVENTORY_FAILURE_REASON_RESERVATION_CONFLICT,
		"",
	)
}

func TestReserveInventoryExpiredDetail(t *testing.T) {
	harness := newGRPCHarness(t, map[string]int{"product-001": 10})
	if _, err := harness.client.ReserveInventory(context.Background(), request(
		"order-001",
		harness.clock.Now().Add(time.Hour),
		&inventoryv1.ReservationItem{ProductId: "product-001", Quantity: 1},
	)); err != nil {
		t.Fatalf("first ReserveInventory() error = %v", err)
	}
	harness.clock.Set(harness.clock.Now().Add(2 * time.Hour))

	_, err := harness.client.ReserveInventory(context.Background(), request(
		"order-001",
		harness.clock.Now().Add(time.Hour),
		&inventoryv1.ReservationItem{ProductId: "product-001", Quantity: 1},
	))
	assertInventoryError(
		t,
		err,
		codes.FailedPrecondition,
		inventoryv1.InventoryFailureReason_INVENTORY_FAILURE_REASON_RESERVATION_EXPIRED,
		"",
	)
}

func TestReserveInventoryUnknownErrorHasNoBusinessDetail(t *testing.T) {
	clock := &testClock{now: time.Date(2026, time.October, 6, 9, 0, 0, 0, time.UTC)}
	server := grpcserver.New(application.NewReserveInventory(
		failingRepository{err: errors.New("storage unavailable")},
		clock,
		&sequenceIDs{},
	), application.NewGetReservationByOrderID(failingRepository{err: errors.New("storage unavailable")}))
	_, err := server.ReserveInventory(context.Background(), request(
		"order-001",
		clock.Now().Add(time.Hour),
		&inventoryv1.ReservationItem{ProductId: "product-001", Quantity: 1},
	))
	gotStatus := status.Convert(err)
	if gotStatus.Code() != codes.Internal {
		t.Fatalf("status = %v, want Internal; error=%v", gotStatus.Code(), err)
	}
	if details := gotStatus.Details(); len(details) != 0 {
		t.Fatalf("details = %v, want none", details)
	}
}

func TestReserveInventoryMapsRepositoryContextErrorsWithoutBusinessDetail(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode codes.Code
	}{
		{name: "canceled", err: fmt.Errorf("repository transaction: %w", context.Canceled), wantCode: codes.Canceled},
		{name: "deadline exceeded", err: context.DeadlineExceeded, wantCode: codes.DeadlineExceeded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clock := &testClock{now: time.Date(2026, time.October, 6, 9, 0, 0, 0, time.UTC)}
			server := grpcserver.New(application.NewReserveInventory(
				failingRepository{err: test.err},
				clock,
				&sequenceIDs{},
			), application.NewGetReservationByOrderID(failingRepository{err: test.err}))
			_, err := server.ReserveInventory(context.Background(), request(
				"order-001",
				clock.Now().Add(time.Hour),
				&inventoryv1.ReservationItem{ProductId: "product-001", Quantity: 1},
			))
			gotStatus := status.Convert(err)
			if gotStatus.Code() != test.wantCode {
				t.Fatalf("status = %v, want %v; error=%v", gotStatus.Code(), test.wantCode, err)
			}
			if details := gotStatus.Details(); len(details) != 0 {
				t.Fatalf("details = %v, want none", details)
			}
		})
	}
}

func TestGetReservationByOrderIDMapsReservedResponseAndIsReadOnly(t *testing.T) {
	harness := newGRPCHarness(t, map[string]int{"product-001": 10, "product-002": 10})
	expiresAt := harness.clock.Now().Add(time.Hour)
	reserved, err := harness.client.ReserveInventory(context.Background(), request(
		"order-001",
		expiresAt,
		&inventoryv1.ReservationItem{ProductId: "product-002", Quantity: 3},
		&inventoryv1.ReservationItem{ProductId: "product-001", Quantity: 2},
	))
	if err != nil {
		t.Fatalf("ReserveInventory() error = %v", err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		response, err := harness.client.GetReservationByOrderID(context.Background(), &inventoryv1.GetReservationByOrderIDRequest{OrderId: "order-001"})
		if err != nil {
			t.Fatalf("GetReservationByOrderID() attempt %d error = %v", attempt+1, err)
		}
		if response.ReservationId != reserved.ReservationId || response.OrderId != "order-001" ||
			response.Status != inventoryv1.ReservationStatus_RESERVATION_STATUS_RESERVED ||
			!response.ExpiresAt.AsTime().Equal(expiresAt) {
			t.Fatalf("response = %+v", response)
		}
		if len(response.Items) != 2 || response.Items[0].ProductId != "product-001" ||
			response.Items[0].Quantity != 2 || response.Items[1].ProductId != "product-002" ||
			response.Items[1].Quantity != 3 {
			t.Fatalf("canonical items = %+v", response.Items)
		}
	}
	if got := harness.repository.Available("product-001"); got != 8 {
		t.Fatalf("product-001 available = %d, want 8", got)
	}
	if got := harness.repository.Available("product-002"); got != 7 {
		t.Fatalf("product-002 available = %d, want 7", got)
	}
}

func TestGetReservationByOrderIDMapsExpiredResponse(t *testing.T) {
	harness := newGRPCHarness(t, map[string]int{"product-001": 10})
	expiresAt := harness.clock.Now().Add(time.Hour)
	if _, err := harness.client.ReserveInventory(context.Background(), request(
		"order-001", expiresAt, &inventoryv1.ReservationItem{ProductId: "product-001", Quantity: 2},
	)); err != nil {
		t.Fatalf("ReserveInventory() error = %v", err)
	}
	harness.clock.Set(expiresAt)
	if count, err := application.NewExpireReservations(harness.repository, harness.clock).Execute(context.Background()); err != nil || count != 1 {
		t.Fatalf("ExpireReservations.Execute() = (%d, %v), want (1, nil)", count, err)
	}
	response, err := harness.client.GetReservationByOrderID(context.Background(), &inventoryv1.GetReservationByOrderIDRequest{OrderId: "order-001"})
	if err != nil {
		t.Fatalf("GetReservationByOrderID() error = %v", err)
	}
	if response.Status != inventoryv1.ReservationStatus_RESERVATION_STATUS_EXPIRED ||
		!response.ExpiresAt.AsTime().Equal(expiresAt) {
		t.Fatalf("response = %+v, want EXPIRED with original expiry", response)
	}
}

func TestGetReservationByOrderIDErrorCodes(t *testing.T) {
	harness := newGRPCHarness(t, nil)
	for _, test := range []struct {
		name    string
		request *inventoryv1.GetReservationByOrderIDRequest
		code    codes.Code
	}{
		{name: "nil", request: nil, code: codes.InvalidArgument},
		{name: "blank", request: &inventoryv1.GetReservationByOrderIDRequest{OrderId: " \t"}, code: codes.InvalidArgument},
		{name: "missing", request: &inventoryv1.GetReservationByOrderIDRequest{OrderId: "missing"}, code: codes.NotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := harness.client.GetReservationByOrderID(context.Background(), test.request)
			if status.Code(err) != test.code {
				t.Fatalf("code = %v, want %v; error=%v", status.Code(err), test.code, err)
			}
		})
	}
}

func TestGetReservationByOrderIDNilRepositoryResultIsInternal(t *testing.T) {
	server := grpcserver.New(nil, application.NewGetReservationByOrderID(failingRepository{}))
	response, err := server.GetReservationByOrderID(
		context.Background(),
		&inventoryv1.GetReservationByOrderIDRequest{OrderId: "order-001"},
	)
	if response != nil || status.Code(err) != codes.Internal {
		t.Fatalf("GetReservationByOrderID() = (%+v, %v), want (nil, Internal)", response, err)
	}
}

func request(
	orderID string,
	expiresAt time.Time,
	items ...*inventoryv1.ReservationItem,
) *inventoryv1.ReserveInventoryRequest {
	return &inventoryv1.ReserveInventoryRequest{
		OrderId:   orderID,
		Items:     items,
		ExpiresAt: timestamppb.New(expiresAt),
	}
}

func assertInventoryError(
	t *testing.T,
	err error,
	wantCode codes.Code,
	wantReason inventoryv1.InventoryFailureReason,
	wantProductID string,
) {
	t.Helper()
	gotStatus := status.Convert(err)
	if gotStatus.Code() != wantCode {
		t.Fatalf("status = %v, want %v; error=%v", gotStatus.Code(), wantCode, err)
	}
	details := gotStatus.Details()
	if len(details) != 1 {
		t.Fatalf("details count = %d, want 1; details=%v", len(details), details)
	}
	detail, ok := details[0].(*inventoryv1.InventoryErrorDetail)
	if !ok {
		t.Fatalf("detail type = %T, want *InventoryErrorDetail", details[0])
	}
	if detail.Reason != wantReason {
		t.Fatalf("reason = %v, want %v", detail.Reason, wantReason)
	}
	if detail.GetProductId() != wantProductID {
		t.Fatalf("product_id = %q, want %q", detail.GetProductId(), wantProductID)
	}
}

type grpcHarness struct {
	client     inventoryv1.InventoryServiceClient
	clock      *testClock
	repository *memory.InventoryRepository
}

func newGRPCHarness(t *testing.T, quantities map[string]int) grpcHarness {
	t.Helper()
	stock := make([]domain.Stock, 0, len(quantities))
	for productID, available := range quantities {
		item, err := domain.NewStock(productID, available)
		if err != nil {
			t.Fatalf("NewStock() error = %v", err)
		}
		stock = append(stock, item)
	}
	repository := memory.NewInventoryRepository(stock)
	clock := &testClock{now: time.Date(2026, time.October, 6, 9, 0, 0, 0, time.UTC)}
	useCase := application.NewReserveInventory(repository, clock, &sequenceIDs{})
	getReservation := application.NewGetReservationByOrderID(repository)

	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	inventoryv1.RegisterInventoryServiceServer(server, grpcserver.New(useCase, getReservation))
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(server.Stop)
	t.Cleanup(func() { _ = listener.Close() })

	connection, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient() error = %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return grpcHarness{
		client:     inventoryv1.NewInventoryServiceClient(connection),
		clock:      clock,
		repository: repository,
	}
}

type testClock struct {
	mu  sync.RWMutex
	now time.Time
}

func (clock *testClock) Now() time.Time {
	clock.mu.RLock()
	defer clock.mu.RUnlock()
	return clock.now
}

func (clock *testClock) Set(now time.Time) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = now
}

type sequenceIDs struct{ next atomic.Int64 }

func (ids *sequenceIDs) NewID() (string, error) {
	return fmt.Sprintf("reservation-%03d", ids.next.Add(1)), nil
}

type failingRepository struct{ err error }

func (repository failingRepository) Reserve(context.Context, ports.ReserveRequest) (*domain.Reservation, error) {
	return nil, repository.err
}

func (repository failingRepository) FindReservationByOrderID(context.Context, string) (*domain.Reservation, error) {
	return nil, repository.err
}

func (repository failingRepository) ExpireReservations(context.Context, time.Time) (int, error) {
	return 0, repository.err
}
