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
	mu        sync.Mutex
	calls     int
	fail      bool
	delay     time.Duration
	coins     []apitypes.Coin
	global    apitypes.GlobalMarketData
	pingErr   error
	pingDelay time.Duration
	pingCalls int
	pageErr   error
}

func (f *fakeProvider) Ping() error {
	f.mu.Lock()
	f.pingCalls++
	delay, err := f.pingDelay, f.pingErr
	f.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	return err
}
func (f *fakeProvider) pingCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pingCalls
}
func (f *fakeProvider) GetAllCoinData(_ string, ch chan []apitypes.Coin) error {
	f.record()
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.fail {
		return errors.New("offline")
	}
	go func() { defer close(ch); ch <- append([]apitypes.Coin(nil), f.coins...) }()
	return nil
}
func (f *fakeProvider) StreamAllCoinData(ctx context.Context, _ string) <-chan apitypes.CoinPageResult {
	results := make(chan apitypes.CoinPageResult)
	go func() {
		defer close(results)
		f.record()
		if f.delay > 0 {
			time.Sleep(f.delay)
		}
		if len(f.coins) > 0 {
			select {
			case results <- apitypes.CoinPageResult{Coins: append([]apitypes.Coin(nil), f.coins...)}:
			case <-ctx.Done():
				results <- apitypes.CoinPageResult{Err: ctx.Err()}
				return
			}
		}
		if f.pageErr != nil {
			results <- apitypes.CoinPageResult{Err: f.pageErr}
			return
		}
		if f.fail {
			results <- apitypes.CoinPageResult{Err: errors.New("offline")}
		}
	}()
	return results
}
func (f *fakeProvider) GetCoinGraphData(string, string, string, int64, int64) (apitypes.CoinGraph, error) {
	f.record()
	if f.fail {
		return apitypes.CoinGraph{}, errors.New("offline")
	}
	return apitypes.CoinGraph{Price: [][]float64{{1, 42}}}, nil
}
func (f *fakeProvider) GetGlobalMarketGraphData(string, int64, int64) (apitypes.MarketGraph, error) {
	f.record()
	if f.fail {
		return apitypes.MarketGraph{}, errors.New("offline")
	}
	return apitypes.MarketGraph{MarketCapByAvailableSupply: [][]float64{{1, 100}}}, nil
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
func (f *fakeProvider) CoinLink(slug string) string                           { return "https://example.test/" + slug }
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

func TestChartRangesCacheAndStaleFallback(t *testing.T) {
	now := time.Date(2026, 8, 21, 1, 0, 0, 0, time.UTC)
	provider := &fakeProvider{coins: []apitypes.Coin{{ID: "bitcoin", Name: "Bitcoin", Symbol: "BTC", Rank: 1}}}
	service := testService(t, provider, &now)
	first, err := service.CoinHistory(context.Background(), "btc", "usd", "ytd")
	if err != nil {
		t.Fatal(err)
	}
	if first.Meta.CacheStatus != "miss" || provider.callCount() != 2 {
		t.Fatalf("unexpected first chart result: %+v calls=%d", first.Meta, provider.callCount())
	}
	second, err := service.CoinHistory(context.Background(), "bitcoin", "USD", "YTD")
	if err != nil {
		t.Fatal(err)
	}
	if second.Meta.CacheStatus != "hit" || provider.callCount() != 2 {
		t.Fatalf("chart cache missed: %+v calls=%d", second.Meta, provider.callCount())
	}
	now = now.Add(6 * time.Minute)
	provider.fail = true
	stale, err := service.CoinHistory(context.Background(), "bitcoin", "USD", "ytd")
	if err != nil {
		t.Fatal(err)
	}
	if !stale.Meta.Stale || stale.Meta.CacheStatus != "stale" {
		t.Fatalf("expected stale chart, got %+v", stale.Meta)
	}
	if _, err := service.GlobalHistory(context.Background(), "USD", "2y"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected invalid range, got %v", err)
	}
}

func TestExchangeRateAndCoinLinkAreCached(t *testing.T) {
	now := time.Now().UTC()
	provider := &fakeProvider{}
	service := testService(t, provider, &now)
	if _, err := service.ExchangeRate(context.Background(), "btc", "usd"); err != nil {
		t.Fatal(err)
	}
	first, err := service.CoinLink(context.Background(), "bitcoin")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CoinLink(context.Background(), "BITCOIN")
	if err != nil {
		t.Fatal(err)
	}
	if first.Meta.CacheStatus != "miss" || second.Meta.CacheStatus != "hit" {
		t.Fatalf("unexpected link cache statuses: %s %s", first.Meta.CacheStatus, second.Meta.CacheStatus)
	}
}

func TestProviderFallbackHasExplicitProvenance(t *testing.T) {
	now := time.Now().UTC()
	primary := &fakeProvider{fail: true}
	fallback := &fakeProvider{coins: []apitypes.Coin{{ID: "bitcoin", Name: "Bitcoin", Symbol: "BTC", Rank: 1, Price: 43}}}
	service, err := NewServiceWithFallback(primary, fallback, Config{Provider: "primary", FallbackProvider: "secondary", CacheDir: t.TempDir(), FreshTTL: time.Minute, MaxStale: time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Prices(context.Background(), []string{"btc"}, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta.Provider != "secondary" || result.Meta.PrimaryProvider != "primary" || !result.Meta.FallbackUsed {
		t.Fatalf("metadata: %+v", result.Meta)
	}
	if primary.callCount() != 1 || fallback.callCount() != 1 {
		t.Fatalf("calls primary=%d fallback=%d", primary.callCount(), fallback.callCount())
	}
	primary.fail = false
	primary.coins = []apitypes.Coin{{ID: "bitcoin", Name: "Bitcoin", Symbol: "BTC", Rank: 1, Price: 44}}
	recovered, err := service.Prices(context.Background(), []string{"btc"}, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Meta.Provider != "primary" || recovered.Meta.FallbackUsed {
		t.Fatalf("recovery metadata: %+v", recovered.Meta)
	}
}

func TestPartialPaginationFallsBackWithoutCachingPartialData(t *testing.T) {
	now := time.Now().UTC()
	primary := &fakeProvider{coins: []apitypes.Coin{{ID: "partial", Name: "Partial", Rank: 1}}, pageErr: errors.New("page two failed")}
	fallback := &fakeProvider{coins: []apitypes.Coin{{ID: "bitcoin", Name: "Bitcoin", Symbol: "BTC", Rank: 1}}}
	service, err := NewServiceWithFallback(primary, fallback, Config{Provider: "primary", FallbackProvider: "secondary", CacheDir: t.TempDir(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Coins(context.Background(), "USD")
	if err != nil {
		t.Fatal(err)
	}
	coins, err := decodeCoins(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	if len(coins) != 1 || coins[0].ID != "bitcoin" || !result.Meta.FallbackUsed {
		t.Fatalf("partial primary escaped: coins=%+v meta=%+v", coins, result.Meta)
	}
	second, err := service.Coins(context.Background(), "USD")
	if err != nil {
		t.Fatal(err)
	}
	if second.Meta.CacheStatus != "hit" || primary.callCount() != 2 || fallback.callCount() != 1 {
		t.Fatalf("unexpected cache/calls: meta=%+v primary=%d fallback=%d", second.Meta, primary.callCount(), fallback.callCount())
	}
}

func TestReadinessFallbackMetadataAndProbeCache(t *testing.T) {
	now := time.Now().UTC()
	primary := &fakeProvider{pingErr: errors.New("offline")}
	fallback := &fakeProvider{}
	service, err := NewServiceWithFallback(primary, fallback, Config{Provider: "primary", FallbackProvider: "secondary", CacheDir: t.TempDir(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Ready(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Meta.Provider != "secondary" || !first.Meta.FallbackUsed || first.Meta.CacheStatus != "miss" {
		t.Fatalf("first readiness: %+v", first.Meta)
	}
	second, err := service.Ready(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Meta.CacheStatus != "hit" || primary.pingCalls != 1 || fallback.pingCalls != 1 {
		t.Fatalf("cached readiness: %+v ping calls=%d/%d", second.Meta, primary.pingCalls, fallback.pingCalls)
	}
}

func TestReadinessDoesNotProbeFallbackWhenPrimaryIsHealthy(t *testing.T) {
	now := time.Now().UTC()
	primary := &fakeProvider{}
	fallback := &fakeProvider{}
	service, err := NewServiceWithFallback(primary, fallback, Config{Provider: "primary", FallbackProvider: "secondary", CacheDir: t.TempDir(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Ready(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta.Provider != "primary" || result.Meta.FallbackUsed || primary.pingCalls != 1 || fallback.pingCalls != 0 {
		t.Fatalf("result=%+v ping calls=%d/%d", result.Meta, primary.pingCalls, fallback.pingCalls)
	}
}

func TestReadinessGivesFallbackItsOwnTimeout(t *testing.T) {
	now := time.Now().UTC()
	primary := &fakeProvider{pingDelay: 80 * time.Millisecond}
	fallback := &fakeProvider{}
	service, err := NewServiceWithFallback(primary, fallback, Config{Provider: "primary", FallbackProvider: "secondary", CacheDir: t.TempDir(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	service.readyProbeTimeout = 20 * time.Millisecond
	result, err := service.Ready(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta.Provider != "secondary" || !result.Meta.FallbackUsed || fallback.pingCalls != 1 {
		t.Fatalf("result=%+v fallback calls=%d", result.Meta, fallback.pingCalls)
	}
}

func TestReadinessHonorsCallerDeadline(t *testing.T) {
	now := time.Now().UTC()
	provider := &fakeProvider{pingDelay: time.Second}
	service := testService(t, provider, &now)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	result, err := service.Ready(ctx)
	if !errors.Is(err, ErrUnavailable) || time.Since(started) > 300*time.Millisecond {
		t.Fatalf("result=%+v err=%v elapsed=%s", result, err, time.Since(started))
	}
}

func TestReadinessSerializesConcurrentProbesAndExpiresAfterThirtySeconds(t *testing.T) {
	now := time.Now().UTC()
	provider := &fakeProvider{pingDelay: 25 * time.Millisecond}
	service := testService(t, provider, &now)
	const requests = 8
	var wg sync.WaitGroup
	errs := make(chan error, requests)
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.Ready(context.Background())
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
	if provider.pingCalls != 1 {
		t.Fatalf("expected one serialized probe, got %d", provider.pingCalls)
	}
	now = now.Add(31 * time.Second)
	if _, err := service.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.pingCalls != 2 {
		t.Fatalf("expected expired probe to refresh, got %d calls", provider.pingCalls)
	}
}

func TestReadinessWaiterHonorsCallerDeadline(t *testing.T) {
	now := time.Now().UTC()
	provider := &fakeProvider{pingDelay: 100 * time.Millisecond}
	service := testService(t, provider, &now)
	firstDone := make(chan error, 1)
	go func() {
		_, err := service.Ready(context.Background())
		firstDone <- err
	}()
	deadline := time.Now().Add(time.Second)
	for provider.pingCallCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("first readiness probe did not start")
		}
		time.Sleep(time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := service.Ready(ctx); !errors.Is(err, ErrUnavailable) || time.Since(started) > 100*time.Millisecond {
		t.Fatalf("err=%v elapsed=%s", err, time.Since(started))
	}
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}

func TestHealthyPrimaryNeverQueriesFallbackAndNotFoundDoesNotFallback(t *testing.T) {
	now := time.Now().UTC()
	primary := &fakeProvider{coins: []apitypes.Coin{{ID: "bitcoin", Name: "Bitcoin", Symbol: "BTC", Rank: 1}}}
	fallback := &fakeProvider{coins: []apitypes.Coin{{ID: "ethereum", Name: "Ethereum", Symbol: "ETH", Rank: 1}}}
	service, err := NewServiceWithFallback(primary, fallback, Config{Provider: "primary", FallbackProvider: "secondary", CacheDir: t.TempDir(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Prices(context.Background(), []string{"btc"}, "USD")
	if err != nil {
		t.Fatal(err)
	}
	if result.Meta.FallbackUsed || fallback.callCount() != 0 {
		t.Fatalf("unexpected fallback: %+v calls=%d", result.Meta, fallback.callCount())
	}
	if _, err := service.Coin(context.Background(), "ethereum", "USD"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
	if fallback.callCount() != 0 {
		t.Fatal("not-found request queried fallback")
	}
}

func TestPriceFetchPublishesCanonicalUpdate(t *testing.T) {
	now := time.Now().UTC()
	provider := &fakeProvider{coins: []apitypes.Coin{{ID: "bitcoin", Name: "Bitcoin", Symbol: "BTC", Rank: 1}}}
	service := testService(t, provider, &now)
	updates := make(chan Update, 2)
	cancel := service.SubscribeUpdates(func(update Update) { updates <- update })
	defer cancel()
	if _, err := service.Prices(context.Background(), []string{"BTC"}, "USD"); err != nil {
		t.Fatal(err)
	}
	first := <-updates
	second := <-updates
	if first.URI != "cointop://prices/btc?currency=USD" || second.URI != "cointop://prices/btc" {
		t.Fatalf("updates: %#v %#v", first, second)
	}
	if _, err := service.Prices(context.Background(), []string{"btc"}, "USD"); err != nil {
		t.Fatal(err)
	}
	select {
	case update := <-updates:
		t.Fatalf("cache hit published update: %#v", update)
	default:
	}
}

func TestAllChartRangeStartsAtEpoch(t *testing.T) {
	now := time.Date(2026, 8, 21, 1, 0, 0, 0, time.UTC)
	rangeID, start, end, err := chartWindow("all", now)
	if err != nil {
		t.Fatal(err)
	}
	if rangeID != "all" || start.Unix() != 0 || !end.Equal(now) {
		t.Fatalf("range=%s start=%s end=%s", rangeID, start, end)
	}
}
