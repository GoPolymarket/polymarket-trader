package builder

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/GoPolymarket/polymarket-go-sdk/pkg/data"
)

// VolumeTracker periodically syncs builder volume and leaderboard data.
type VolumeTracker struct {
	dataClient   data.Client
	mu           sync.RWMutex
	dailyVolume  []data.BuilderVolumeEntry
	leaderboard  []data.BuilderLeaderboardEntry
	lastSync     time.Time
	syncInterval time.Duration
}

func NewVolumeTracker(dataClient data.Client, syncInterval time.Duration) *VolumeTracker {
	return &VolumeTracker{
		dataClient:   dataClient,
		syncInterval: syncInterval,
	}
}

func (t *VolumeTracker) Sync(ctx context.Context) error {
	vol, err := t.dataClient.BuildersVolume(ctx, &data.BuildersVolumeRequest{})
	if err != nil {
		return err
	}
	lb, err := t.dataClient.BuildersLeaderboard(ctx, &data.BuildersLeaderboardRequest{})
	if err != nil {
		return err
	}

	t.mu.Lock()
	t.dailyVolume = vol
	t.leaderboard = lb
	t.lastSync = time.Now()
	t.mu.Unlock()
	return nil
}

func (t *VolumeTracker) DailyVolume() []data.BuilderVolumeEntry {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.dailyVolume
}

func (t *VolumeTracker) Leaderboard() []data.BuilderLeaderboardEntry {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.leaderboard
}

// DailyVolumeJSON returns daily volume as interface{} for JSON serialization.
func (t *VolumeTracker) DailyVolumeJSON() interface{} {
	return t.DailyVolume()
}

// LeaderboardJSON returns leaderboard as interface{} for JSON serialization.
func (t *VolumeTracker) LeaderboardJSON() interface{} {
	return t.Leaderboard()
}

func (t *VolumeTracker) LastSync() time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lastSync
}

func (t *VolumeTracker) Run(ctx context.Context) error {
	// Initial sync.
	if err := t.Sync(ctx); err != nil {
		log.Printf("builder tracker initial sync: %v", err)
	}

	ticker := time.NewTicker(t.syncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := t.Sync(ctx); err != nil {
				log.Printf("builder tracker sync: %v", err)
			}
		}
	}
}
