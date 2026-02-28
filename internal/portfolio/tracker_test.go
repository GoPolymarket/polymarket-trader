package portfolio

import (
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

func TestNewTracker(t *testing.T) {
	addr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	tracker := NewTracker(nil, addr, 5*time.Minute)

	if tracker == nil {
		t.Fatal("expected non-nil tracker")
		return
	}
	if tracker.syncInterval != 5*time.Minute {
		t.Errorf("expected 5m sync interval, got %v", tracker.syncInterval)
	}
}

func TestTrackerInitialState(t *testing.T) {
	addr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	tracker := NewTracker(nil, addr, 5*time.Minute)

	if tracker.TotalValue() != 0 {
		t.Errorf("expected 0 total value, got %f", tracker.TotalValue())
	}
	if !tracker.LastSync().IsZero() {
		t.Error("expected zero last sync time")
	}
	if len(tracker.Positions()) != 0 {
		t.Errorf("expected 0 positions, got %d", len(tracker.Positions()))
	}
}
