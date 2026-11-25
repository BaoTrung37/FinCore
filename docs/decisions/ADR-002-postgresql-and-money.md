# ADR-002: PostgreSQL & Integer Minor-Unit Money Representation

- **Status:** Accepted
- **Date:** 2026-11-25

## Context

Financial systems cannot tolerate floating-point rounding drift (`float64`) or eventual-consistency anomalies in core balance/ledger writes.

## Decision

1. Use **PostgreSQL 17** as the primary transactional store (`pgxpool`, strict ACID transactions, row-level locking `SELECT ... FOR UPDATE`, unique constraints for idempotency).
2. Represent monetary amounts as **`int64` in minor units** (e.g., VND in đồng, USD in cents) paired with an explicit ISO-4217 `currency` code — never `float64`.

## Consequences

- **Pros:** Exact arithmetic, native DB check constraints (`amount > 0`), deterministic `SUM(credit) - SUM(debit)` verification.
- **Cons:** Currency-specific minor-unit scaling must be enforced at API/presentation boundaries (e.g., 100 cents = 1 USD vs 1 đồng = 1 VND).
