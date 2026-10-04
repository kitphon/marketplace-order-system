# Repository instructions

- Preserve Clean Architecture boundaries.
- Domain and application packages must not import HTTP, MongoDB, Kafka, Redis, or gRPC implementations.
- Infrastructure adapters must implement interfaces declared in `internal/ports`.
- Represent currency in minor units using `int64`; do not use floating point for money.
- Preserve request idempotency and optimistic concurrency behavior.
- Add or update tests for every behavior change.
- Before completing a task, run Go formatting, `go vet ./...`, `go test ./...`, and `go test -race ./...` from each affected Go module.
- Never claim a test passed unless it was actually executed successfully.
- Update `docs/PROJECT_STATE.md` whenever a milestone changes.
