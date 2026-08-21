package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	apitypes "github.com/cointop-sh/cointop/pkg/api/types"
)

type fakeProvider struct {
	mu     sync.Mutex
	calls  int
	fail   bool
	delay  time.Duration
	coins  []apitypes.Coin
	global apitypes.GlobalMarketData
}

func (f *fakeProvider) Ping() error { return nil }
func (f *fakeProvider) GetAllCoinData(_ string, ch chan []apitypes.Coin) error {
	f.record()
	if f.fail {
		return errors.New("offline")
	}
	go func() { defer close(ch); ch <- append([]apitypes.Coin(nil), f.coins...) }()
	return nil
}
func (f *fakeProvider) GetCoinGraphData(string, string, string, int64, int64) (apitypes.CoinGraph, error) {
	return apitypes.CoinGraph{}, nil
}
func (f *fakeProvider) GetGlobalMarketGraphData(string, int64, int64) (apitypes.MarketGraph, error) {
	return apitypes.MarketGraph{}, nil
}
func (f *fakeProvider) GetGlobalMarketData(string) (apitypes.GlobalMarketData, error) {
	f.record()
	if f.fail {
		return apitypes.GlobalMarketData{}, errors.New("offline")
	}
	return f.global, nil
}
func (f *fakeProvider) GetCoinData(name, _ string) (apitypes.Coin, error) {
	f.record()
	if f.fail {
		return apitypes.Coin{}, errors.New("offline")
	}
	for _, coin := range f.coins {
		if coin.ID == name || coin.Name == name || coin.Symbol == name {
			return coin, nil
		}
	}
	return apitypes.Coin{}, nil
}
func (f *fakeProvider) GetCoinDataBatch(_ []string, _ string) ([]apitypes.Coin, error) {
	f.record()
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.fail {
		return nil, errors.New("offline")
	}
	return append([]apitypes.Coin(nil), f.coins...), nil
}
func (f *fakeProvider) CoinLink(string) string                                { return "" }
func (f *fakeProvider) SupportedCurrencies() []string                         { f.record(); return []string{"USD", "BTC"} }
func (f *fakeProvider) Price(string, string) (float64, error)                 { return 0, nil }
func (f *fakeProvider) GetExchangeRate(string, string, bool) (float64, error) { return 1, nil }
func (f *fakeProvider) record()                                               { f.mu.Lock(); f.calls++; f.mu.Unlock() }
func (f *fakeProvider) callCount() int                                        { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }

func testService(t *testing.T, provider *fakeProvider, now *time.Time) *Service {
	t.Helper()
	service, err := NewService(provider, Config{
		Provider: "fake", CacheDir: t.TempDir(), FreshTTL: time.Minute,
		CurrenciesTTL: time.Hour, MaxStale: time.Hour, Now: func() time.Time { return *now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestPricesCacheHitAndStaleFallback(t *testing.T) {
	now := time.Date(2026, 8, 21, 1, 0, 0, 0, time.UTC)
	provider := &fakeProvider{coins: []apitypes.Coin{{ID: "bitcoin", Name: "Bitcoin", Symbol: "BTC", Price: 42}}}
	service := testService(t, provider, &now)

	first, err := service.Prices(context.Background(), []string{"btc"}, "usd")
	if err != nil {
		t.Fatal(err)
	}
	if first.Meta.CacheStatus != "miss" || provider.callCount() != 1 {
		t.Fatalf("unexpected first result: %+v calls=%d", first.Meta, provider.callCount())
	}
	second, err := service.Prices(context.Background(), []string{"BTC"}, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if second.Meta.CacheStatus != "hit" || provider.callCount() != 1 {
		t.Fatalf("unexpected hit: %+v calls=%d", second.Meta, provider.callCount())
	}

	now = now.Add(2 * time.Minute)
	provider.fail = true
	stale, err := service.Prices(context.Background(), []string{"btc"}, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if stale.Meta.CacheStatus != "stale" || !stale.Meta.Stale {
		t.Fatalf("expected stale response, got %+v", stale.Meta)
	}

	now = now.Add(2 * time.Hour)
	_, err = service.Prices(context.Background(), []string{"btc"}, "USD")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected unavailable, got %v", err)
	}
}

func TestPricesCoalescesConcurrentRequests(t *testing.T) {
	now := time.Now().UTC()
	provider := &fakeProvider{delay: 50 * time.Millisecond, coins: []apitypes.Coin{{ID: "bitcoin", Name: "Bitcoin", Symbol: "BTC", Price: 42}}}
	service := testService(t, provider, &now)
	const requests = 12
	var wg sync.WaitGroup
	errs := make(chan error, requests)
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.Prices(context.Background(), []string{"btc"}, "USD")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if provider.callCount() != 1 {
		t.Fatalf("expected one provider call, got %d", provider.callCount())
	}
}

func TestPricesPreferRankedCoinForAmbiguousSymbol(t *testing.T) {
	now := time.Now().UTC()
	provider := &fakeProvider{coins: []apitypes.Coin{
		{ID: "bitcoin", Name: "Bitcoin", Symbol: "BTC", Rank: 1, Price: 42},
		{ID: "batcat", Name: "batcat", Symbol: "BTC", Rank: 900, Price: 0.00001},
	}}
	service := testService(t, provider, &now)
	result, err := service.Prices(context.Background(), []string{"btc"}, "USD")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	var prices []Price
	if err := json.Unmarshal(raw, &prices); err != nil {
		t.Fatal(err)
	}
	if len(prices) != 1 || prices[0].ID != "bitcoin" {
		t.Fatalf("unexpected prices: %+v", prices)
	}
}

func TestSnapshotPersistsAcrossServices(t *testing.T) {
	now := time.Now().UTC()
	dir := t.TempDir()
	provider := &fakeProvider{coins: []apitypes.Coin{{ID: "bitcoin", Name: "Bitcoin", Symbol: "BTC", Price: 42}}}
	config := Config{Provider: "fake", CacheDir: dir, FreshTTL: time.Minute, MaxStale: time.Hour, Now: func() time.Time { return now }}
	first, err := NewService(provider, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Prices(context.Background(), []string{"btc"}, "USD"); err != nil {
		t.Fatal(err)
	}
	provider.fail = true
	second, err := NewService(provider, config)
	if err != nil {
		t.Fatal(err)
	}
	result, err := second.Prices(context.Background(), []string{"btc"}, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta.CacheStatus != "hit" {
		t.Fatalf("expected persisted cache hit, got %s", result.Meta.CacheStatus)
	}
}

func TestCorruptSnapshotBecomesCacheMiss(t *testing.T) {
	dir := t.TempDir()
	cache, err := newSnapshotCache(dir)
	if err != nil {
		t.Fatal(err)
	}
	key := "v1:fake:prices:USD:btc"
	if err := os.WriteFile(cache.path(key), []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.get(key); ok {
		t.Fatal("corrupt snapshot should not be returned")
	}
	if _, err := os.Stat(cache.path(key) + ".corrupt"); err != nil {
		t.Fatalf("corrupt snapshot was not quarantined: %v", err)
	}
}
