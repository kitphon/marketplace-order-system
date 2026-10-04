package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/marketplace-order-system/services/order/internal/adapters/httpapi"
	"github.com/example/marketplace-order-system/services/order/internal/adapters/memory"
	"github.com/example/marketplace-order-system/services/order/internal/application"
	"github.com/example/marketplace-order-system/services/order/internal/domain"
	"github.com/example/marketplace-order-system/services/order/internal/ports"
)

func TestCreateOrderHTTP(t *testing.T) {
	router := testRouter()
	body := []byte(`{"items":[{"product_id":"product-001","quantity":2}]}`)
	request := httptest.NewRequest(http.MethodPost, "/v1/orders", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Customer-ID", "customer-001")
	request.Header.Set("Idempotency-Key", "checkout-001")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	var payload struct {
		ID          string             `json:"id"`
		Status      domain.OrderStatus `json:"status"`
		TotalAmount int64              `json:"total_amount"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.ID == "" || payload.Status != domain.OrderStatusInventoryReserved || payload.TotalAmount != 400_000 {
		t.Fatalf("unexpected response: %+v", payload)
	}
}

func TestCreateOrderHTTPRequiresHeaders(t *testing.T) {
	router := testRouter()
	request := httptest.NewRequest(http.MethodPost, "/v1/orders", bytes.NewReader([]byte(`{"items":[]}`)))
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func testRouter() http.Handler {
	repository := memory.NewOrderRepository()
	products := memory.NewProductClient([]ports.ProductSnapshot{
		{
			ProductID: "product-001",
			SellerID:  "seller-001",
			Name:      "Mechanical Keyboard",
			UnitPrice: domain.Money(200_000),
			Active:    true,
		},
	})
	inventory := memory.NewInventoryClient(map[string]int{"product-001": 100})
	useCase := application.NewCreateOrder(
		repository,
		products,
		inventory,
		&testIDGenerator{},
		testClock{},
	)
	return httpapi.NewRouter(httpapi.NewOrderHandler(useCase), func(context.Context) error { return nil })
}

type testIDGenerator struct {
	next atomic.Int64
}

func (generator *testIDGenerator) NewID() (string, error) {
	return fmt.Sprintf("order-%d", generator.next.Add(1)), nil
}

type testClock struct{}

func (testClock) Now() time.Time {
	return time.Date(2026, time.October, 4, 10, 0, 0, 0, time.UTC)
}

