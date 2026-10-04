package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/kitphon/marketplace-order-system/services/order/internal/application"
	"github.com/kitphon/marketplace-order-system/services/order/internal/domain"
	"github.com/kitphon/marketplace-order-system/services/order/internal/ports"
)

const maxCreateOrderBodyBytes = 1 << 20

type OrderHandler struct {
	createOrder *application.CreateOrder
}

func NewOrderHandler(createOrder *application.CreateOrder) *OrderHandler {
	return &OrderHandler{createOrder: createOrder}
}

type createOrderRequest struct {
	Items []createOrderItemRequest `json:"items"`
}

type createOrderItemRequest struct {
	ProductID string `json:"product_id"`
	Quantity  int    `json:"quantity"`
}

type orderResponse struct {
	ID          string              `json:"id"`
	CustomerID  string              `json:"customer_id"`
	Status      domain.OrderStatus  `json:"status"`
	Items       []orderItemResponse `json:"items"`
	TotalAmount int64               `json:"total_amount"`
	Currency    string              `json:"currency"`
	Version     int64               `json:"version"`
	CreatedAt   string              `json:"created_at"`
	UpdatedAt   string              `json:"updated_at"`
}

type orderItemResponse struct {
	ProductID string `json:"product_id"`
	SellerID  string `json:"seller_id"`
	Name      string `json:"name"`
	UnitPrice int64  `json:"unit_price"`
	Quantity  int    `json:"quantity"`
	Subtotal  int64  `json:"subtotal"`
}

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (handler *OrderHandler) Create(response http.ResponseWriter, request *http.Request) {
	customerID := request.Header.Get("X-Customer-ID")
	idempotencyKey := request.Header.Get("Idempotency-Key")
	if customerID == "" || idempotencyKey == "" {
		writeError(response, http.StatusBadRequest, "MISSING_REQUIRED_HEADER", "X-Customer-ID and Idempotency-Key are required")
		return
	}

	request.Body = http.MaxBytesReader(response, request.Body, maxCreateOrderBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var body createOrderRequest
	if err := decoder.Decode(&body); err != nil {
		writeError(response, http.StatusBadRequest, "INVALID_JSON", "request body is invalid")
		return
	}
	if err := ensureJSONEnd(decoder); err != nil {
		writeError(response, http.StatusBadRequest, "INVALID_JSON", "request body must contain exactly one JSON object")
		return
	}

	items := make([]application.CreateOrderItem, 0, len(body.Items))
	for _, item := range body.Items {
		items = append(items, application.CreateOrderItem{
			ProductID: item.ProductID,
			Quantity:  item.Quantity,
		})
	}
	order, err := handler.createOrder.Execute(request.Context(), application.CreateOrderCommand{
		CustomerID:     customerID,
		IdempotencyKey: idempotencyKey,
		Items:          items,
	})
	if err != nil {
		writeCreateOrderError(response, err)
		return
	}

	response.Header().Set("Location", "/v1/orders/"+order.ID())
	writeJSON(response, http.StatusCreated, responseFromOrder(order))
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("unexpected second JSON value")
	}
	return fmt.Errorf("decode trailing JSON: %w", err)
}

func writeCreateOrderError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, application.ErrInvalidCreateOrderRequest):
		writeError(response, http.StatusBadRequest, "INVALID_ORDER", "order request is invalid")
	case errors.Is(err, application.ErrProductNotFound):
		writeError(response, http.StatusNotFound, "PRODUCT_NOT_FOUND", "one or more products were not found")
	case errors.Is(err, application.ErrProductInactive):
		writeError(response, http.StatusUnprocessableEntity, "PRODUCT_INACTIVE", "one or more products are inactive")
	case errors.Is(err, application.ErrIdempotencyKeyReused):
		writeError(response, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED", "idempotency key was used with a different request")
	case errors.Is(err, ports.ErrInsufficientStock):
		writeError(response, http.StatusConflict, "INSUFFICIENT_STOCK", "insufficient stock")
	case errors.Is(err, ports.ErrVersionConflict), errors.Is(err, ports.ErrReservationConflict):
		writeError(response, http.StatusConflict, "ORDER_CONFLICT", "order could not be updated because its state changed")
	default:
		writeError(response, http.StatusInternalServerError, "INTERNAL_ERROR", "an unexpected error occurred")
	}
}

func responseFromOrder(order *domain.Order) orderResponse {
	items := make([]orderItemResponse, 0, len(order.Items()))
	for _, item := range order.Items() {
		items = append(items, orderItemResponse{
			ProductID: item.ProductID(),
			SellerID:  item.SellerID(),
			Name:      item.Name(),
			UnitPrice: item.UnitPrice().MinorUnits(),
			Quantity:  item.Quantity(),
			Subtotal:  item.Subtotal().MinorUnits(),
		})
	}
	return orderResponse{
		ID:          order.ID(),
		CustomerID:  order.CustomerID(),
		Status:      order.Status(),
		Items:       items,
		TotalAmount: order.TotalAmount().MinorUnits(),
		Currency:    order.Currency(),
		Version:     order.Version(),
		CreatedAt:   order.CreatedAt().Format("2006-01-02T15:04:05.000Z07:00"),
		UpdatedAt:   order.UpdatedAt().Format("2006-01-02T15:04:05.000Z07:00"),
	}
}

func writeError(response http.ResponseWriter, status int, code, message string) {
	writeJSON(response, status, errorResponse{Code: code, Message: message})
}

func writeJSON(response http.ResponseWriter, status int, payload any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(payload)
}
