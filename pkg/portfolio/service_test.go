package portfolio

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	apitypes "github.com/cointop-sh/cointop/pkg/api/types"
	"github.com/cointop-sh/cointop/pkg/marketdata"
)

type fakeMarket struct{}

func (fakeMarket) Provider() string { return "fake" }

func (fakeMarket) Coin(_ context.Context, id, currency string) (marketdata.Result, error) {
	return testResult(apitypes.Coin{ID: id, Name: "Bitcoin", Symbol: "BTC", Price: 50}, currency), nil
}
func (fakeMarket) ExchangeRate(_ context.Context, from, to string) (marketdata.Result, error) {
	return testResult(marketdata.ExchangeRate{From: from, To: to, Rate: 2}, to), nil
}
func testResult(data any, currency string) marketdata.Result {
	now := time.Date(2026, 8, 21, 1, 0, 0, 0, time.UTC)
	return marketdata.Result{Data: data, Meta: marketdata.Meta{Provider: "fake", Currency: currency, FetchedAt: now, ExpiresAt: now.Add(time.Minute), CacheStatus: "hit"}}
}

func TestLoadCalculatesPortfolioWithoutPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	content := []byte("[portfolio]\nholdings = [[\"bitcoin\", 2, 10, \"EUR\"]]\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewService(fakeMarket{}, path).Load(context.Background(), "USD")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, ok := loaded.Data.(Snapshot)
	if !ok || len(snapshot.Holdings) != 1 {
		t.Fatalf("snapshot: %#v", loaded.Data)
	}
	holding := snapshot.Holdings[0]
	if holding.Balance != 100 || holding.CostBasis != 40 || holding.ProfitLoss != 60 || holding.Allocation != 100 {
		t.Fatalf("holding: %#v", holding)
	}
	if snapshot.TotalBalance != 100 || snapshot.ProfitLossPercent != 150 {
		t.Fatalf("totals: %#v", snapshot)
	}
}
