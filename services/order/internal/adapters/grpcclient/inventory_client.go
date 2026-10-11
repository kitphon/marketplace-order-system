package grpcclient

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	inventoryv1 "github.com/kitphon/marketplace-order-system/contracts/inventory/v1"
	"github.com/kitphon/marketplace-order-system/services/order/internal/ports"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type InventoryClient struct {
	client inventoryv1.InventoryServiceClient
	retry  *RetryPolicy
}

func NewInventoryClient(client inventoryv1.InventoryServiceClient, retry *RetryPolicy) *InventoryClient {
	return &InventoryClient{client: client, retry: retry}
}

func (client *InventoryClient) Reserve(
	ctx context.Context,
	request ports.ReserveInventoryRequest,
) error {
	protoRequest, err := inventoryRequest(request)
	if err != nil {
		return err
	}

	var response *inventoryv1.ReserveInventoryResponse
	exhausted, err := client.retry.Do(ctx, func(attemptContext context.Context) error {
		var callErr error
		response, callErr = client.client.ReserveInventory(attemptContext, protoRequest)
		return callErr
	})
	if err != nil {
		return mapInventoryError(ctx, exhausted, err)
	}
	return validateInventoryResponse(response)
}

func inventoryRequest(request ports.ReserveInventoryRequest) (*inventoryv1.ReserveInventoryRequest, error) {
	if strings.TrimSpace(request.OrderID) == "" || len(request.Items) == 0 {
		return nil, ports.ErrInvalidInventoryRequest
	}
	expiresAt := timestamppb.New(request.ExpireAt)
	if err := expiresAt.CheckValid(); err != nil {
		return nil, ports.ErrInvalidInventoryRequest
	}
	items := make([]*inventoryv1.ReservationItem, 0, len(request.Items))
	seen := make(map[string]struct{}, len(request.Items))
	for _, item := range request.Items {
		if strings.TrimSpace(item.ProductID) == "" || item.Quantity <= 0 || item.Quantity > math.MaxInt32 {
			return nil, ports.ErrInvalidInventoryRequest
		}
		if _, exists := seen[item.ProductID]; exists {
			return nil, ports.ErrInvalidInventoryRequest
		}
		seen[item.ProductID] = struct{}{}
		items = append(items, &inventoryv1.ReservationItem{
			ProductId: item.ProductID,
			Quantity:  int32(item.Quantity),
		})
	}
	return &inventoryv1.ReserveInventoryRequest{
		OrderId:   request.OrderID,
		Items:     items,
		ExpiresAt: expiresAt,
	}, nil
}

func validateInventoryResponse(response *inventoryv1.ReserveInventoryResponse) error {
	if response == nil ||
		strings.TrimSpace(response.ReservationId) == "" ||
		response.Status != inventoryv1.ReservationStatus_RESERVATION_STATUS_RESERVED ||
		response.ExpiresAt == nil ||
		response.ExpiresAt.CheckValid() != nil {
		return ports.ErrMalformedInventoryResponse
	}
	return nil
}

func mapInventoryError(ctx context.Context, exhausted bool, err error) error {
	if parentErr := ctx.Err(); parentErr != nil {
		return parentErr
	}
	if errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled {
		return context.Canceled
	}
	if exhausted {
		return fmt.Errorf("%w: %v", ports.ErrInventoryUnavailable, err)
	}

	grpcStatus := status.Convert(err)
	for _, detail := range grpcStatus.Details() {
		inventoryDetail, ok := detail.(*inventoryv1.InventoryErrorDetail)
		if !ok {
			continue
		}
		switch inventoryDetail.Reason {
		case inventoryv1.InventoryFailureReason_INVENTORY_FAILURE_REASON_INSUFFICIENT_STOCK:
			if grpcStatus.Code() != codes.FailedPrecondition {
				return ports.ErrMalformedInventoryResponse
			}
			return ports.ErrInsufficientStock
		case inventoryv1.InventoryFailureReason_INVENTORY_FAILURE_REASON_RESERVATION_CONFLICT:
			if grpcStatus.Code() != codes.AlreadyExists {
				return ports.ErrMalformedInventoryResponse
			}
			return ports.ErrReservationConflict
		case inventoryv1.InventoryFailureReason_INVENTORY_FAILURE_REASON_RESERVATION_EXPIRED:
			if grpcStatus.Code() != codes.FailedPrecondition {
				return ports.ErrMalformedInventoryResponse
			}
			return ports.ErrReservationExpired
		default:
			return ports.ErrMalformedInventoryResponse
		}
	}

	switch grpcStatus.Code() {
	case codes.InvalidArgument:
		return ports.ErrInvalidInventoryRequest
	case codes.FailedPrecondition, codes.AlreadyExists:
		return ports.ErrMalformedInventoryResponse
	default:
		return fmt.Errorf("%w: upstream code %s", ports.ErrInventoryUnavailable, grpcStatus.Code())
	}
}

var _ ports.InventoryClient = (*InventoryClient)(nil)
