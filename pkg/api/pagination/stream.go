// Package pagination provides the shared, failure-aware page loop used by
// first-party market-data providers.
package pagination

import (
	"context"
	"time"

	"github.com/cointop-sh/cointop/pkg/api/types"
)

type Fetch func(page int) ([]types.Coin, error)

// Stream emits ordered, non-empty pages. An empty page completes normally;
// fetch failures and cancellation emit one terminal error before closing.
func Stream(ctx context.Context, maxPages int, delay time.Duration, fetch Fetch) <-chan types.CoinPageResult {
	results := make(chan types.CoinPageResult, 1)
	go func() {
		defer close(results)
		for page := 0; page < maxPages; page++ {
			if page > 0 && delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					results <- types.CoinPageResult{Err: ctx.Err()}
					return
				case <-timer.C:
				}
			}
			select {
			case <-ctx.Done():
				results <- types.CoinPageResult{Err: ctx.Err()}
				return
			default:
			}
			coins, err := fetch(page)
			if err != nil {
				results <- types.CoinPageResult{Err: err}
				return
			}
			if len(coins) == 0 {
				return
			}
			select {
			case results <- types.CoinPageResult{Coins: coins}:
			case <-ctx.Done():
				results <- types.CoinPageResult{Err: ctx.Err()}
				return
			}
		}
	}()
	return results
}
