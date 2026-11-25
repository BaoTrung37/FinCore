# FinCore M3 — Transfer Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build `internal/modules/transfer` (`POST /api/v1/transfers`, `GET /api/v1/transfers/{id}`), add `GET /api/v1/accounts/{id}/transactions`, and verify idempotency + 100-concurrent-transfer safety.

**Spec:** [2026-11-25-fincore-m3-transfer-engine-design.md](file:///Users/trungnguyen/Work/GoLang/FinCore/docs/superpowers/specs/2026-11-25-fincore-m3-transfer-engine-design.md)

## Tasks

1. **Migration `000003` & `sqlc`:** Add `transfers` table with `UNIQUE (user_id, idempotency_key)`, update `sqlc.yaml` & `AGENTS.md`, generate `internal/modules/transfer/db`.
2. **Expose `ledger.Service.PostInTx` & `ListAccountEntries`:** Allow `transfer.Service` to execute `ledger` posting and `transfers` insert in the *same* `pgx.Tx` (true single-transaction atomicity!), plus paginated `ListEntriesByAccountID`.
3. **Implement `transfer.Service` & `transfer.Handler`:** SHA-256 `request_hash`, idempotency replay vs `409` conflict, ownership & self-transfer checks, unit tests.
4. **Wire M3 routes & Integration + Concurrency Tests:** Wire `transfer` + `account/{id}/transactions` in `cmd/api/main.go`, add `tests/integration/transfer_integration_test.go` (idempotency replay, payload mismatch `409`, 100 concurrent transfers).
