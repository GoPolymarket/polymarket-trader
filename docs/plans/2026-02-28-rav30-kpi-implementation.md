# RAV30 KPI Endpoint Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Deliver a canonical `/api/kpi` endpoint with RAV30 and the 16 funnel/risk/execution/builder metrics, backed by runtime instrumentation in the app.

**Architecture:** Add an in-memory KPI collector inside `internal/app` to capture signal/order/risk/quality counters and daily UTC aggregates. Expose collector snapshots via a new `KPIStats()` App method and AppState interface. Compute RAV30 and normalized factors in the API layer using `KPIStats + /api/perf + /api/risk + /api/execution-quality + /api/builder` equivalent inputs.

**Tech Stack:** Go 1.25, net/http handlers, existing app tracker/risk manager, Go testing, golangci-lint.

---

### Task 1: API contract tests first (`/api/kpi`)

**Files:**
- Modify: `internal/api/server_test.go`
- Modify: `internal/api/server.go` (interface additions only if required for compile)

**Step 1: Write failing tests**
- Add `TestHandleKPI` that asserts:
  - endpoint exists and returns `200`.
  - `north_star.rav30` equals `net_pnl_30d * risk_compliance_30d * exec_quality_factor_30d * builder_factor_30d`.
  - process metrics include keys 2-16 from spec.
  - `risk_block_events_daily_by_reason` exists and includes reason dimensions.
  - PnL includes `realized_only` and `total` columns.

**Step 2: Run test to verify RED**
- Run: `go test ./internal/api -run TestHandleKPI -count=1`
- Expected: FAIL (missing endpoint / interface method).

---

### Task 2: App KPI collector and instrumentation

**Files:**
- Create: `internal/app/kpi_metrics.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`

**Step 1: Write failing app test(s)**
- Add tests for collector behavior (risk block reason counting, submitted/filled count, emergency duration accumulation, signal count).

**Step 2: Run test to verify RED**
- Run: `go test ./internal/app -run TestKPI -count=1`
- Expected: FAIL.

**Step 3: Implement minimal collector**
- Add collector struct with mutex and UTC day reset handling.
- Add methods to record:
  - maker/taker signals
  - submitted orders / filled orders
  - risk block reason events
  - cooldown trigger events
  - emergency stop active duration
  - maker spread capture sample bps
  - taker realization events
  - risk compliance samples (can_trade ratio)
  - net pnl samples + PnL columns (realized/total)
- Wire hooks in `app.go`:
  - `HandleBookEvent` signal points + risk block reasons.
  - `placeLimit`/`placeMarket` successful submit.
  - `tracker.OnFill` fill count.
  - `riskSync` cooldown/emergency/can-trade/net-pnl sampling.
  - `SetEmergencyStop` state change hooks.

**Step 4: Re-run app tests**
- Run: `go test ./internal/app -run TestKPI -count=1`
- Expected: PASS.

---

### Task 3: API endpoint wiring and formula

**Files:**
- Modify: `internal/api/server.go`
- Modify: `internal/api/server_test.go`

**Step 1: Extend AppState**
- Add `KPIStats() map[string]interface{}` to `AppState`.
- Implement in app and mock.

**Step 2: Add endpoint**
- Register `/api/kpi` in `NewServer`.
- Implement `handleKPI`:
  - compute `RAV30`.
  - output process metrics 2-16.
  - include hygiene metadata (`utc`, paper/live split, pnl columns).

**Step 3: Re-run API tests**
- Run: `go test ./internal/api -run TestHandleKPI -count=1`
- Expected: PASS.

---

### Task 4: Documentation + full verification

**Files:**
- Modify: `README.md`

**Step 1: Document endpoint**
- Add `/api/kpi` with field overview and RAV30 formula.

**Step 2: Full verification**
- Run: `go test ./... -count=1`
- Run: `golangci-lint run`
- Expected: all pass.

**Step 3: Commit (optional, if requested)**
- `git add ...`
- `git commit -m "feat: add rav30 kpi endpoint and runtime metric instrumentation"`
