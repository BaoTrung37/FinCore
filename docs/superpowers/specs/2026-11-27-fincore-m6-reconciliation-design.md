# FinCore M6 — Reconciliation Engine Design

**Date:** 2026-11-27  
**Scope:** Phase 6 (`feat/m6-backend`) — Dual Reconciliation Engine (External Bank Statement Reconciliation + Internal Ledger Integrity Audit) on top of M1–M5.

## 1. Intent & Why

Financial systems require continuous verification that (a) external bank settlement records match FinCore's internal `payments` records, and (b) FinCore's double-entry `ledger_entries` remain balanced and consistent with cached `accounts.balance_amount` projections. M6 adds the final domain module defined in `ADR-001` (`internal/modules/reconciliation`), closing the core banking integrity loop and establishing the matching engine foundation for FinMatch.

## 2. Goals, Non-Goals & Anti-Goals

- **Goals:**
  - External bank statement reconciliation (`reconciliation_runs`, `reconciliation_items`) accepting both JSON payloads and CSV uploads (`multipart/form-data`).
  - Deterministic matching against user-owned `payments` by `external_ref` within a `(bank_code, currency, period_start..period_end)` window, classifying `matched`, `amount_mismatch`, `direction_mismatch`, `status_mismatch`, `missing_in_fincore`, and `missing_in_bank`.
  - Discrepancy resolution workflow (`open` → `resolved` | `ignored`) with mandatory `resolution_note`, `resolved_by`, and `resolved_at`.
  - Internal ledger integrity audit (`ledger_audit_runs`) verifying double-entry balance (`SUM(debit) == SUM(credit)`, `>= 2` entries, single currency per transaction) and zero balance drift (`accounts.balance_amount == SUM(credit - debit)`).
  - Transactional outbox notifications (`reconciliation.run_completed`, `reconciliation.ledger_audit_completed`) via the existing M5 `webhook.Event` publisher.
  - OpenAPI spec, `docs/erd.md`, `AGENTS.md`, and `README.md` updated to include M6.
- **Non-Goals:**
  - Automatic monetary adjustment / auto-reversal postings on discrepancy resolution (manual resolution records audit intent only; financial corrections continue to use explicit ledger reversals).
  - Fuzzy / ML matching without `external_ref`.
  - Background cron scheduling or SFTP bank file polling (on-demand API only in M6).
  - Web dashboard UI (explicitly out of scope for `feat/m6-backend`).
- **Anti-Goals:**
  - Mutating `payments`, `accounts`, or `ledger_entries` from the `reconciliation` module (strict module boundary: read-only queries on foreign tables, writes only to `reconciliation_*` and `ledger_audit_runs`).
  - Using `float64` anywhere in CSV parsing, amount comparisons, or audit calculations.

## 3. Constraints

- Go `1.27+`, PostgreSQL 17, `pgxpool`, `sqlc`, `golang-migrate/migrate/v4`, `chi/v5`, `slog`.
- Money always `int64` in minor units (`VND`, `USD`).
- Strict tenant scoping: users can only reconcile and inspect their own `payments`, `reconciliation_runs`, and `ledger_audit_runs`.
- All endpoints under `/api/v1/reconciliation` require JWT authentication (`auth.RequireAuth`).
- CSV upload bounded by `http.MaxBytesReader` (1 MiB) and parsed via Go stdlib `encoding/csv`.

## 4. Approaches Considered

1. **Approach A — Dual Reconciliation: External Bank Statement + Internal Ledger Audit *(Selected)***
   - Combines FinMatch-style external bank statement reconciliation (JSON + CSV) with an internal double-entry & balance-drift ledger audit.
   - *Trade-off:* 3 tables in migration `000006` and 7 HTTP endpoints, but completes both core banking auditability and FinMatch foundation in one cohesive module.
2. **Approach B — External Bank Statement Reconciliation Only**
   - Skips internal ledger balance-drift auditing.
   - *Trade-off:* Smaller surface area, leaves internal ledger integrity unchecked at the API layer.
3. **Approach C — Internal Ledger Audit Only**
   - Periodic/on-demand double-entry and balance-drift checks only.
   - *Trade-off:* Minimal code, but does not deliver external bank statement matching.

**Decision:** Approach A.

## 5. Architecture & Data Model

### 5.1 Database Migration (`migrations/000006_create_reconciliation_tables.up.sql`)

