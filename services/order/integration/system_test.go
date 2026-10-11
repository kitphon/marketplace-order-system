//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	inventoryv1 "github.com/kitphon/marketplace-order-system/contracts/inventory/v1"
	productv1 "github.com/kitphon/marketplace-order-system/contracts/product/v1"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const readinessTimeout = 30 * time.Second

func TestIntegration(t *testing.T) {
	configuration := loadConfiguration()
	waitForStack(t, configuration)

	t.Run("named gRPC health", func(t *testing.T) {
		assertNamedHealth(t, configuration.productAddress, productv1.ProductService_ServiceDesc.ServiceName)
		assertNamedHealth(t, configuration.inventoryAddress, inventoryv1.InventoryService_ServiceDesc.ServiceName)
	})

	t.Run("Product gRPC contract", func(t *testing.T) {
		connection := newGRPCConnection(t, configuration.productAddress)
		client := productv1.NewProductServiceClient(connection)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		response, err := client.GetProducts(ctx, &productv1.GetProductsRequest{
			ProductIds: []string{"product-001", "missing-product"},
		})
		if err != nil {
			t.Fatalf("GetProducts() error = %v", err)
		}
		if len(response.Products) != 1 {
			t.Fatalf("products count = %d, want 1", len(response.Products))
		}
		product := response.Products[0]
		if product.ProductId != "product-001" ||
			product.SellerId != "seller-001" ||
			product.Name != "Mechanical Keyboard" ||
			product.UnitPriceMinor != 200_000 ||
			product.Currency != "THB" ||
			!product.Active {
			t.Fatalf("product = %+v", product)
		}
	})

	t.Run("Inventory idempotent reservation", func(t *testing.T) {
		connection := newGRPCConnection(t, configuration.inventoryAddress)
		client := inventoryv1.NewInventoryServiceClient(connection)
		expiresAt := time.Now().UTC().Add(10 * time.Minute).Truncate(time.Millisecond)
		request := &inventoryv1.ReserveInventoryRequest{
			OrderId: uniqueID(configuration, "inventory"),
			Items: []*inventoryv1.ReservationItem{{
				ProductId: "product-002",
				Quantity:  1,
			}},
			ExpiresAt: timestamppb.New(expiresAt),
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		first, err := client.ReserveInventory(ctx, request)
		if err != nil {
			t.Fatalf("first ReserveInventory() error = %v", err)
		}
		second, err := client.ReserveInventory(ctx, request)
		if err != nil {
			t.Fatalf("second ReserveInventory() error = %v", err)
		}
		if first.Status != inventoryv1.ReservationStatus_RESERVATION_STATUS_RESERVED ||
			second.Status != inventoryv1.ReservationStatus_RESERVATION_STATUS_RESERVED {
			t.Fatalf("reservation statuses = (%v, %v)", first.Status, second.Status)
		}
		if first.ReservationId == "" || second.ReservationId != first.ReservationId {
			t.Fatalf("reservation IDs = (%q, %q)", first.ReservationId, second.ReservationId)
		}
		if !first.ExpiresAt.AsTime().Equal(expiresAt) || !second.ExpiresAt.AsTime().Equal(expiresAt) {
			t.Fatalf("expirations = (%v, %v), want %v", first.ExpiresAt, second.ExpiresAt, expiresAt)
		}

		conflict := &inventoryv1.ReserveInventoryRequest{
			OrderId: request.OrderId,
			Items: []*inventoryv1.ReservationItem{{
				ProductId: "product-002",
				Quantity:  2,
			}},
			ExpiresAt: request.ExpiresAt,
		}
		_, err = client.ReserveInventory(ctx, conflict)
		if status.Code(err) != codes.AlreadyExists {
			t.Fatalf("conflict code = %v, want AlreadyExists; error=%v", status.Code(err), err)
		}
		assertInventoryReason(t, err, inventoryv1.InventoryFailureReason_INVENTORY_FAILURE_REASON_RESERVATION_CONFLICT)
	})

	t.Run("full Create Order and HTTP idempotency", func(t *testing.T) {
		customerID := uniqueID(configuration, "customer")
		idempotencyKey := uniqueID(configuration, "checkout")
		payload := `{"items":[{"product_id":"product-001","quantity":2}]}`
		firstStatus, firstLocation, first := createOrder(t, configuration, customerID, idempotencyKey, payload)
		assertCreatedOrder(t, firstStatus, firstLocation, first, 400_000)

		secondStatus, secondLocation, second := createOrder(t, configuration, customerID, idempotencyKey, payload)
		assertCreatedOrder(t, secondStatus, secondLocation, second, 400_000)
		if second.ID != first.ID {
			t.Fatalf("idempotent retry ID = %q, want %q", second.ID, first.ID)
		}
		assertSinglePersistedOrder(t, configuration, customerID, idempotencyKey)

		conflictPayload := `{"items":[{"product_id":"product-001","quantity":3}]}`
		statusCode, _, body := postOrder(t, configuration, customerID, idempotencyKey, conflictPayload)
		if statusCode != http.StatusConflict {
			t.Fatalf("conflict status = %d, want 409; body=%s", statusCode, body)
		}
		assertErrorCode(t, body, "IDEMPOTENCY_KEY_REUSED")
	})

	t.Run("product not found", func(t *testing.T) {
		statusCode, _, body := postOrder(
			t,
			configuration,
			uniqueID(configuration, "missing-customer"),
			uniqueID(configuration, "missing-checkout"),
			`{"items":[{"product_id":"missing-product","quantity":1}]}`,
		)
		if statusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", statusCode, body)
		}
		assertErrorCode(t, body, "PRODUCT_NOT_FOUND")
	})

	t.Run("insufficient stock", func(t *testing.T) {
		statusCode, _, body := postOrder(
			t,
			configuration,
			uniqueID(configuration, "stock-customer"),
			uniqueID(configuration, "stock-checkout"),
			`{"items":[{"product_id":"product-002","quantity":1000}]}`,
		)
		if statusCode != http.StatusConflict {
			t.Fatalf("status = %d, want 409; body=%s", statusCode, body)
		}
		assertErrorCode(t, body, "INSUFFICIENT_STOCK")
	})
}

