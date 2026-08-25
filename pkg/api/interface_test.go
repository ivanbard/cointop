package api_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cointop-sh/cointop/pkg/api"
	"github.com/cointop-sh/cointop/pkg/api/types"
)

type legacyProvider struct{ err error }

func (p legacyProvider) Ping() error { return nil }
func (p legacyProvider) GetAllCoinData(_ string, ch chan []types.Coin) error {
	if p.err != nil {
		return p.err
	}
	go func() { defer close(ch); ch <- []types.Coin{{ID: "bitcoin"}} }()
	return nil
}
func (legacyProvider) GetCoinGraphData(string, string, string, int64, int64) (types.CoinGraph, error) {
	return types.CoinGraph{}, nil
}
func (legacyProvider) GetGlobalMarketGraphData(string, int64, int64) (types.MarketGraph, error) {
	return types.MarketGraph{}, nil
}
func (legacyProvider) GetGlobalMarketData(string) (types.GlobalMarketData, error) {
	return types.GlobalMarketData{}, nil
}
func (legacyProvider) GetCoinData(string, string) (types.Coin, error)          { return types.Coin{}, nil }
func (legacyProvider) GetCoinDataBatch([]string, string) ([]types.Coin, error) { return nil, nil }
func (legacyProvider) CoinLink(string) string                                  { return "" }
func (legacyProvider) SupportedCurrencies() []string                           { return nil }
func (legacyProvider) Price(string, string) (float64, error)                   { return 0, nil }
func (legacyProvider) GetExchangeRate(string, string, bool) (float64, error)   { return 0, nil }

func TestStreamAllCoinDataAdaptsLegacyProvider(t *testing.T) {
	result := <-api.StreamAllCoinData(context.Background(), legacyProvider{}, "USD")
	if result.Err != nil || len(result.Coins) != 1 {
		t.Fatalf("result=%+v", result)
	}
	want := errors.New("offline")
	result = <-api.StreamAllCoinData(context.Background(), legacyProvider{err: want}, "USD")
	if !errors.Is(result.Err, want) {
		t.Fatalf("error=%v", result.Err)
	}
}
