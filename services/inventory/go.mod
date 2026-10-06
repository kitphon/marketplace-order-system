module github.com/kitphon/marketplace-order-system/services/inventory

go 1.25.0

require (
	github.com/kitphon/marketplace-order-system/contracts v0.0.0
	google.golang.org/grpc v1.64.0
	google.golang.org/protobuf v1.36.11
)

require (
	golang.org/x/net v0.56.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.39.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240318140521-94a12d6c2237 // indirect
)

replace github.com/kitphon/marketplace-order-system/contracts => ../../contracts
