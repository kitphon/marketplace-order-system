# M3: MongoDB Order repository

## Guarantees implemented

- A unique compound index enforces `(customer_id, idempotency_key)`.
- Repository Create classifies an idempotency conflict separately from an
  `_id` collision.
- Update uses `_id + expected version` as an atomic compare-and-set filter.
- A failed match is classified as either not found or version conflict.
- Persistence documents are mapped to validated domain aggregates rather than
  decoding directly into private domain fields.

## Indexes

- `uq_customer_idempotency_key` enforces request idempotency.
- `ix_status_updated_at` prepares for stale-PENDING reconciliation scans.

## Run the integration test

```bash
docker compose -f deploy/docker-compose.yml up -d

cd services/order
go mod tidy
MONGODB_URI='mongodb://localhost:27017/?replicaSet=rs0&directConnection=true' \
  go test -tags=integration ./internal/adapters/mongodb/...
```

The repository solves durable persistence and lost updates. It intentionally
does not solve the cross-service PENDING/RESERVED recovery problem yet.

