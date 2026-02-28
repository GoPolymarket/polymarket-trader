package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Notifier sends alerts to a Telegram chat via the Bot API.
type Notifier struct {
	botToken   string
	chatID     string
	httpClient *http.Client
	enabled    bool
	baseURL    string // overridable for testing; defaults to Telegram API
}

// NewNotifier creates a Notifier. Notifications are enabled only when both
// botToken and chatID are non-empty.
func NewNotifier(botToken, chatID string) *Notifier {
	return &Notifier{
		botToken:   botToken,
		chatID:     chatID,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		enabled:    botToken != "" && chatID != "",
	}
}

// Enabled reports whether the notifier is active.
func (n *Notifier) Enabled() bool { return n.enabled }

// Send posts a message to the configured Telegram chat.
func (n *Notifier) Send(ctx context.Context, msg string) error {
	if !n.enabled {
		return nil
	}

	endpoint := n.baseURL
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", n.botToken)
	}
	vals := url.Values{
		"chat_id":    {n.chatID},
		"text":       {msg},
		"parse_mode": {"HTML"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return fmt.Errorf("notify: build request: %w", err)
	}
	req.URL.RawQuery = vals.Encode()

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("notify: send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var body struct {
			Description string `json:"description"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return fmt.Errorf("notify: telegram %d: %s", resp.StatusCode, body.Description)
	}
	return nil
}

// NotifyFill sends a trade fill alert.
func (n *Notifier) NotifyFill(ctx context.Context, assetID, side string, price, size float64) error {
	msg := fmt.Sprintf("<b>Fill</b>\nAsset: <code>%s</code>\nSide: %s\nPrice: %.4f\nSize: %.2f", assetID, side, price, size)
	return n.Send(ctx, msg)
}

// NotifyStopLoss sends a stop-loss trigger alert.
func (n *Notifier) NotifyStopLoss(ctx context.Context, assetID string, pnl float64) error {
	msg := fmt.Sprintf("<b>Stop-Loss Triggered</b>\nAsset: <code>%s</code>\nPnL: %.2f USDC", assetID, pnl)
	return n.Send(ctx, msg)
}

// NotifyEmergencyStop sends an emergency stop alert.
func (n *Notifier) NotifyEmergencyStop(ctx context.Context) error {
	return n.Send(ctx, "<b>EMERGENCY STOP</b>\nMax drawdown exceeded. All trading halted.")
}

// NotifyDailySummary sends a daily performance summary.
func (n *Notifier) NotifyDailySummary(ctx context.Context, pnl float64, fills int, volume float64) error {
	msg := fmt.Sprintf("<b>Daily Summary</b>\nPnL: %.2f USDC\nFills: %d\nVolume: %.2f USDC", pnl, fills, volume)
	return n.Send(ctx, msg)
}

// NotifyRiskCooldown sends a risk cooldown alert after a loss streak.
func (n *Notifier) NotifyRiskCooldown(ctx context.Context, consecutiveLosses, maxConsecutiveLosses int, cooldownRemaining time.Duration) error {
	msg := fmt.Sprintf(
		"<b>Risk Cooldown</b>\nConsecutive Losses: %d/%d\nCooldown Remaining: %.0fs",
		consecutiveLosses,
		maxConsecutiveLosses,
		cooldownRemaining.Seconds(),
	)
	return n.Send(ctx, msg)
}

// NotifyDailyCoachTemplate sends a pre-rendered daily coaching template.
func (n *Notifier) NotifyDailyCoachTemplate(ctx context.Context, textHTML string) error {
	return n.Send(ctx, textHTML)
}

// NotifyWeeklyReviewTemplate sends a pre-rendered weekly review template.
func (n *Notifier) NotifyWeeklyReviewTemplate(ctx context.Context, textHTML string) error {
	return n.Send(ctx, textHTML)
}
