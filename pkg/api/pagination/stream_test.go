package pagination

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cointop-sh/cointop/pkg/api/types"
)

func TestStreamOrdersPagesAndStopsOnEmpty(t *testing.T) {
	var calls []int
	var results []types.CoinPageResult
	for result := range Stream(context.Background(), 5, 0, func(page int) ([]types.Coin, error) {
		calls = append(calls, page)
		if page == 2 {
			return nil, nil
		}
		return []types.Coin{{ID: string(rune('a' + page))}}, nil
	}) {
		results = append(results, result)
	}
	if len(results) != 2 || results[0].Coins[0].ID != "a" || results[1].Coins[0].ID != "b" {
		t.Fatalf("results=%+v", results)
	}
	if len(calls) != 3 || calls[2] != 2 {
		t.Fatalf("calls=%v", calls)
	}
}

func TestStreamReportsLaterPageErrorOnce(t *testing.T) {
	want := errors.New("page two failed")
	var results []types.CoinPageResult
	for result := range Stream(context.Background(), 5, 0, func(page int) ([]types.Coin, error) {
		if page == 1 {
			return nil, want
		}
		return []types.Coin{{ID: "bitcoin"}}, nil
	}) {
		results = append(results, result)
	}
	if len(results) != 2 || len(results[0].Coins) != 1 || !errors.Is(results[1].Err, want) {
		t.Fatalf("results=%+v", results)
	}
}

func TestStreamCancellationInterruptsDelay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	results := Stream(ctx, 2, time.Hour, func(int) ([]types.Coin, error) {
		return []types.Coin{{ID: "bitcoin"}}, nil
	})
	if first := <-results; first.Err != nil {
		t.Fatal(first.Err)
	}
	cancel()
	if result := <-results; !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("result=%+v", result)
	}
}
