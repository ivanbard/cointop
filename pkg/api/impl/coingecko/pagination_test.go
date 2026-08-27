package coingecko

import (
	"context"
	"errors"
	"testing"
)

func TestCoinPaginationHonorsCanceledContextBeforeRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := <-NewCoinGecko(&Config{PerPage: 1, MaxPages: 1}).StreamAllCoinData(ctx, "USD")
	if !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("err=%v", result.Err)
	}
}
