package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cointop-sh/cointop/pkg/api"
	apitypes "github.com/cointop-sh/cointop/pkg/api/types"
)

type Config struct {
	Provider         string
	FallbackProvider string
	CacheDir         string
	FreshTTL         time.Duration
	CurrenciesTTL    time.Duration
	ChartTTL         time.Duration
	MaxStale         time.Duration
	Now              func() time.Time
}

type flight struct {
	done   chan struct{}
	result Result
	err    error
}

type Service struct {
	provider          api.Interface
	fallback          api.Interface
	name              string
	fallbackName      string
	cache             *snapshotCache
	freshTTL          time.Duration
	currTTL           time.Duration
	chartTTL          time.Duration
	maxStale          time.Duration
	now               func() time.Time
	mu                sync.Mutex
	flights           map[string]*flight
	updatesMu         sync.RWMutex
	updateSubscribers map[uint64]func(Update)
	nextSubscriber    uint64
	readyMu           sync.Mutex
	readyResult       Result
	readyErr          error
	readyExpires      time.Time
}

func NewService(provider api.Interface, config Config) (*Service, error) {
	return NewServiceWithFallback(provider, nil, config)
}

func NewServiceWithFallback(provider, fallback api.Interface, config Config) (*Service, error) {
	if provider == nil {
		return nil, errors.New("provider is required")
	}
	if config.Provider == "" {
		return nil, errors.New("provider name is required")
	}
	if fallback != nil && strings.TrimSpace(config.FallbackProvider) == "" {
		return nil, errors.New("fallback provider name is required")
	}
	if fallback != nil && strings.EqualFold(config.Provider, config.FallbackProvider) {
		return nil, errors.New("fallback provider must differ from primary provider")
	}
	if config.FreshTTL <= 0 {
		config.FreshTTL = time.Minute
	}
	if config.CurrenciesTTL <= 0 {
		config.CurrenciesTTL = 24 * time.Hour
	}
	if config.ChartTTL <= 0 {
		config.ChartTTL = 5 * time.Minute
	}
	if config.MaxStale <= 0 {
		config.MaxStale = 24 * time.Hour
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	cache, err := newSnapshotCache(filepath.Join(config.CacheDir, "marketdata-v1"))
	if err != nil {
		return nil, err
	}
	return &Service{
		provider:          provider,
		fallback:          fallback,
		name:              strings.ToLower(config.Provider),
		fallbackName:      strings.ToLower(config.FallbackProvider),
		cache:             cache,
		freshTTL:          config.FreshTTL,
		currTTL:           config.CurrenciesTTL,
		chartTTL:          config.ChartTTL,
		maxStale:          config.MaxStale,
		now:               config.Now,
		flights:           make(map[string]*flight),
		updateSubscribers: make(map[uint64]func(Update)),
	}, nil
}

func (s *Service) Provider() string { return s.name }

func (s *Service) Health() Result {
	now := s.now().UTC()
	return Result{Data: Health{Status: "ok"}, Meta: Meta{
		Provider: s.name, PrimaryProvider: s.name, FetchedAt: now, ExpiresAt: now,
		CacheStatus: "hit", Stale: false,
	}}
}

// Ready verifies the writable cache and that at least one configured provider
// responds. Probes are serialized, bounded to five seconds, and cached for 30s.
func (s *Service) Ready(ctx context.Context) (Result, error) {
	s.readyMu.Lock()
	defer s.readyMu.Unlock()
	now := s.now().UTC()
	if now.Before(s.readyExpires) {
		result := s.readyResult
		result.Meta.CacheStatus = "hit"
		return result, s.readyErr
	}

	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result := Result{Data: Health{Status: "not_ready"}, Meta: Meta{
		Provider: s.name, PrimaryProvider: s.name, FetchedAt: now,
		ExpiresAt: now.Add(30 * time.Second), CacheStatus: "miss",
	}}
	if err := s.cache.probe(probeCtx); err != nil {
		s.storeReady(result, fmt.Errorf("%w: cache probe: %v", ErrUnavailable, err))
		return result, s.readyErr
	}

	provider, err := s.readyProvider(probeCtx)
	if err != nil {
		s.storeReady(result, fmt.Errorf("%w: provider probe: %v", ErrUnavailable, err))
		return result, s.readyErr
	}
	result.Data = Health{Status: "ready"}
	result.Meta.Provider = provider
	result.Meta.FallbackUsed = provider != s.name
	s.storeReady(result, nil)
	return result, nil
}

func (s *Service) storeReady(result Result, err error) {
	s.readyResult, s.readyErr, s.readyExpires = result, err, result.Meta.ExpiresAt
}

func (s *Service) readyProvider(ctx context.Context) (string, error) {
	type response struct {
		name string
		err  error
	}
	count := 1
	responses := make(chan response, 2)
	go func() { responses <- response{name: s.name, err: s.provider.Ping()} }()
	if s.fallback != nil {
		count++
		go func() { responses <- response{name: s.fallbackName, err: s.fallback.Ping()} }()
	}
	var fallbackOK bool
	var failures []error
	for i := 0; i < count; i++ {
		select {
		case <-ctx.Done():
			if fallbackOK {
				return s.fallbackName, nil
			}
			return "", ctx.Err()
		case response := <-responses:
			if response.err == nil {
				if response.name == s.name {
					return s.name, nil
				}
				fallbackOK = true
				continue
			}
			failures = append(failures, fmt.Errorf("%s: %w", response.name, response.err))
			if fallbackOK {
				return s.fallbackName, nil
			}
		}
	}
	if fallbackOK {
		return s.fallbackName, nil
	}
	return "", errors.Join(failures...)
}

func (s *Service) Prices(ctx context.Context, coins []string, currency string) (Result, error) {
	coins = normalizeIdentifiers(coins)
	if len(coins) == 0 {
		return Result{}, fmt.Errorf("%w: at least one coin is required", ErrInvalidInput)
	}
	if len(coins) > 100 {
		return Result{}, fmt.Errorf("%w: at most 100 coins are allowed", ErrInvalidInput)
	}
	currency = normalizeCurrency(currency)
	market, err := s.Coins(ctx, currency)
	if err != nil {
		return Result{}, err
	}
	data, err := decodeCoins(market.Data)
	if err != nil {
		return Result{}, err
	}
	selected := selectCoins(data, coins)
	if len(selected) == 0 {
		return Result{}, fmt.Errorf("%w: no matching coins", ErrNotFound)
	}
	prices := make([]Price, 0, len(selected))
	for _, coin := range selected {
		prices = append(prices, Price{ID: coin.ID, Name: coin.Name, Symbol: coin.Symbol, Price: coin.Price})
	}
	market.Data = prices
	if market.Meta.CacheStatus == "miss" {
		baseURI := "cointop://prices/" + strings.Join(coins, ",")
		s.notifyUpdate(Update{URI: baseURI + "?currency=" + url.QueryEscape(currency)})
		if currency == "USD" {
			s.notifyUpdate(Update{URI: baseURI})
		}
	}
	return market, nil
}

// SubscribeUpdates registers a callback for newly fetched price resources.
func (s *Service) SubscribeUpdates(callback func(Update)) func() {
	if callback == nil {
		return func() {}
	}
	s.updatesMu.Lock()
	s.nextSubscriber++
	id := s.nextSubscriber
	s.updateSubscribers[id] = callback
	s.updatesMu.Unlock()
	return func() { s.updatesMu.Lock(); delete(s.updateSubscribers, id); s.updatesMu.Unlock() }
}

func (s *Service) notifyUpdate(update Update) {
	s.updatesMu.RLock()
	callbacks := make([]func(Update), 0, len(s.updateSubscribers))
	for _, callback := range s.updateSubscribers {
		callbacks = append(callbacks, callback)
	}
	s.updatesMu.RUnlock()
	for _, callback := range callbacks {
		callback(update)
	}
}

func (s *Service) Coins(ctx context.Context, currency string) (Result, error) {
	currency = normalizeCurrency(currency)
	return s.cached(ctx, "coins", currency, "all", s.freshTTL, "", func(provider api.Interface) (interface{}, error) {
		var coins []apitypes.Coin
		for result := range api.StreamAllCoinData(ctx, provider, currency) {
			if result.Err != nil {
				return nil, fmt.Errorf("%w: %v", ErrUnavailable, result.Err)
			}
			coins = append(coins, result.Coins...)
		}
		if len(coins) == 0 {
			return nil, fmt.Errorf("%w: provider returned no coins", ErrUnavailable)
		}
		sort.SliceStable(coins, func(i, j int) bool { return coins[i].Rank < coins[j].Rank })
		return coins, nil
	})
}

func (s *Service) Coin(ctx context.Context, identifier, currency string) (Result, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return Result{}, fmt.Errorf("%w: coin identifier is required", ErrInvalidInput)
	}
	currency = normalizeCurrency(currency)
	market, err := s.Coins(ctx, currency)
	if err != nil {
		return Result{}, err
	}
	coins, err := decodeCoins(market.Data)
	if err != nil {
		return Result{}, err
	}
	selected := selectCoins(coins, []string{strings.ToLower(identifier)})
	if len(selected) == 0 {
		return Result{}, fmt.Errorf("%w: coin %q", ErrNotFound, identifier)
	}
	market.Data = selected[0]
	return market, nil
}

