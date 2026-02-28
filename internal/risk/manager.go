package risk

import (
	"fmt"
	"sync"
	"time"

	"github.com/GoPolymarket/polymarket-trader/internal/execution"
)

type Config struct {
	MaxOpenOrders           int
	MaxDailyLossUSDC        float64
	MaxDailyLossPct         float64 // percentage loss cap derived from account capital (0.02 = 2%)
	AccountCapitalUSDC      float64 // baseline capital for percentage-based limits
	MaxPositionPerMarket    float64
	StopLossPerMarket       float64 // max loss per market before unwind
	MaxDrawdownPct          float64 // max total drawdown as fraction of daily start
	RiskSyncInterval        time.Duration
	MaxConsecutiveLosses    int
	ConsecutiveLossCooldown time.Duration
}

type Snapshot struct {
	EmergencyStop        bool
	DailyPnL             float64
	DailyLossLimitUSDC   float64
	ConsecutiveLosses    int
	InCooldown           bool
	CooldownRemaining    time.Duration
	MaxConsecutiveLosses int
}

type Manager struct {
	mu                sync.RWMutex
	cfg               Config
	openOrders        int
	dailyPnL          float64
	positions         map[string]float64 // tokenID → USDC exposure
	emergencyStop     bool
	dailyStartPnL     float64 // PnL at start of day for drawdown calc
	consecutiveLosses int
	cooldownUntil     time.Time
}

func New(cfg Config) *Manager {
	return &Manager{
		cfg:       cfg,
		positions: make(map[string]float64),
	}
}

func (m *Manager) Allow(tokenID string, amountUSDC float64) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.emergencyStop {
		return fmt.Errorf("emergency stop active")
	}
	if m.inCooldownLocked() {
		return fmt.Errorf("loss cooldown active: %.0fs remaining", m.cooldownUntil.Sub(time.Now()).Seconds())
	}
	if m.openOrders >= m.cfg.MaxOpenOrders {
		return fmt.Errorf("max open orders reached: %d/%d", m.openOrders, m.cfg.MaxOpenOrders)
	}
	dailyLossLimit := m.dailyLossLimitLocked()
	if dailyLossLimit > 0 && m.dailyPnL <= -dailyLossLimit {
		return fmt.Errorf("daily loss limit reached: %.2f/%.2f", m.dailyPnL, -dailyLossLimit)
	}
	pos := m.positions[tokenID]
	if pos+amountUSDC > m.cfg.MaxPositionPerMarket {
		return fmt.Errorf("position limit for %s: %.2f+%.2f > %.2f", tokenID, pos, amountUSDC, m.cfg.MaxPositionPerMarket)
	}
	return nil
}

func (m *Manager) SetOpenOrders(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.openOrders = n
}

func (m *Manager) RecordPnL(amount float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dailyPnL += amount
}

func (m *Manager) AddPosition(tokenID string, amountUSDC float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.positions[tokenID] += amountUSDC
}

func (m *Manager) RemovePosition(tokenID string, amountUSDC float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.positions[tokenID] -= amountUSDC
	if m.positions[tokenID] <= 0 {
		delete(m.positions, tokenID)
	}
}

func (m *Manager) SetEmergencyStop(stop bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.emergencyStop = stop
}

func (m *Manager) EmergencyStop() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.emergencyStop
}

func (m *Manager) DailyPnL() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.dailyPnL
}

func (m *Manager) ResetDaily() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dailyStartPnL = m.dailyPnL
	m.dailyPnL = 0
	m.consecutiveLosses = 0
	m.cooldownUntil = time.Time{}
}

// SyncFromTracker updates risk state from the execution tracker.
func (m *Manager) SyncFromTracker(openOrders int, positions map[string]execution.Position, realizedPnL float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.openOrders = openOrders
	m.dailyPnL = realizedPnL

	// Rebuild position exposure from tracker positions.
	m.positions = make(map[string]float64, len(positions))
	for assetID, pos := range positions {
		exposure := pos.AvgEntryPrice * abs(pos.NetSize)
		if exposure > 0 {
			m.positions[assetID] = exposure
		}
	}
}

// EvaluateStopLoss checks if a position's unrealized loss exceeds the per-market stop-loss.
func (m *Manager) EvaluateStopLoss(assetID string, pos execution.Position, currentMid float64) bool {
	if m.cfg.StopLossPerMarket <= 0 {
		return false
	}
	unrealized := (currentMid - pos.AvgEntryPrice) * pos.NetSize
	totalPnL := pos.RealizedPnL + unrealized
	return totalPnL <= -m.cfg.StopLossPerMarket
}

// EvaluateDrawdown checks if total drawdown exceeds the max allowed percentage.
// Capital is the starting capital for calculating the percentage.
func (m *Manager) EvaluateDrawdown(realizedPnL, unrealizedPnL, capital float64) bool {
	if m.cfg.MaxDrawdownPct <= 0 || capital <= 0 {
		return false
	}
	totalPnL := realizedPnL + unrealizedPnL
	drawdownPct := -totalPnL / capital
	return drawdownPct >= m.cfg.MaxDrawdownPct
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// DailyLossLimitUSDC returns the effective daily loss limit after config derivation.
func (m *Manager) DailyLossLimitUSDC() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.dailyLossLimitLocked()
}

// RecordTradeResult updates consecutive-loss state using realized PnL deltas.
// Returns true when loss streak triggers a cooldown.
func (m *Manager) RecordTradeResult(realizedDelta float64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if realizedDelta < 0 {
		m.consecutiveLosses++
	} else if realizedDelta > 0 {
		m.consecutiveLosses = 0
	}

	if m.cfg.MaxConsecutiveLosses <= 0 || m.consecutiveLosses < m.cfg.MaxConsecutiveLosses {
		return false
	}

	cooldown := m.cfg.ConsecutiveLossCooldown
	if cooldown <= 0 {
		cooldown = 15 * time.Minute
	}
	m.cooldownUntil = time.Now().Add(cooldown)
	return true
}

func (m *Manager) ConsecutiveLosses() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.consecutiveLosses
}

func (m *Manager) InCooldown() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.inCooldownLocked()
}

func (m *Manager) CooldownRemaining() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.inCooldownLocked() {
		return 0
	}
	return m.cooldownUntil.Sub(time.Now())
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	remaining := time.Duration(0)
	inCooldown := m.inCooldownLocked()
	if inCooldown {
		remaining = m.cooldownUntil.Sub(time.Now())
	}
	return Snapshot{
		EmergencyStop:        m.emergencyStop,
		DailyPnL:             m.dailyPnL,
		DailyLossLimitUSDC:   m.dailyLossLimitLocked(),
		ConsecutiveLosses:    m.consecutiveLosses,
		InCooldown:           inCooldown,
		CooldownRemaining:    remaining,
		MaxConsecutiveLosses: m.cfg.MaxConsecutiveLosses,
	}
}

func (m *Manager) dailyLossLimitLocked() float64 {
	limit := m.cfg.MaxDailyLossUSDC
	if m.cfg.AccountCapitalUSDC > 0 && m.cfg.MaxDailyLossPct > 0 {
		derived := m.cfg.AccountCapitalUSDC * m.cfg.MaxDailyLossPct
		if limit <= 0 || derived < limit {
			limit = derived
		}
	}
	return limit
}

func (m *Manager) inCooldownLocked() bool {
	if m.cooldownUntil.IsZero() {
		return false
	}
	return time.Now().Before(m.cooldownUntil)
}
