BUF_VERSION := v1.72.0
BUF := env GOWORK=off go run github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)

.PHONY: proto proto-format proto-lint proto-generate

proto: proto-format proto-lint proto-generate

proto-format:
	$(BUF) format -w

proto-lint:
	$(BUF) lint

proto-generate:
	$(BUF) generate