func (s *Service) Global(ctx context.Context, currency string) (Result, error) {
	currency = normalizeCurrency(currency)
	return s.cached(ctx, "global", currency, "market", s.freshTTL, "", func(provider api.Interface) (interface{}, error) {
		data, err := provider.GetGlobalMarketData(currency)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		return data, nil
	})
}

func (s *Service) Currencies(ctx context.Context) (Result, error) {
	return s.cached(ctx, "currencies", "", "supported", s.currTTL, "", func(provider api.Interface) (interface{}, error) {
		currencies := append([]string(nil), provider.SupportedCurrencies()...)
		if len(currencies) == 0 {
			return nil, fmt.Errorf("%w: provider returned no currencies", ErrUnavailable)
		}
		sort.Strings(currencies)
		return currencies, nil
	})
}

func (s *Service) CoinHistory(ctx context.Context, identifier, currency, chartRange string) (Result, error) {
	coinResult, err := s.Coin(ctx, identifier, currency)
	if err != nil {
		return Result{}, err
	}
	var coin apitypes.Coin
	if raw, marshalErr := json.Marshal(coinResult.Data); marshalErr != nil {
		return Result{}, marshalErr
	} else if unmarshalErr := json.Unmarshal(raw, &coin); unmarshalErr != nil {
		return Result{}, unmarshalErr
	}
	currency = normalizeCurrency(currency)
	rangeID, start, end, err := chartWindow(chartRange, s.now().UTC())
	if err != nil {
		return Result{}, err
	}
	identity := strings.ToLower(coin.ID) + ":" + rangeID
	return s.cached(ctx, "coin-history", currency, identity, s.chartTTL, coinResult.Meta.Provider, func(provider api.Interface) (interface{}, error) {
		graph, fetchErr := provider.GetCoinGraphData(currency, coin.Symbol, coin.Name, start.Unix(), end.Unix())
		if fetchErr != nil {
			return nil, fmt.Errorf("%w: %v", ErrUnavailable, fetchErr)
		}
		return CoinHistory{ID: coin.ID, Name: coin.Name, Symbol: coin.Symbol, Range: rangeID, Start: start, End: end, Series: graph}, nil
	})
}

