//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"reflect"
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

func TestInventoryRuntimePersistenceBeforeRestart(t *testing.T) {
	configuration := loadConfiguration()
	waitForNamedHealth(t, configuration.inventoryAddress, inventoryv1.InventoryService_ServiceDesc.ServiceName)
	database := openInventoryDatabase(t, configuration)
	assertInventoryIndexes(t, database)

	before := loadInventoryStock(t, database, "product-002")
	request := &inventoryv1.ReserveInventoryRequest{
		OrderId: uniqueID(configuration, "runtime-persistence"),
		Items: []*inventoryv1.ReservationItem{{
			ProductId: "product-002",
			Quantity:  2,
		}},
		ExpiresAt: timestamppb.New(time.Now().UTC().Add(30 * time.Minute).Truncate(time.Millisecond)),
	}
	client := inventoryClient(t, configuration)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, err := client.ReserveInventory(ctx, request)
	if err != nil {
		t.Fatalf("first runtime ReserveInventory() error = %v", err)
	}
	persisted := loadInventoryReservation(t, database, request.OrderId)
	if persisted.ID != first.ReservationId || persisted.Status != "RESERVED" || persisted.Version != 1 {
		t.Fatalf("persisted reservation = %+v, response ID = %q", persisted, first.ReservationId)
	}
	afterFirst := loadInventoryStock(t, database, "product-002")
	if afterFirst.Available != before.Available-2 || afterFirst.Version != before.Version+1 {
		t.Fatalf("stock after first reservation = %+v, before = %+v", afterFirst, before)
	}

	second, err := client.ReserveInventory(ctx, request)
	if err != nil {
		t.Fatalf("identical runtime ReserveInventory() error = %v", err)
	}
	if second.ReservationId != first.ReservationId || !second.ExpiresAt.AsTime().Equal(first.ExpiresAt.AsTime()) {
		t.Fatalf("identical retry = %+v, first = %+v", second, first)
	}
	assertInventoryStock(t, database, "product-002", afterFirst)
	assertInventoryReservationCount(t, database, request.OrderId, 1)

	conflict := &inventoryv1.ReserveInventoryRequest{
		OrderId: request.OrderId,
		Items: []*inventoryv1.ReservationItem{{
			ProductId: "product-002",
			Quantity:  3,
		}},
		ExpiresAt: request.ExpiresAt,
	}
	_, err = client.ReserveInventory(ctx, conflict)
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("runtime conflict code = %v, want AlreadyExists; error=%v", status.Code(err), err)
	}
	assertInventoryReason(t, err, inventoryv1.InventoryFailureReason_INVENTORY_FAILURE_REASON_RESERVATION_CONFLICT)
	assertInventoryStock(t, database, "product-002", afterFirst)
	afterConflict := loadInventoryReservation(t, database, request.OrderId)
	if !reflect.DeepEqual(afterConflict, persisted) {
		t.Fatalf("reservation changed after conflict: before=%+v after=%+v", persisted, afterConflict)
	}
}

