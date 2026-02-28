package builder

import (
	"testing"
	"time"
)

func TestNewVolumeTracker(t *testing.T) {
	tracker := NewVolumeTracker(nil, 10*time.Minute)
	if tracker == nil {
		t.Fatal("expected non-nil tracker")
		return
	}
	if tracker.syncInterval != 10*time.Minute {
		t.Errorf("expected 10m sync interval, got %v", tracker.syncInterval)
	}
}

func TestVolumeTrackerInitialState(t *testing.T) {
	tracker := NewVolumeTracker(nil, 10*time.Minute)

	if len(tracker.DailyVolume()) != 0 {
		t.Errorf("expected 0 daily volume entries, got %d", len(tracker.DailyVolume()))
	}
	if len(tracker.Leaderboard()) != 0 {
		t.Errorf("expected 0 leaderboard entries, got %d", len(tracker.Leaderboard()))
	}
	if !tracker.LastSync().IsZero() {
		t.Error("expected zero last sync time")
	}
}

func TestVolumeTrackerJSONWrappers(t *testing.T) {
	tracker := NewVolumeTracker(nil, 10*time.Minute)

	vol := tracker.DailyVolumeJSON()
	if vol == nil {
		t.Error("DailyVolumeJSON should not be nil")
	}

	lb := tracker.LeaderboardJSON()
	if lb == nil {
		t.Error("LeaderboardJSON should not be nil")
	}
}