func (s *Service) GlobalHistory(ctx context.Context, currency, chartRange string) (Result, error) {
	currency = normalizeCurrency(currency)
	rangeID, start, end, err := chartWindow(chartRange, s.now().UTC())
	if err != nil {
		return Result{}, err
	}
	return s.cached(ctx, "global-history", currency, rangeID, s.chartTTL, "", func(provider api.Interface) (interface{}, error) {
		graph, fetchErr := provider.GetGlobalMarketGraphData(currency, start.Unix(), end.Unix())
		if fetchErr != nil {
			return nil, fmt.Errorf("%w: %v", ErrUnavailable, fetchErr)
		}
		return GlobalHistory{Range: rangeID, Start: start, End: end, Series: graph}, nil
	})
}

func (s *Service) ExchangeRate(ctx context.Context, from, to string) (Result, error) {
	from, to = normalizeCurrency(from), normalizeCurrency(to)
	return s.cached(ctx, "exchange-rate", to, from, s.freshTTL, "", func(provider api.Interface) (interface{}, error) {
		rate, err := provider.GetExchangeRate(from, to, false)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		return ExchangeRate{From: from, To: to, Rate: rate}, nil
	})
}

func (s *Service) CoinLink(ctx context.Context, identifier string) (Result, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return Result{}, fmt.Errorf("%w: coin identifier is required", ErrInvalidInput)
	}
	return s.cached(ctx, "coin-link", "", strings.ToLower(identifier), s.currTTL, "", func(provider api.Interface) (interface{}, error) {
		link := provider.CoinLink(identifier)
		if strings.TrimSpace(link) == "" {
			return nil, fmt.Errorf("%w: coin link", ErrNotFound)
		}
		return CoinLink{Identifier: identifier, URL: link}, nil
	})
}

