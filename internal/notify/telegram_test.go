package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedChatID = r.URL.Query().Get("chat_id")
		receivedText = r.URL.Query().Get("text")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]bool{"ok": true}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	n := &Notifier{
		botToken:   "test-token",
		chatID:     "test-chat",
		httpClient: server.Client(),
		enabled:    true,
		baseURL:    server.URL,
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		if err := json.NewEncoder(w).Encode(map[string]string{"description": "bad request"}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	n := &Notifier{
		botToken:   "test-token",
		chatID:     "test-chat",
		httpClient: server.Client(),
		enabled:    true,
		baseURL:    server.URL,
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedText = r.URL.Query().Get("text")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]bool{"ok": true}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	n := &Notifier{
		botToken:   "test-token",
		chatID:     "test-chat",
		httpClient: server.Client(),
		enabled:    true,
		baseURL:    server.URL,
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