func TestInventoryRuntimePersistenceAfterRestart(t *testing.T) {
	configuration := loadConfiguration()
	waitForNamedHealth(t, configuration.inventoryAddress, inventoryv1.InventoryService_ServiceDesc.ServiceName)
	database := openInventoryDatabase(t, configuration)
	persisted := loadInventoryReservation(t, database, uniqueID(configuration, "runtime-persistence"))
	stockBeforeRetry := loadInventoryStock(t, database, "product-002")
	if stockBeforeRetry.Available == 100 {
		t.Fatal("Inventory startup seed reset product-002 stock to 100")
	}

	request := &inventoryv1.ReserveInventoryRequest{
		OrderId: persisted.OrderID,
		Items: []*inventoryv1.ReservationItem{{
			ProductId: "product-002",
			Quantity:  2,
		}},
		ExpiresAt: timestamppb.New(time.Now().UTC().Add(time.Hour)),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := inventoryClient(t, configuration).ReserveInventory(ctx, request)
	if err != nil {
		t.Fatalf("post-restart ReserveInventory() error = %v", err)
	}
	if response.ReservationId != persisted.ID || !response.ExpiresAt.AsTime().Equal(persisted.ExpiresAt) {
		t.Fatalf("post-restart response = %+v, persisted = %+v", response, persisted)
	}
	assertInventoryStock(t, database, "product-002", stockBeforeRetry)
	assertInventoryReservationCount(t, database, persisted.OrderID, 1)
}

func TestInventoryStartupFailsWithoutMongoDB(t *testing.T) {
	binary := os.Getenv("M5_TEST_INVENTORY_BINARY")
	if binary == "" {
		t.Skip("M5_TEST_INVENTORY_BINARY is not set")
	}
	grpcAddress := unusedLocalAddress(t)
	mongodbAddress := unusedLocalAddress(t)
	for mongodbAddress == grpcAddress {
		mongodbAddress = unusedLocalAddress(t)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary)
	command.Env = append(os.Environ(),
		"INVENTORY_GRPC_ADDR="+grpcAddress,
		"MONGODB_URI=mongodb://"+mongodbAddress+"/?directConnection=true&serverSelectionTimeoutMS=500",
		"MONGODB_DATABASE=unreachable_inventory_test",
	)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatalf("start Inventory binary: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			if err == nil {
				t.Fatalf("Inventory exited successfully with unreachable MongoDB; output=%s", output.String())
			}
			if connection, dialErr := net.DialTimeout("tcp", grpcAddress, 50*time.Millisecond); dialErr == nil {
				_ = connection.Close()
				t.Fatalf("Inventory gRPC port became ready despite startup failure; output=%s", output.String())
			}
			return
		case <-ticker.C:
			connection, err := net.DialTimeout("tcp", grpcAddress, 20*time.Millisecond)
			if err == nil {
				_ = connection.Close()
				cancel()
				<-done
				t.Fatalf("Inventory gRPC port became ready before MongoDB initialization; output=%s", output.String())
			}
		case <-ctx.Done():
			<-done
			t.Fatalf("Inventory did not fail within timeout; output=%s", output.String())
		}
	}
}

type testConfiguration struct {
	productAddress    string
	inventoryAddress  string
	orderURL          string
	mongodbURI        string
	orderDatabase     string
	inventoryDatabase string
	runID             string
}