func TestSmoke(t *testing.T) {
	configuration := loadConfiguration()
	waitForStack(t, configuration)
	customerID := uniqueID(configuration, "smoke-customer")
	idempotencyKey := uniqueID(configuration, "smoke-checkout")
	payload := `{"items":[{"product_id":"product-001","quantity":1}]}`

	firstStatus, firstLocation, first := createOrder(t, configuration, customerID, idempotencyKey, payload)
	assertCreatedOrder(t, firstStatus, firstLocation, first, 200_000)
	secondStatus, secondLocation, second := createOrder(t, configuration, customerID, idempotencyKey, payload)
	assertCreatedOrder(t, secondStatus, secondLocation, second, 200_000)
	if second.ID != first.ID {
		t.Fatalf("idempotent smoke retry ID = %q, want %q", second.ID, first.ID)
	}
}

type testConfiguration struct {
	productAddress   string
	inventoryAddress string
	orderURL         string
	mongodbURI       string
	mongodbDatabase  string
	runID            string
}

func loadConfiguration() testConfiguration {
	return testConfiguration{
		productAddress:   envOrDefault("M5_TEST_PRODUCT_ADDR", "127.0.0.1:55051"),
		inventoryAddress: envOrDefault("M5_TEST_INVENTORY_ADDR", "127.0.0.1:55052"),
		orderURL:         strings.TrimRight(envOrDefault("M5_TEST_ORDER_URL", "http://127.0.0.1:58080"), "/"),
		mongodbURI:       envOrDefault("M5_TEST_MONGODB_URI", "mongodb://127.0.0.1:57017/?replicaSet=rs0&directConnection=true"),
		mongodbDatabase:  envOrDefault("M5_TEST_MONGODB_DATABASE", "order_m5_test"),
		runID:            envOrDefault("M5_TEST_RUN_ID", fmt.Sprintf("manual-%d", time.Now().UnixNano())),
	}
}