- **`reconciliation_runs`**: `id`, `user_id`, `bank_code`, `currency` (`VND`|`USD`), `period_start`, `period_end` (`>= period_start`), `status` (`completed` | `has_discrepancies`), `total_bank_records`, `total_internal_records`, `matched_count`, `discrepancy_count`, `created_at`.
- **`reconciliation_items`**: `id`, `run_id` (`ON DELETE CASCADE`), `external_ref`, `payment_id` (nullable FK `payments.id`), `match_status` (`matched`, `amount_mismatch`, `direction_mismatch`, `status_mismatch`, `missing_in_fincore`, `missing_in_bank`), `bank_amount` (`BIGINT NULL`), `internal_amount` (`BIGINT NULL`), `bank_direction`, `internal_direction`, `bank_status`, `internal_status`, `currency`, `resolution_status` (`not_applicable`, `open`, `resolved`, `ignored`), `resolution_note`, `resolved_by` (nullable FK `users.id`), `resolved_at` (`TIMESTAMPTZ NULL`), `created_at`.
- **`ledger_audit_runs`**: `id`, `user_id`, `status` (`healthy` | `anomalies_found`), `checked_transactions`, `unbalanced_transactions`, `checked_accounts`, `drifted_accounts`, `anomalies` (`JSONB`), `created_at`.

### 5.2 Matching Engine (`matcher.go` & `parser.go`)

- **Statement record validation:** `external_ref` non-empty & unique within a batch, `direction` in `{deposit, withdrawal}`, `amount > 0`, `currency` matches run currency (`VND` or `USD`), `status` in `{completed, failed, pending}`.
- **Matching priority:**
  1. Match by `external_ref` against user's `payments` filtered by `(user_id, bank_code, currency, created_at BETWEEN period_start AND period_end, external_ref IS NOT NULL)`.
  2. Missing in `payments` → `missing_in_fincore` (`resolution_status = 'open'`).
  3. Matched `external_ref`:
     - `bank.Amount != payment.Amount` → `amount_mismatch` (`open`)
     - `bank.Direction != payment.Direction` → `direction_mismatch` (`open`)
     - `bank.Status != payment.Status` → `status_mismatch` (`open`)
     - All equal → `matched` (`not_applicable`)
  4. Any internal payment in the window not seen in the bank statement → `missing_in_bank` (`open`).
- **CSV format:** Required header `external_ref,direction,amount,currency,status`. Amount parsed with `strconv.ParseInt(..., 10, 64)`.

### 5.3 Internal Ledger Integrity Audit (`service.go`)

- Checks across `ledger_transactions` + `ledger_entries`:
  - Any transaction where `COUNT(entries) < 2`, `COUNT(DISTINCT currency) > 1`, or `SUM(debit) != SUM(credit)`.
- Checks across `accounts` + `ledger_entries`:
  - Any account where `accounts.balance_amount != COALESCE(SUM(CASE WHEN entry_type = 'credit' THEN amount ELSE -amount END), 0)`.
- Persists summary and JSONB anomaly list in `ledger_audit_runs` and publishes `reconciliation.ledger_audit_completed` to outbox.

### 5.4 HTTP API (`/api/v1/reconciliation`)

| Method | Path | Purpose | Status |
|---|---|---|---|
| `POST` | `/api/v1/reconciliation/runs` | Run bank reconciliation from JSON payload | `201` |
| `POST` | `/api/v1/reconciliation/runs/csv` | Run bank reconciliation from CSV multipart upload | `201` |
| `GET` | `/api/v1/reconciliation/runs` | List user's reconciliation runs (`limit`, `offset`) | `200` |
| `GET` | `/api/v1/reconciliation/runs/{id}` | Get run summary + items (`?match_status=`, `?resolution_status=`) | `200` |
| `PATCH` | `/api/v1/reconciliation/runs/{id}/items/{item_id}` | Resolve or ignore an open discrepancy | `200` |
| `POST` | `/api/v1/reconciliation/ledger-audit` | Run internal ledger integrity audit | `201` |
| `GET` | `/api/v1/reconciliation/ledger-audit` | List past ledger audit runs (`limit`, `offset`) | `200` |

## 6. Verification Strategy

- **Unit tests:** Pure matcher tests (all 6 match statuses, duplicate `external_ref`, currency mismatch, empty batch), CSV parser tests (valid rows, bad headers, float/negative amounts, empty file), handler validation & error mapping (`httptest`).
- **Integration tests (`tests/integration/reconciliation_integration_test.go`):** Full PostgreSQL flow — create deposits/withdrawals + mock bank callbacks, run JSON and CSV reconciliation verifying matched + `amount_mismatch` + `missing_in_fincore` + `missing_in_bank`, resolve a discrepancy via `PATCH`, run `POST /api/v1/reconciliation/ledger-audit` verifying `healthy` status, and verify outbox events created.
