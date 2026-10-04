# ADR-0002: Database per service

- Status: Accepted
- Date: 2026-10-03

## Decision

Each service owns a logical MongoDB database and credentials. A service must
not query another service's database directly.

## Consequences

- Data ownership and schema boundaries remain explicit.
- Services communicate through contracts rather than shared collections.
- Cross-service workflows require Saga-style coordination and compensation.

