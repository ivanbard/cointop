package api

import (
	"context"

	"github.com/cointop-sh/cointop/pkg/api/types"
)

// CoinPageStreamer is the optional, context-aware coin pagination contract.
// Interface intentionally retains GetAllCoinData for third-party providers.
type CoinPageStreamer interface {
	StreamAllCoinData(context.Context, string) <-chan types.CoinPageResult
}

// StreamAllCoinData uses the context-aware contract when available and adapts
// legacy providers otherwise.
func StreamAllCoinData(ctx context.Context, provider Interface, convert string) <-chan types.CoinPageResult {
	if streamer, ok := provider.(CoinPageStreamer); ok {
		return streamer.StreamAllCoinData(ctx, convert)
	}

	results := make(chan types.CoinPageResult)
	go func() {
		defer close(results)
		pages := make(chan []types.Coin)
		callDone := make(chan error, 1)
		go func() { callDone <- provider.GetAllCoinData(convert, pages) }()
		callFinished := false
		for pages != nil || !callFinished {
			select {
			case <-ctx.Done():
				results <- types.CoinPageResult{Err: ctx.Err()}
				return
			case err := <-callDone:
				callFinished = true
				callDone = nil
				if err != nil {
					results <- types.CoinPageResult{Err: err}
					return
				}
			case page, ok := <-pages:
				if !ok {
					pages = nil
					continue
				}
				select {
				case results <- types.CoinPageResult{Coins: page}:
				case <-ctx.Done():
					results <- types.CoinPageResult{Err: ctx.Err()}
					return
				}
			}
		}
	}()
	return results
}

// Interface interface
type Interface interface {
	Ping() error
	GetAllCoinData(convert string, ch chan []types.Coin) error
	GetCoinGraphData(convert string, symbol string, name string, start int64, end int64) (types.CoinGraph, error)
	GetGlobalMarketGraphData(convert string, start int64, end int64) (types.MarketGraph, error)
	GetGlobalMarketData(convert string) (types.GlobalMarketData, error)
	GetCoinData(name string, convert string) (types.Coin, error)
	GetCoinDataBatch(names []string, convert string) ([]types.Coin, error)
	CoinLink(slug string) string
	SupportedCurrencies() []string
	Price(name string, convert string) (float64, error)
	GetExchangeRate(convertFrom, convertTo string, cached bool) (float64, error) // I don't love this caching
}
