package grpcclient

import (
	"context"
	"errors"
	"testing"
	"time"

	inventoryv1 "github.com/kitphon/marketplace-order-system/contracts/inventory/v1"
	"github.com/kitphon/marketplace-order-system/services/order/internal/ports"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestInventoryClientMapsRequestAndValidatesReservedResponse(t *testing.T) {
	expiresAt := time.Date(2026, time.October, 11, 10, 0, 0, 0, time.UTC)
	service := &fakeInventoryServiceClient{responses: []*inventoryv1.ReserveInventoryResponse{{
		ReservationId: "reservation-001",
		Status:        inventoryv1.ReservationStatus_RESERVATION_STATUS_RESERVED,
		ExpiresAt:     timestamppb.New(expiresAt),
	}}}
	client := NewInventoryClient(service, testPolicy(t, 3))
	err := client.Reserve(context.Background(), reserveRequest(expiresAt))
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	if service.calls != 1 || len(service.requests) != 1 {
		t.Fatalf("calls = %d, requests = %d; want one", service.calls, len(service.requests))
	}
	request := service.requests[0]
	if request.OrderId != "order-001" || len(request.Items) != 1 ||
		request.Items[0].ProductId != "product-001" || request.Items[0].Quantity != 2 ||
		!request.ExpiresAt.AsTime().Equal(expiresAt) {
		t.Fatalf("request = %+v", request)
	}
}

func TestInventoryClientMapsStructuredBusinessFailuresWithoutRetry(t *testing.T) {
	tests := []struct {
		name   string
		code   codes.Code
		reason inventoryv1.InventoryFailureReason
		want   error
	}{
		{name: "insufficient stock", code: codes.FailedPrecondition, reason: inventoryv1.InventoryFailureReason_INVENTORY_FAILURE_REASON_INSUFFICIENT_STOCK, want: ports.ErrInsufficientStock},
		{name: "reservation conflict", code: codes.AlreadyExists, reason: inventoryv1.InventoryFailureReason_INVENTORY_FAILURE_REASON_RESERVATION_CONFLICT, want: ports.ErrReservationConflict},
		{name: "reservation expired", code: codes.FailedPrecondition, reason: inventoryv1.InventoryFailureReason_INVENTORY_FAILURE_REASON_RESERVATION_EXPIRED, want: ports.ErrReservationExpired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeInventoryServiceClient{errors: []error{inventoryStatusError(t, test.code, test.reason)}}
			client := NewInventoryClient(service, testPolicy(t, 3))
			err := client.Reserve(context.Background(), reserveRequest(time.Now().Add(time.Hour)))
			if !errors.Is(err, test.want) || service.calls != 1 {
				t.Fatalf("Reserve() error = %v, calls = %d", err, service.calls)
			}
		})
	}
}

func TestInventoryClientRejectsMissingOrUnknownStructuredDetail(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "missing", err: status.Error(codes.FailedPrecondition, "failure without detail")},
		{name: "unknown", err: inventoryStatusError(t, codes.FailedPrecondition, inventoryv1.InventoryFailureReason(99))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeInventoryServiceClient{errors: []error{test.err}}
			client := NewInventoryClient(service, testPolicy(t, 3))
			err := client.Reserve(context.Background(), reserveRequest(time.Now().Add(time.Hour)))
			if !errors.Is(err, ports.ErrMalformedInventoryResponse) || service.calls != 1 {
				t.Fatalf("Reserve() error = %v, calls = %d", err, service.calls)
			}
		})
	}
}

func TestInventoryClientRejectsMalformedSuccessResponse(t *testing.T) {
	service := &fakeInventoryServiceClient{responses: []*inventoryv1.ReserveInventoryResponse{{
		ReservationId: "reservation-001",
		Status:        inventoryv1.ReservationStatus_RESERVATION_STATUS_UNSPECIFIED,
		ExpiresAt:     timestamppb.Now(),
	}}}
	client := NewInventoryClient(service, testPolicy(t, 3))
	err := client.Reserve(context.Background(), reserveRequest(time.Now().Add(time.Hour)))
	if !errors.Is(err, ports.ErrMalformedInventoryResponse) {
		t.Fatalf("Reserve() error = %v, want ErrMalformedInventoryResponse", err)
	}
}

