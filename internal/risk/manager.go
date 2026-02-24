package risk

import (
	"fmt"
	"sync"
)

type Config struct {
	MaxOpenOrders        int
	MaxDailyLossUSDC     float64
	MaxPositionPerMarket float64
}

type Manager struct {
	mu            sync.RWMutex
	cfg           Config
	openOrders    int
	dailyPnL      float64
	positions     map[string]float64 // tokenID → USDC exposure
	emergencyStop bool
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
	if m.openOrders >= m.cfg.MaxOpenOrders {
		return fmt.Errorf("max open orders reached: %d/%d", m.openOrders, m.cfg.MaxOpenOrders)
	}
	if m.dailyPnL <= -m.cfg.MaxDailyLossUSDC {
		return fmt.Errorf("daily loss limit reached: %.2f/%.2f", m.dailyPnL, -m.cfg.MaxDailyLossUSDC)
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

func (m *Manager) DailyPnL() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.dailyPnL
}

func (m *Manager) ResetDaily() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dailyPnL = 0
}
