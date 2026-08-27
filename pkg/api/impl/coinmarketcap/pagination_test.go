package coinmarketcap

import (
	"context"
	"errors"
	"testing"
)

func TestCoinPaginationHonorsCanceledContextBeforeRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := <-NewCMC("test").StreamAllCoinData(ctx, "USD")
	if !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("err=%v", result.Err)
	}
}
