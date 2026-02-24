package feed

import (
	"testing"

	"github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/ws"
)

func TestBookSnapshotUpdate(t *testing.T) {
	snap := NewBookSnapshot()
	event := ws.OrderbookEvent{
		AssetID: "token-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "100"}, {Price: "0.49", Size: "200"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "150"}, {Price: "0.53", Size: "250"}},
	}
	snap.Update(event)

	book, ok := snap.Get("token-1")
	if !ok {
		t.Fatal("expected book for token-1")
	}
	if len(book.Bids) != 2 {
		t.Fatalf("expected 2 bid levels, got %d", len(book.Bids))
	}
	if book.Bids[0].Price != "0.50" {
		t.Fatalf("expected best bid 0.50, got %s", book.Bids[0].Price)
	}
	if len(book.Asks) != 2 {
		t.Fatalf("expected 2 ask levels, got %d", len(book.Asks))
	}
}

func TestBookSnapshotMid(t *testing.T) {
	snap := NewBookSnapshot()
	snap.Update(ws.OrderbookEvent{
		AssetID: "token-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "100"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "100"}},
	})
	mid, err := snap.Mid("token-1")
	if err != nil {
		t.Fatal(err)
	}
	expected := 0.51
	if mid < expected-0.001 || mid > expected+0.001 {
		t.Fatalf("expected mid ~0.51, got %f", mid)
	}
}

func TestBookSnapshotDepth(t *testing.T) {
	snap := NewBookSnapshot()
	snap.Update(ws.OrderbookEvent{
		AssetID: "token-1",
		Bids:    []ws.OrderbookLevel{{Price: "0.50", Size: "100"}, {Price: "0.49", Size: "200"}},
		Asks:    []ws.OrderbookLevel{{Price: "0.52", Size: "150"}, {Price: "0.53", Size: "250"}},
	})
	bidDepth, askDepth := snap.Depth("token-1", 2)
	if bidDepth != 300 {
		t.Fatalf("expected bid depth 300, got %f", bidDepth)
	}
	if askDepth != 400 {
		t.Fatalf("expected ask depth 400, got %f", askDepth)
	}
}

func TestBookSnapshotMissing(t *testing.T) {
	snap := NewBookSnapshot()
	_, err := snap.Mid("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing asset")
	}
}

func TestBookSnapshotAssetIDs(t *testing.T) {
	snap := NewBookSnapshot()
	snap.Update(ws.OrderbookEvent{AssetID: "t1", Bids: []ws.OrderbookLevel{{Price: "0.5", Size: "10"}}, Asks: []ws.OrderbookLevel{{Price: "0.6", Size: "10"}}})
	snap.Update(ws.OrderbookEvent{AssetID: "t2", Bids: []ws.OrderbookLevel{{Price: "0.5", Size: "10"}}, Asks: []ws.OrderbookLevel{{Price: "0.6", Size: "10"}}})
	ids := snap.AssetIDs()
	if len(ids) != 2 {
		t.Fatalf("expected 2 assets, got %d", len(ids))
	}
}
