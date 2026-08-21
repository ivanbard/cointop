package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cointop-sh/cointop/pkg/api"
	apitypes "github.com/cointop-sh/cointop/pkg/api/types"
)

type Config struct {
	Provider      string
	CacheDir      string
	FreshTTL      time.Duration
	CurrenciesTTL time.Duration
	ChartTTL      time.Duration
	MaxStale      time.Duration
	Now           func() time.Time
}

type flight struct {
	done   chan struct{}
	result Result
	err    error
}

type Service struct {
	provider api.Interface
	name     string
	cache    *snapshotCache
	freshTTL time.Duration
	currTTL  time.Duration
	chartTTL time.Duration
	maxStale time.Duration
	now      func() time.Time
	mu       sync.Mutex
	flights  map[string]*flight
}

func NewService(provider api.Interface, config Config) (*Service, error) {
	if provider == nil {
		return nil, errors.New("provider is required")
	}
	if config.Provider == "" {
		return nil, errors.New("provider name is required")
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
		provider: provider,
		name:     strings.ToLower(config.Provider),
		cache:    cache,
		freshTTL: config.FreshTTL,
		currTTL:  config.CurrenciesTTL,
		chartTTL: config.ChartTTL,
		maxStale: config.MaxStale,
		now:      config.Now,
		flights:  make(map[string]*flight),
	}, nil
}

func (s *Service) Provider() string { return s.name }

func (s *Service) Health() Result {
	now := s.now().UTC()
	return Result{Data: Health{Status: "ready"}, Meta: Meta{
		Provider: s.name, FetchedAt: now, ExpiresAt: now,
		CacheStatus: "hit", Stale: false,
	}}
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
	return market, nil
}

func (s *Service) Coins(ctx context.Context, currency string) (Result, error) {
	currency = normalizeCurrency(currency)
	key := s.key("coins", currency, "all")
	return s.cached(ctx, key, currency, s.freshTTL, func() (interface{}, error) {
		ch := make(chan []apitypes.Coin)
		if err := s.provider.GetAllCoinData(currency, ch); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		var coins []apitypes.Coin
		for page := range ch {
			coins = append(coins, page...)
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
	return s.cached(ctx, s.key("global", currency, "market"), currency, s.freshTTL, func() (interface{}, error) {
		data, err := s.provider.GetGlobalMarketData(currency)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		return data, nil
	})
}

func (s *Service) Currencies(ctx context.Context) (Result, error) {
	return s.cached(ctx, s.key("currencies", "", "supported"), "", s.currTTL, func() (interface{}, error) {
		currencies := append([]string(nil), s.provider.SupportedCurrencies()...)
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
	return s.cached(ctx, s.key("coin-history", currency, identity), currency, s.chartTTL, func() (interface{}, error) {
		graph, fetchErr := s.provider.GetCoinGraphData(currency, coin.Symbol, coin.Name, start.Unix(), end.Unix())
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
	return s.cached(ctx, s.key("global-history", currency, rangeID), currency, s.chartTTL, func() (interface{}, error) {
		graph, fetchErr := s.provider.GetGlobalMarketGraphData(currency, start.Unix(), end.Unix())
		if fetchErr != nil {
			return nil, fmt.Errorf("%w: %v", ErrUnavailable, fetchErr)
		}
		return GlobalHistory{Range: rangeID, Start: start, End: end, Series: graph}, nil
	})
}

func (s *Service) ExchangeRate(ctx context.Context, from, to string) (Result, error) {
	from, to = normalizeCurrency(from), normalizeCurrency(to)
	return s.cached(ctx, s.key("exchange-rate", to, from), to, s.freshTTL, func() (interface{}, error) {
		rate, err := s.provider.GetExchangeRate(from, to, false)
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
	return s.cached(ctx, s.key("coin-link", "", strings.ToLower(identifier)), "", s.currTTL, func() (interface{}, error) {
		link := s.provider.CoinLink(identifier)
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

func (s *Service) cached(ctx context.Context, key, currency string, ttl time.Duration, fetch func() (interface{}, error)) (Result, error) {
	now := s.now().UTC()
	stale, hasStale := s.cache.get(key)
	if hasStale && now.Before(stale.ExpiresAt) {
		return resultFromSnapshot(stale, s.name, currency, "hit", false)
	}

	s.mu.Lock()
	if existing, ok := s.flights[key]; ok {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case <-existing.done:
			return existing.result, existing.err
		}
	}
	f := &flight{done: make(chan struct{})}
	s.flights[key] = f
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.flights, key)
		close(f.done)
		s.mu.Unlock()
	}()

	value, err := fetch()
	if err != nil {
		if hasStale && now.Before(stale.ExpiresAt.Add(s.maxStale)) {
			f.result, f.err = resultFromSnapshot(stale, s.name, currency, "stale", true)
			return f.result, f.err
		}
		f.err = err
		return Result{}, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		f.err = err
		return Result{}, err
	}
	entry := snapshot{Version: CacheVersion, Key: key, Data: raw, FetchedAt: now, ExpiresAt: now.Add(ttl)}
	// A persistence failure must not hide successfully fetched live data. The
	// in-memory entry is installed before the disk write is attempted.
	_ = s.cache.set(entry)
	f.result, f.err = resultFromSnapshot(entry, s.name, currency, "miss", false)
	return f.result, f.err
}

func resultFromSnapshot(entry snapshot, provider, currency, status string, stale bool) (Result, error) {
	var data interface{}
	if err := json.Unmarshal(entry.Data, &data); err != nil {
		return Result{}, err
	}
	return Result{Data: data, Meta: Meta{
		Provider: provider, Currency: currency, FetchedAt: entry.FetchedAt,
		ExpiresAt: entry.ExpiresAt, CacheStatus: status, Stale: stale,
	}}, nil
}

func (s *Service) key(resource, currency, identity string) string {
	return strings.Join([]string{"v1", s.name, resource, strings.ToUpper(currency), identity}, ":")
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
