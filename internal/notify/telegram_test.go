package notify

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func testHTTPClient(fn roundTripFunc) *http.Client {
	return &http.Client{Transport: fn}
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestNewNotifierDisabled(t *testing.T) {
	n := NewNotifier("", "")
	if n.Enabled() {
		t.Fatal("expected disabled notifier with empty credentials")
	}
}

func TestNewNotifierEnabled(t *testing.T) {
	n := NewNotifier("bot123", "chat456")
	if !n.Enabled() {
		t.Fatal("expected enabled notifier with credentials")
	}
}

func TestSendDisabled(t *testing.T) {
	n := NewNotifier("", "")
	if err := n.Send(context.Background(), "test"); err != nil {
		t.Fatalf("disabled send should succeed silently: %v", err)
	}
}

func TestSendSuccess(t *testing.T) {
	var receivedChatID, receivedText string
	client := testHTTPClient(func(r *http.Request) (*http.Response, error) {
		receivedChatID = r.URL.Query().Get("chat_id")
		receivedText = r.URL.Query().Get("text")
		if r.Method != http.MethodPost {
			return jsonResponse(http.StatusMethodNotAllowed, `{"description":"method not allowed"}`), nil
		}
		return jsonResponse(http.StatusOK, `{"ok":true}`), nil
	})

	n := &Notifier{
		botToken:   "test-token",
		chatID:     "test-chat",
		httpClient: client,
		enabled:    true,
		baseURL:    "https://telegram.test/sendMessage",
	}

	err := n.Send(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("send should succeed: %v", err)
	}
	if receivedChatID != "test-chat" {
		t.Errorf("expected chat_id=test-chat, got %s", receivedChatID)
	}
	if receivedText != "hello world" {
		t.Errorf("expected text=hello world, got %s", receivedText)
	}
}

func TestSendServerError(t *testing.T) {
	client := testHTTPClient(func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusBadRequest, `{"description":"bad request"}`), nil
	})

	n := &Notifier{
		botToken:   "test-token",
		chatID:     "test-chat",
		httpClient: client,
		enabled:    true,
		baseURL:    "https://telegram.test/sendMessage",
	}

	err := n.Send(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error for server error response")
	}
}

func TestNotifyFillDisabled(t *testing.T) {
	n := NewNotifier("", "")
	if err := n.NotifyFill(context.Background(), "asset-1", "BUY", 0.50, 10); err != nil {
		t.Fatalf("disabled notify should succeed: %v", err)
	}
}

func TestNotifyFillSuccess(t *testing.T) {
	var receivedText string
	client := testHTTPClient(func(r *http.Request) (*http.Response, error) {
		receivedText = r.URL.Query().Get("text")
		return jsonResponse(http.StatusOK, `{"ok":true}`), nil
	})

	n := &Notifier{
		botToken:   "test-token",
		chatID:     "test-chat",
		httpClient: client,
		enabled:    true,
		baseURL:    "https://telegram.test/sendMessage",
	}

	if err := n.NotifyFill(context.Background(), "asset-1", "BUY", 0.5000, 10.00); err != nil {
		t.Fatalf("notify fill: %v", err)
	}
	if receivedText == "" {
		t.Error("expected non-empty text")
	}
}

func TestNotifyStopLossDisabled(t *testing.T) {
	n := NewNotifier("", "")
	if err := n.NotifyStopLoss(context.Background(), "asset-1", -5.0); err != nil {
		t.Fatalf("disabled notify should succeed: %v", err)
	}
}

func TestNotifyEmergencyStopDisabled(t *testing.T) {
	n := NewNotifier("", "")
	if err := n.NotifyEmergencyStop(context.Background()); err != nil {
		t.Fatalf("disabled notify should succeed: %v", err)
	}
}

func TestNotifyDailySummaryDisabled(t *testing.T) {
	n := NewNotifier("", "")
	if err := n.NotifyDailySummary(context.Background(), 1.5, 10, 100); err != nil {
		t.Fatalf("disabled notify should succeed: %v", err)
	}
}
