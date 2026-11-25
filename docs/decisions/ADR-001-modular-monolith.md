# ADR-001: Modular Monolith Architecture

- **Status:** Accepted
- **Date:** 2026-11-25

## Context

FinCore needs clear domain boundaries (`auth`, `account`, `ledger`, `transfer`, `payment`, `reconciliation`, `webhook`) while guaranteeing ACID consistency across ledger entries, balance projections, and outbox events.

## Decision

Start with a **Modular Monolith** (`internal/modules/<domain>` + `internal/platform/*`) backed by a single PostgreSQL database, rather than splitting into microservices upfront.

## Consequences

- **Pros:** Cross-module operations (e.g., transfer + double-entry ledger + outbox insert) execute inside a single PostgreSQL transaction without distributed transactions (2PC/Saga). Simpler local dev, testing, and deployment.
- **Cons:** Requires strict discipline on module boundaries (no cross-module direct table writes outside defined service/tx interfaces) so modules remain extractable later if scale demands.