func loadConfiguration() testConfiguration {
	return testConfiguration{
		productAddress:    envOrDefault("M5_TEST_PRODUCT_ADDR", "127.0.0.1:55051"),
		inventoryAddress:  envOrDefault("M5_TEST_INVENTORY_ADDR", "127.0.0.1:55052"),
		orderURL:          strings.TrimRight(envOrDefault("M5_TEST_ORDER_URL", "http://127.0.0.1:58080"), "/"),
		mongodbURI:        envOrDefault("M5_TEST_MONGODB_URI", "mongodb://127.0.0.1:57017/?replicaSet=rs0&directConnection=true"),
		orderDatabase:     envOrDefault("M5_TEST_ORDER_MONGODB_DATABASE", envOrDefault("M5_TEST_MONGODB_DATABASE", "order_m5_test")),
		inventoryDatabase: envOrDefault("M5_TEST_INVENTORY_MONGODB_DATABASE", "inventory_m5_test"),
		runID:             envOrDefault("M5_TEST_RUN_ID", fmt.Sprintf("manual-%d", time.Now().UnixNano())),
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
	count, err := client.Database(configuration.orderDatabase).Collection("orders").CountDocuments(
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

type inventoryStockDocument struct {
	ProductID string    `bson:"_id"`
	Available int       `bson:"available"`
	Version   int64     `bson:"version"`
	CreatedAt time.Time `bson:"created_at"`
	UpdatedAt time.Time `bson:"updated_at"`
}

type inventoryReservationItemDocument struct {
	ProductID string `bson:"product_id"`
	Quantity  int    `bson:"quantity"`
}

type inventoryReservationDocument struct {
	ID        string                             `bson:"_id"`
	OrderID   string                             `bson:"order_id"`
	Items     []inventoryReservationItemDocument `bson:"items"`
	Status    string                             `bson:"status"`
	ExpiresAt time.Time                          `bson:"expires_at"`
	Version   int64                              `bson:"version"`
	CreatedAt time.Time                          `bson:"created_at"`
	UpdatedAt time.Time                          `bson:"updated_at"`
}

func openInventoryDatabase(t *testing.T, configuration testConfiguration) *mongo.Database {
	t.Helper()
	client, err := mongo.Connect(options.Client().ApplyURI(configuration.mongodbURI))
	if err != nil {
		t.Fatalf("connect to Inventory MongoDB: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := client.Disconnect(ctx); err != nil {
			t.Errorf("disconnect Inventory MongoDB client: %v", err)
		}
	})
	return client.Database(configuration.inventoryDatabase)
}

func loadInventoryStock(
	t *testing.T,
	database *mongo.Database,
	productID string,
) inventoryStockDocument {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var document inventoryStockDocument
	if err := database.Collection("stocks").FindOne(
		ctx,
		bson.D{{Key: "_id", Value: productID}},
	).Decode(&document); err != nil {
		t.Fatalf("load Inventory stock %q: %v", productID, err)
	}
	return document
}

func assertInventoryStock(
	t *testing.T,
	database *mongo.Database,
	productID string,
	want inventoryStockDocument,
) {
	t.Helper()
	if got := loadInventoryStock(t, database, productID); got != want {
		t.Fatalf("Inventory stock %q = %+v, want %+v", productID, got, want)
	}
}

func loadInventoryReservation(
	t *testing.T,
	database *mongo.Database,
	orderID string,
) inventoryReservationDocument {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var document inventoryReservationDocument
	if err := database.Collection("reservations").FindOne(
		ctx,
		bson.D{{Key: "order_id", Value: orderID}},
	).Decode(&document); err != nil {
		t.Fatalf("load Inventory reservation for order %q: %v", orderID, err)
	}
	return document
}

func assertInventoryReservationCount(
	t *testing.T,
	database *mongo.Database,
	orderID string,
	want int64,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	count, err := database.Collection("reservations").CountDocuments(
		ctx,
		bson.D{{Key: "order_id", Value: orderID}},
	)
	if err != nil {
		t.Fatalf("count Inventory reservations: %v", err)
	}
	if count != want {
		t.Fatalf("Inventory reservation count for order %q = %d, want %d", orderID, count, want)
	}
}

func assertInventoryIndexes(t *testing.T, database *mongo.Database) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cursor, err := database.Collection("reservations").Indexes().List(ctx)
	if err != nil {
		t.Fatalf("list Inventory reservation indexes: %v", err)
	}
	defer cursor.Close(context.Background())
	found := map[string]bool{}
	for cursor.Next(ctx) {
		var index struct {
			Name   string `bson:"name"`
			Unique bool   `bson:"unique"`
		}
		if err := cursor.Decode(&index); err != nil {
			t.Fatalf("decode Inventory reservation index: %v", err)
		}
		if index.Name == "uq_reservations_order_id" && !index.Unique {
			t.Fatalf("Inventory index %q is not unique", index.Name)
		}
		found[index.Name] = true
	}
	if err := cursor.Err(); err != nil {
		t.Fatalf("iterate Inventory reservation indexes: %v", err)
	}
	for _, name := range []string{"uq_reservations_order_id", "ix_reservations_status_expires_at"} {
		if !found[name] {
			t.Fatalf("Inventory index %q not found", name)
		}
	}
}

func inventoryClient(t *testing.T, configuration testConfiguration) inventoryv1.InventoryServiceClient {
	t.Helper()
	return inventoryv1.NewInventoryServiceClient(newGRPCConnection(t, configuration.inventoryAddress))
}

func unusedLocalAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate local address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release local address: %v", err)
	}
	return address
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
