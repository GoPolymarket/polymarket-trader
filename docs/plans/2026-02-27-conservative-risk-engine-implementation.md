# Conservative Risk Engine Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add risk-first conservative automation controls (2% daily loss guardrail, consecutive-loss cooldown, risk telemetry API) for small-capital users.

**Architecture:** Extend the existing `internal/risk.Manager` with derived daily loss limits and loss-streak cooldown logic, then wire state transitions in `internal/app` periodic risk sync. Surface risk state through a new API endpoint so clients can monitor guardrails in real time.

**Tech Stack:** Go 1.25, stdlib HTTP, existing polymarket-trader app/risk/api packages, Go test.

---

### Task 1: Risk Manager Behavior (TDD)

**Files:**
- Modify: `internal/risk/manager_test.go`
- Modify: `internal/risk/manager.go`

**Step 1: Write failing tests**
- Add tests for:
  - Daily loss limit derived from `account_capital_usdc * max_daily_loss_pct`.
  - Consecutive losing trade deltas triggering cooldown.
  - Positive delta resetting loss streak.

**Step 2: Run targeted tests and verify failure**
- Run: `go test ./internal/risk -run 'Test(DailyLossLimitFromCapitalPct|ConsecutiveLossCooldown|ConsecutiveLossResetOnProfit)' -count=1`
- Expected: compile/test failure for missing config fields/methods.

**Step 3: Implement minimal production code**
- Add new config fields/state in manager.
- Add `RecordTradeResult`, `DailyLossLimitUSDC`, cooldown checks, and snapshot helpers.

**Step 4: Run targeted tests and verify pass**
- Run the same command; expect PASS.

---

### Task 2: App Integration for Realized-PnL Delta Tracking (TDD)

**Files:**
- Modify: `internal/app/app_test.go`
- Modify: `internal/app/app.go`

**Step 1: Write failing tests**
- Add test proving `riskSync` tracks realized PnL deltas and triggers cooldown state after configured consecutive losses.

**Step 2: Run targeted tests and verify failure**
- Run: `go test ./internal/app -run TestRiskSyncTracksRealizedDeltas -count=1`
- Expected: failure due to missing app/risk integration surface.

**Step 3: Implement minimal production code**
- Track previous realized PnL in `App`.
- Feed deltas into risk manager.
- Use configured account capital for drawdown checks.

**Step 4: Re-run targeted tests**
- Expect PASS.

---

### Task 3: API Risk Telemetry Endpoint (TDD)

**Files:**
- Modify: `internal/api/server_test.go`
- Modify: `internal/api/server.go`

**Step 1: Write failing tests**
- Add coverage for `GET /api/risk` returning daily limit usage, cooldown state, and consecutive loss streak.

**Step 2: Run targeted tests and verify failure**
- Run: `go test ./internal/api -run TestHandleRisk -count=1`

**Step 3: Implement minimal production code**
- Expand `AppState` interface and API routes.
- Add handler to return structured risk status payload.

**Step 4: Re-run targeted tests**
- Expect PASS.

---

### Task 4: Config/Docs Alignment + Verification

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `config.yaml`
- Modify: `README.md`

**Step 1: Add/verify config fields**
- Add conservative-mode defaults and YAML tags for new risk controls.

**Step 2: Add tests for defaults/yaml parsing**
- Ensure new fields parse from YAML and default sanely.

**Step 3: Run full verification**
- Run: `go test ./... -count=1`
- Expected: all packages pass.

**Step 4: Final sanity**
- Confirm docs mention new settings and API endpoint.