func TestInventoryClientRetriesTransientFailureThenSucceeds(t *testing.T) {
	service := &fakeInventoryServiceClient{
		responses: []*inventoryv1.ReserveInventoryResponse{nil, validInventoryResponse()},
		errors:    []error{status.Error(codes.Unavailable, "temporary"), nil},
	}
	client := NewInventoryClient(service, testPolicy(t, 3))
	if err := client.Reserve(context.Background(), reserveRequest(time.Now().Add(time.Hour))); err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	if service.calls != 2 {
		t.Fatalf("calls = %d, want 2", service.calls)
	}
}

func TestInventoryClientRetryExhaustionMapsUnavailable(t *testing.T) {
	service := &fakeInventoryServiceClient{errors: []error{
		status.Error(codes.Unavailable, "one"),
		status.Error(codes.Unavailable, "two"),
		status.Error(codes.Unavailable, "three"),
	}}
	client := NewInventoryClient(service, testPolicy(t, 3))
	err := client.Reserve(context.Background(), reserveRequest(time.Now().Add(time.Hour)))
	if !errors.Is(err, ports.ErrInventoryUnavailable) || service.calls != 3 {
		t.Fatalf("Reserve() error = %v, calls = %d", err, service.calls)
	}
}

func TestInventoryClientPropagatesParentCancellationAndDeadline(t *testing.T) {
	tests := []struct {
		name    string
		context func() (context.Context, context.CancelFunc)
		want    error
	}{
		{name: "canceled", context: func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, func() {}
		}, want: context.Canceled},
		{name: "deadline", context: func() (context.Context, context.CancelFunc) {
			return context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		}, want: context.DeadlineExceeded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := test.context()
			defer cancel()
			service := &fakeInventoryServiceClient{}
			client := NewInventoryClient(service, testPolicy(t, 3))
			err := client.Reserve(ctx, reserveRequest(time.Now().Add(time.Hour)))
			if !errors.Is(err, test.want) || service.calls != 0 {
				t.Fatalf("Reserve() error = %v, calls = %d", err, service.calls)
			}
		})
	}
}

func TestInventoryClientMapsUpstreamCancellationToUnavailableWithoutRetry(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "gRPC canceled", err: status.Error(codes.Canceled, "upstream canceled")},
		{name: "raw context canceled", err: context.Canceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeInventoryServiceClient{errors: []error{test.err}}
			client := NewInventoryClient(service, testPolicy(t, 3))
			err := client.Reserve(context.Background(), reserveRequest(time.Now().Add(time.Hour)))
			if !errors.Is(err, ports.ErrInventoryUnavailable) {
				t.Fatalf("Reserve() error = %v, want ErrInventoryUnavailable", err)
			}
			if errors.Is(err, context.Canceled) {
				t.Fatalf("Reserve() error = %v, must not identify as context.Canceled", err)
			}
			if service.calls != 1 {
				t.Fatalf("calls = %d, want 1", service.calls)
			}
		})
	}
}

func inventoryStatusError(
	t *testing.T,
	code codes.Code,
	reason inventoryv1.InventoryFailureReason,
) error {
	t.Helper()
	withDetails, err := status.New(code, "inventory failure").WithDetails(
		&inventoryv1.InventoryErrorDetail{Reason: reason},
	)
	if err != nil {
		t.Fatalf("WithDetails() error = %v", err)
	}
	return withDetails.Err()
}

func reserveRequest(expiresAt time.Time) ports.ReserveInventoryRequest {
	return ports.ReserveInventoryRequest{
		OrderID: "order-001",
		Items: []ports.ReservationItem{{
			ProductID: "product-001",
			Quantity:  2,
		}},
		ExpireAt: expiresAt,
	}
}

func validInventoryResponse() *inventoryv1.ReserveInventoryResponse {
	return &inventoryv1.ReserveInventoryResponse{
		ReservationId: "reservation-001",
		Status:        inventoryv1.ReservationStatus_RESERVATION_STATUS_RESERVED,
		ExpiresAt:     timestamppb.New(time.Now().Add(time.Hour)),
	}
}

type fakeInventoryServiceClient struct {
	calls     int
	requests  []*inventoryv1.ReserveInventoryRequest
	responses []*inventoryv1.ReserveInventoryResponse
	errors    []error
}

func (client *fakeInventoryServiceClient) ReserveInventory(
	_ context.Context,
	request *inventoryv1.ReserveInventoryRequest,
	_ ...grpc.CallOption,
) (*inventoryv1.ReserveInventoryResponse, error) {
	index := client.calls
	client.calls++
	client.requests = append(client.requests, request)
	var response *inventoryv1.ReserveInventoryResponse
	if index < len(client.responses) {
		response = client.responses[index]
	}
	var err error
	if index < len(client.errors) {
		err = client.errors[index]
	}
	return response, err
}
