# Paper Trading Mode Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a production-usable virtual trading mode (`paper`) with real orderbook inputs, simulated execution, and account telemetry for safe strategy validation.

**Architecture:** Introduce a dedicated `internal/paper` simulator that consumes top-of-book prices, applies slippage/fees, and produces synthetic order/fill events. Wire this simulator into `internal/app` order placement paths when `trading_mode=paper`, while reusing existing risk checks, tracker/PnL logic, and dashboard API. Keep live mode unchanged.

**Tech Stack:** Go 1.25, existing `internal/app`, `internal/execution`, `internal/api`, `internal/config`, SDK WebSocket orderbook events.

---

### Task 1: Add Paper Simulator Package (TDD)

**Files:**
- Create: `internal/paper/simulator.go`
- Create: `internal/paper/simulator_test.go`

**Step 1: Write failing tests**
- `TestExecuteMarketBuyDeductsBalanceAndFees`
- `TestExecuteLimitOnlyFillsWhenCrossed`
- `TestExecuteMarketRejectsInsufficientBalance`

**Step 2: Run tests to verify RED**
- Run: `go test ./internal/paper -count=1`
- Expect: compile failures (package missing) or failing assertions.

**Step 3: Implement minimal simulator**
- Add config with initial balance/slippage/fee.
- Add `ExecuteMarket` and `ExecuteLimit`.
- Return deterministic synthetic order/trade IDs and account snapshot.

**Step 4: Re-run tests to verify GREEN**
- Run same command; expect PASS.

---

### Task 2: Config Model + Defaults (TDD)

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `config.yaml`

**Step 1: Write failing tests**
- Add assertions for `trading_mode` default and `paper` config defaults.
- Extend YAML parse test to include `trading_mode` and `paper.initial_balance_usdc`.

**Step 2: Run config tests (RED)**
- Run: `go test ./internal/config -run 'Test(LoadDefaults|LoadFromYAML)' -count=1`

**Step 3: Implement config changes**
- Add `TradingMode` and `PaperConfig`.
- Set default mode to `paper` and default initial balance to `1000`.
- Reflect in sample `config.yaml`.

**Step 4: Re-run config tests (GREEN)**
- Run same command; expect PASS.

---

### Task 3: App Integration for Paper Mode (TDD)

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`

**Step 1: Write failing tests**
- Add test proving `HandleBookEvent` in `paper` mode (non-dry-run) creates synthetic fills and updates paper balance.

**Step 2: Run app test (RED)**
- Run: `go test ./internal/app -run TestHandleBookEventPaperMode -count=1`

**Step 3: Implement minimal app wiring**
- Initialize simulator when `cfg.TradingMode=="paper"`.
- Branch `placeMarket`/`placeLimit` to simulator.
- Feed synthetic order/trade events into tracker.
- Avoid live cancel API calls in paper mode.
- Expose `TradingMode()` + `PaperSnapshot()` methods.

**Step 4: Re-run app test (GREEN)**
- Run same command; expect PASS.

---

### Task 4: API Exposure + Tests (TDD)

**Files:**
- Modify: `internal/api/server.go`
- Modify: `internal/api/server_test.go`

**Step 1: Write failing tests**
- Add `TestHandlePaper` to validate mode/balance response.

**Step 2: Run API tests (RED)**
- Run: `go test ./internal/api -run TestHandlePaper -count=1`

**Step 3: Implement API changes**
- Extend `AppState` with `TradingMode()` and `PaperSnapshot()`.
- Add `GET /api/paper`.
- Include `trading_mode` in `/api/status`.

**Step 4: Re-run API tests (GREEN)**
- Run same command; expect PASS.

---

### Task 5: Docs + Full Verification

**Files:**
- Modify: `README.md`

**Step 1: Update docs**
- Explain `trading_mode` values and paper parameters.
- Document `GET /api/paper`.

**Step 2: Run full suite**
- Run: `go test ./... -count=1`
- Expect: all packages pass.
