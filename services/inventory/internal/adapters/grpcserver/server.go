package grpcserver

import (
	"context"
	"errors"

	inventoryv1 "github.com/kitphon/marketplace-order-system/contracts/inventory/v1"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/application"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/domain"
	"github.com/kitphon/marketplace-order-system/services/inventory/internal/ports"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Server struct {
	inventoryv1.UnimplementedInventoryServiceServer
	reserveInventory *application.ReserveInventory
}

func New(reserveInventory *application.ReserveInventory) *Server {
	return &Server{reserveInventory: reserveInventory}
}

func (server *Server) ReserveInventory(
	ctx context.Context,
	request *inventoryv1.ReserveInventoryRequest,
) (*inventoryv1.ReserveInventoryResponse, error) {
	command, err := reserveCommand(request)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "inventory reservation request is invalid")
	}
	reservation, err := server.reserveInventory.Execute(ctx, command)
	if err != nil {
		return nil, mapError(err)
	}
	if reservation.Status() != domain.ReservationStatusReserved {
		return nil, status.Error(codes.Internal, "inventory reservation returned an unexpected status")
	}
	return &inventoryv1.ReserveInventoryResponse{
		ReservationId: reservation.ID(),
		Status:        inventoryv1.ReservationStatus_RESERVATION_STATUS_RESERVED,
		ExpiresAt:     timestamppb.New(reservation.ExpiresAt()),
	}, nil
}

func reserveCommand(request *inventoryv1.ReserveInventoryRequest) (application.ReserveInventoryCommand, error) {
	if request == nil || request.ExpiresAt == nil {
		return application.ReserveInventoryCommand{}, application.ErrInvalidReserveInventoryRequest
	}
	if err := request.ExpiresAt.CheckValid(); err != nil {
		return application.ReserveInventoryCommand{}, application.ErrInvalidReserveInventoryRequest
	}
	items := make([]application.ReserveInventoryItem, 0, len(request.Items))
	for _, item := range request.Items {
		if item == nil {
			return application.ReserveInventoryCommand{}, application.ErrInvalidReserveInventoryRequest
		}
		items = append(items, application.ReserveInventoryItem{
			ProductID: item.ProductId,
			Quantity:  int(item.Quantity),
		})
	}
	return application.ReserveInventoryCommand{
		OrderID:   request.OrderId,
		Items:     items,
		ExpiresAt: request.ExpiresAt.AsTime(),
	}, nil
}

func mapError(err error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return status.FromContextError(err).Err()
	case errors.Is(err, application.ErrInvalidReserveInventoryRequest):
		return status.Error(codes.InvalidArgument, "inventory reservation request is invalid")
	case errors.Is(err, ports.ErrInsufficientStock):
		var stockError *ports.InsufficientStockError
		var productID string
		if errors.As(err, &stockError) {
			productID = stockError.ProductID
		}
		return businessError(
			codes.FailedPrecondition,
			"insufficient stock",
			inventoryv1.InventoryFailureReason_INVENTORY_FAILURE_REASON_INSUFFICIENT_STOCK,
			productID,
		)
	case errors.Is(err, ports.ErrReservationConflict):
		return businessError(
			codes.AlreadyExists,
			"reservation conflicts with the existing order reservation",
			inventoryv1.InventoryFailureReason_INVENTORY_FAILURE_REASON_RESERVATION_CONFLICT,
			"",
		)
	case errors.Is(err, ports.ErrReservationExpired):
		return businessError(
			codes.FailedPrecondition,
			"reservation has expired",
			inventoryv1.InventoryFailureReason_INVENTORY_FAILURE_REASON_RESERVATION_EXPIRED,
			"",
		)
	default:
		return status.Error(codes.Internal, "reserve inventory failed")
	}
}

func businessError(
	code codes.Code,
	message string,
	reason inventoryv1.InventoryFailureReason,
	productID string,
) error {
	detail := &inventoryv1.InventoryErrorDetail{Reason: reason}
	if productID != "" {
		detail.ProductId = &productID
	}
	withDetails, err := status.New(code, message).WithDetails(detail)
	if err != nil {
		return status.Error(codes.Internal, "build inventory error detail")
	}
	return withDetails.Err()
}