func chartWindow(value string, now time.Time) (string, time.Time, time.Time, error) {
	rangeID := strings.ToLower(strings.TrimSpace(value))
	if rangeID == "" {
		rangeID = "24h"
	}
	var start time.Time
	switch rangeID {
	case "24h":
		start = now.Add(-24 * time.Hour)
	case "3d":
		start = now.Add(-3 * 24 * time.Hour)
	case "7d":
		start = now.Add(-7 * 24 * time.Hour)
	case "1m":
		start = now.AddDate(0, -1, 0)
	case "3m":
		start = now.AddDate(0, -3, 0)
	case "6m":
		start = now.AddDate(0, -6, 0)
	case "ytd":
		start = time.Date(now.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
	case "1y":
		start = now.AddDate(-1, 0, 0)
	case "all":
		start = time.Unix(0, 0).UTC()
	default:
		return "", time.Time{}, time.Time{}, fmt.Errorf("%w: unsupported range", ErrInvalidInput)
	}
	return rangeID, start, now, nil
}

type providerSource struct {
	name string
	api  api.Interface
}

func (s *Service) sources(preferred string) []providerSource {
	primary := providerSource{name: s.name, api: s.provider}
	if s.fallback == nil {
		return []providerSource{primary}
	}
	fallback := providerSource{name: s.fallbackName, api: s.fallback}
	if strings.EqualFold(preferred, fallback.name) {
		return []providerSource{fallback, primary}
	}
	return []providerSource{primary, fallback}
}

func (s *Service) cached(ctx context.Context, resource, currency, identity string, ttl time.Duration, preferred string, fetch func(api.Interface) (interface{}, error)) (Result, error) {
	now := s.now().UTC()
	flightKey := strings.Join([]string{"request", strings.ToLower(preferred), resource, strings.ToUpper(currency), identity}, ":")
	s.mu.Lock()
	if existing, ok := s.flights[flightKey]; ok {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case <-existing.done:
			result := existing.result
			if result.Meta.CacheStatus == "miss" {
				result.Meta.CacheStatus = "hit"
			}
			return result, existing.err
		}
	}
	f := &flight{done: make(chan struct{})}
	s.flights[flightKey] = f
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.flights, flightKey)
		close(f.done)
		s.mu.Unlock()
	}()

	type staleCandidate struct {
		entry    snapshot
		provider string
	}
	var staleEntries []staleCandidate
	var lastErr error
	for _, source := range s.sources(preferred) {
		key := s.key(source.name, resource, currency, identity)
		stale, hasStale := s.cache.get(key)
		if hasStale && now.Before(stale.ExpiresAt) {
			f.result, f.err = s.resultFromSnapshot(stale, source.name, currency, "hit", false)
			return f.result, f.err
		}
		if hasStale && now.Before(stale.ExpiresAt.Add(s.maxStale)) {
			staleEntries = append(staleEntries, staleCandidate{stale, source.name})
		}
		value, err := fetch(source.api)
		if err != nil {
			lastErr = err
			if !errors.Is(err, ErrUnavailable) {
				f.err = err
				return Result{}, err
			}
			continue
		}
		raw, err := json.Marshal(value)
		if err != nil {
			f.err = err
			return Result{}, err
		}
		entry := snapshot{Version: CacheVersion, Key: key, Data: raw, FetchedAt: now, ExpiresAt: now.Add(ttl)}
		_ = s.cache.set(entry)
		f.result, f.err = s.resultFromSnapshot(entry, source.name, currency, "miss", false)
		return f.result, f.err
	}
	if len(staleEntries) > 0 {
		candidate := staleEntries[0]
		f.result, f.err = s.resultFromSnapshot(candidate.entry, candidate.provider, currency, "stale", true)
		return f.result, f.err
	}
	f.err = lastErr
	return Result{}, lastErr
}

func (s *Service) resultFromSnapshot(entry snapshot, provider, currency, status string, stale bool) (Result, error) {
	var data interface{}
	if err := json.Unmarshal(entry.Data, &data); err != nil {
		return Result{}, err
	}
	return Result{Data: data, Meta: Meta{
		Provider: provider, PrimaryProvider: s.name, FallbackUsed: provider != s.name,
		Currency: currency, FetchedAt: entry.FetchedAt,
		ExpiresAt: entry.ExpiresAt, CacheStatus: status, Stale: stale,
	}}, nil
}

func (s *Service) key(provider, resource, currency, identity string) string {
	return strings.Join([]string{"v1", provider, resource, strings.ToUpper(currency), identity}, ":")
}

func normalizeCurrency(currency string) string {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		return "USD"
	}
	return currency
}

func normalizeIdentifiers(values []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func decodeCoins(value interface{}) ([]apitypes.Coin, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var coins []apitypes.Coin
	if err := json.Unmarshal(raw, &coins); err != nil {
		return nil, err
	}
	return coins, nil
}

func selectCoins(coins []apitypes.Coin, identifiers []string) []apitypes.Coin {
	selected := make([]apitypes.Coin, 0, len(identifiers))
	seen := make(map[string]bool)
	for _, identifier := range identifiers {
		identifier = strings.TrimSpace(identifier)
		var match *apitypes.Coin
		for priority := 0; priority < 3 && match == nil; priority++ {
			for i := range coins {
				coin := &coins[i]
				matches := (priority == 0 && (strings.EqualFold(coin.ID, identifier) || strings.EqualFold(coin.Slug, identifier))) ||
					(priority == 1 && strings.EqualFold(coin.Name, identifier)) ||
					(priority == 2 && strings.EqualFold(coin.Symbol, identifier))
				if matches {
					match = coin
					break
				}
			}
		}
		if match != nil && !seen[match.ID] {
			selected = append(selected, *match)
			seen[match.ID] = true
		}
	}
	return selected
}