func assertSinglePersistedOrder(
	t *testing.T,
	configuration testConfiguration,
	customerID string,
	idempotencyKey string,
) {
	t.Helper()
	client, err := mongo.Connect(options.Client().ApplyURI(configuration.mongodbURI))
	if err != nil {
		t.Fatalf("connect to MongoDB: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := client.Disconnect(ctx); err != nil {
			t.Errorf("disconnect MongoDB client: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	count, err := client.Database(configuration.mongodbDatabase).Collection("orders").CountDocuments(
		ctx,
		bson.D{
			{Key: "customer_id", Value: customerID},
			{Key: "idempotency_key", Value: idempotencyKey},
		},
	)
	if err != nil {
		t.Fatalf("count persisted idempotent orders: %v", err)
	}
	if count != 1 {
		t.Fatalf("persisted logical order count = %d, want 1", count)
	}
}

func waitForStack(t *testing.T, configuration testConfiguration) {
	t.Helper()
	waitForNamedHealth(t, configuration.productAddress, productv1.ProductService_ServiceDesc.ServiceName)
	waitForNamedHealth(t, configuration.inventoryAddress, inventoryv1.InventoryService_ServiceDesc.ServiceName)
	waitForHTTPHealth(t, configuration.orderURL+"/health/live", "UP")
	waitForHTTPHealth(t, configuration.orderURL+"/health/ready", "READY")
}

func waitForNamedHealth(t *testing.T, address, service string) {
	t.Helper()
	connection := newGRPCConnection(t, address)
	client := healthpb.NewHealthClient(connection)
	deadline := time.Now().Add(readinessTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		response, err := client.Check(ctx, &healthpb.HealthCheckRequest{Service: service})
		cancel()
		if err == nil && response.Status == healthpb.HealthCheckResponse_SERVING {
			return
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("health status is %s", response.Status)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("service %s at %s did not become SERVING: %v", service, address, lastErr)
}

func waitForHTTPHealth(t *testing.T, url, wantStatus string) {
	t.Helper()
	deadline := time.Now().Add(readinessTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			cancel()
			t.Fatalf("NewRequestWithContext() error = %v", err)
		}
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			var payload struct {
				Status string `json:"status"`
			}
			decodeErr := json.NewDecoder(response.Body).Decode(&payload)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK && decodeErr == nil && payload.Status == wantStatus {
				cancel()
				return
			}
			lastErr = fmt.Errorf("HTTP %d, status %q, decode error %v", response.StatusCode, payload.Status, decodeErr)
		} else {
			lastErr = err
		}
		cancel()
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("health endpoint %s did not become %s: %v", url, wantStatus, lastErr)
}

func assertNamedHealth(t *testing.T, address, service string) {
	t.Helper()
	connection := newGRPCConnection(t, address)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, err := healthpb.NewHealthClient(connection).Check(ctx, &healthpb.HealthCheckRequest{Service: service})
	if err != nil {
		t.Fatalf("health Check(%s) error = %v", service, err)
	}
	if response.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("health Check(%s) = %v, want SERVING", service, response.Status)
	}
}

func newGRPCConnection(t *testing.T, address string) *grpc.ClientConn {
	t.Helper()
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient(%s) error = %v", address, err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close gRPC connection: %v", err)
		}
	})
	return connection
}

func assertInventoryReason(t *testing.T, err error, want inventoryv1.InventoryFailureReason) {
	t.Helper()
	for _, detail := range status.Convert(err).Details() {
		inventoryDetail, ok := detail.(*inventoryv1.InventoryErrorDetail)
		if ok && inventoryDetail.Reason == want {
			return
		}
	}
	t.Fatalf("error details do not contain Inventory reason %v: %v", want, status.Convert(err).Details())
}

type orderResponse struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	TotalAmount int64  `json:"total_amount"`
	Currency    string `json:"currency"`
	Version     int64  `json:"version"`
}

func createOrder(
	t *testing.T,
	configuration testConfiguration,
	customerID string,
	idempotencyKey string,
	payload string,
) (int, string, orderResponse) {
	t.Helper()
	statusCode, location, body := postOrder(t, configuration, customerID, idempotencyKey, payload)
	var order orderResponse
	if err := json.Unmarshal(body, &order); err != nil {
		t.Fatalf("decode order response: %v; body=%s", err, body)
	}
	return statusCode, location, order
}

func postOrder(
	t *testing.T,
	configuration testConfiguration,
	customerID string,
	idempotencyKey string,
	payload string,
) (int, string, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		configuration.orderURL+"/v1/orders",
		bytes.NewBufferString(payload),
	)
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Customer-ID", customerID)
	request.Header.Set("Idempotency-Key", idempotencyKey)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST /v1/orders error = %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read order response: %v", err)
	}
	return response.StatusCode, response.Header.Get("Location"), body
}

func assertCreatedOrder(t *testing.T, statusCode int, location string, order orderResponse, wantTotal int64) {
	t.Helper()
	if statusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201; order=%+v", statusCode, order)
	}
	if order.ID == "" || location != "/v1/orders/"+order.ID {
		t.Fatalf("order ID = %q, Location = %q", order.ID, location)
	}
	if order.Status != "INVENTORY_RESERVED" || order.Currency != "THB" ||
		order.TotalAmount != wantTotal || order.Version != 2 {
		t.Fatalf("order = %+v, want INVENTORY_RESERVED THB total=%d version=2", order, wantTotal)
	}
}

func assertErrorCode(t *testing.T, body []byte, want string) {
	t.Helper()
	var response struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode error response: %v; body=%s", err, body)
	}
	if response.Code != want {
		t.Fatalf("error code = %q, want %q; body=%s", response.Code, want, body)
	}
}

func uniqueID(configuration testConfiguration, suffix string) string {
	return configuration.runID + "-" + suffix
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
