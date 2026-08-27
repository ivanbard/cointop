package local

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cointop-sh/cointop/pkg/api/types"
)

func TestClientImplementsProviderSurface(t *testing.T) {
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, data any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "meta": map[string]any{"provider": "fake", "fetchedAt": "2026-08-21T01:00:00Z", "expiresAt": "2026-08-21T01:01:00Z", "cacheStatus": "hit", "stale": false}})
	}
	mux.HandleFunc("/v1/ready", func(w http.ResponseWriter, _ *http.Request) { write(w, map[string]any{"status": "ready"}) })
	mux.HandleFunc("/v1/coins", func(w http.ResponseWriter, _ *http.Request) {
		write(w, map[string]any{"items": []any{map[string]any{"id": "bitcoin", "name": "Bitcoin", "symbol": "BTC", "price": 42}}, "limit": 500, "offset": 0, "total": 1})
	})
	mux.HandleFunc("/v1/coins/", func(w http.ResponseWriter, _ *http.Request) {
		write(w, map[string]any{"id": "bitcoin", "name": "Bitcoin", "symbol": "BTC", "price": 42})
	})
	mux.HandleFunc("/v1/prices", func(w http.ResponseWriter, _ *http.Request) {
		write(w, []any{map[string]any{"id": "bitcoin", "symbol": "BTC", "price": 42}})
	})
	mux.HandleFunc("/v1/global", func(w http.ResponseWriter, _ *http.Request) { write(w, map[string]any{"totalMarketCapUSD": 100}) })
	mux.HandleFunc("/v1/currencies", func(w http.ResponseWriter, _ *http.Request) { write(w, []string{"BTC", "USD"}) })
	mux.HandleFunc("/v1/exchange-rate", func(w http.ResponseWriter, _ *http.Request) {
		write(w, map[string]any{"from": "BTC", "to": "USD", "rate": 42})
	})
	mux.HandleFunc("/v1/links/coins/", func(w http.ResponseWriter, _ *http.Request) {
		write(w, map[string]any{"identifier": "bitcoin", "url": "https://example.test/bitcoin"})
	})
	mux.HandleFunc("/v1/charts/coins/", func(w http.ResponseWriter, _ *http.Request) {
		write(w, map[string]any{"series": map[string]any{"price": [][]float64{{1, 42}}}})
	})
	mux.HandleFunc("/v1/charts/global", func(w http.ResponseWriter, _ *http.Request) {
		write(w, map[string]any{"series": map[string]any{"marketCapByAvailableSupply": [][]float64{{1, 100}}}})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Ping(); err != nil {
		t.Fatal(err)
	}
	ch := make(chan []types.Coin)
	if err := client.GetAllCoinData("USD", ch); err != nil {
		t.Fatal(err)
	}
	if coins := <-ch; len(coins) != 1 || coins[0].ID != "bitcoin" {
		t.Fatalf("coins: %#v", coins)
	}
	if coin, err := client.GetCoinData("bitcoin", "USD"); err != nil || coin.Price != 42 {
		t.Fatalf("coin=%#v err=%v", coin, err)
	}
	if price, err := client.Price("btc", "USD"); err != nil || price != 42 {
		t.Fatalf("price=%v err=%v", price, err)
	}
	if rate, err := client.GetExchangeRate("BTC", "USD", false); err != nil || rate != 42 {
		t.Fatalf("rate=%v err=%v", rate, err)
	}
	if link := client.CoinLink("bitcoin"); link != "https://example.test/bitcoin" {
		t.Fatalf("link=%q", link)
	}
	if graph, err := client.GetCoinGraphData("USD", "BTC", "Bitcoin", 0, 1); err != nil || len(graph.Price) != 1 {
		t.Fatalf("graph=%#v err=%v", graph, err)
	}
	if global, err := client.GetGlobalMarketGraphData("USD", 0, 1); err != nil || len(global.MarketCapByAvailableSupply) != 1 {
		t.Fatalf("global=%#v err=%v", global, err)
	}
}

func TestNewRejectsNonLoopbackEndpoint(t *testing.T) {
	if _, err := New("http://example.com:7070"); err == nil {
		t.Fatal("expected non-loopback endpoint rejection")
	}
}

func TestRangeForYTD(t *testing.T) {
	end := time.Date(2026, time.August, 21, 1, 0, 0, 0, time.UTC)
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	if got := rangeFor(start.Unix(), end.Unix()); got != "ytd" {
		t.Fatalf("got %q", got)
	}
}

func TestRangeForEpochIsAll(t *testing.T) {
	if got := rangeFor(0, time.Now().Unix()); got != "all" {
		t.Fatalf("got %q", got)
	}
}

func TestPaginationReportsLaterPageErrorWithoutLegacyPartialOutput(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if requests == 2 {
			http.Error(w, "failed", http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"items": []any{map[string]any{"id": "bitcoin"}}, "total": 501}, "meta": map[string]any{}})
	}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	results := client.StreamAllCoinData(context.Background(), "USD")
	if first := <-results; first.Err != nil || len(first.Coins) != 1 {
		t.Fatalf("first=%+v", first)
	}
	if second := <-results; second.Err == nil {
		t.Fatal("expected second-page error")
	}
	requests = 0
	pages := make(chan []types.Coin)
	if err := client.GetAllCoinData("USD", pages); err == nil {
		t.Fatal("legacy call must return pagination error")
	}
	select {
	case page := <-pages:
		t.Fatalf("partial legacy page: %+v", page)
	default:
	}
}
