package web

import (
	"context"
	"testing"
)

// An online refresh tick force-refreshes the market cache (Yahoo's daily candle
// carries today's live price), stamping the series as fetched today.
func TestRefreshOnceUpdatesCache(t *testing.T) {
	srv, f := testServer(t)
	srv.offline = false // testServer is offline by default; this tick must fetch
	if !f.Book.Market.Price("cw8").FetchedAt.IsZero() {
		t.Fatal("precondition: FetchedAt should start zero")
	}

	srv.refreshOnce(context.Background())

	if f.Book.Market.Price("cw8").FetchedAt.IsZero() {
		t.Fatal("online refreshOnce did not refresh the cache")
	}
}

// Offline mode never touches the network.
func TestRefreshOnceNoopOffline(t *testing.T) {
	srv, f := testServer(t) // offline = true
	srv.refreshOnce(context.Background())
	if !f.Book.Market.Price("cw8").FetchedAt.IsZero() {
		t.Fatal("offline refreshOnce must not fetch")
	}
}
