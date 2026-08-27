package portfolio

import (
	"context"
	"encoding/json"
	"math"
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

func TestLoadAllKnownCostBasis(t *testing.T) {
	snapshot := loadSnapshot(t, "[[\"bitcoin\", 2, 10], [\"ethereum\", 1, 20]]")

	assertAggregates(t, snapshot, true, 40, 110, 275)
	if snapshot.TotalBalance != 150 || len(snapshot.Holdings) != 2 {
		t.Fatalf("snapshot: %#v", snapshot)
	}
	if !closeEnough(snapshot.Holdings[0].Allocation, 100.0/150*100) || !closeEnough(snapshot.Holdings[1].Allocation, 50.0/150*100) {
		t.Fatalf("allocations: %#v", snapshot.Holdings)
	}
}

func TestLoadMixedCostBasisOmitsAggregates(t *testing.T) {
	snapshot := loadSnapshot(t, "[[\"bitcoin\", 2, 10], [\"ethereum\", 1]]")

	assertIncompleteAggregates(t, snapshot)
	if snapshot.TotalBalance != 150 || snapshot.Holdings[0].CostBasis != 20 || snapshot.Holdings[0].ProfitLoss != 80 {
		t.Fatalf("known holding values or total balance changed: %#v", snapshot)
	}
	if !closeEnough(snapshot.Holdings[0].Allocation, 100.0/150*100) || !closeEnough(snapshot.Holdings[1].Allocation, 50.0/150*100) {
		t.Fatalf("allocations: %#v", snapshot.Holdings)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var encoded map[string]any
	if err := json.Unmarshal(raw, &encoded); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"totalCostBasis", "profitLoss", "profitLossPercent"} {
		if _, exists := encoded[field]; exists {
			t.Fatalf("incomplete snapshot contains %s: %s", field, raw)
		}
	}
}

func TestLoadAllUnknownCostBasisOmitsAggregates(t *testing.T) {
	snapshot := loadSnapshot(t, "[[\"bitcoin\", 2], [\"ethereum\", 1]]")

	assertIncompleteAggregates(t, snapshot)
	if snapshot.TotalBalance != 150 || len(snapshot.Holdings) != 2 {
		t.Fatalf("balances: %#v", snapshot)
	}
}

func TestLoadEmptyPortfolioHasCompleteZeroAggregates(t *testing.T) {
	snapshot := loadSnapshot(t, "[]")

	assertAggregates(t, snapshot, true, 0, 0, 0)
	if snapshot.TotalBalance != 0 || len(snapshot.Holdings) != 0 {
		t.Fatalf("snapshot: %#v", snapshot)
	}
}

func TestLoadConvertsForeignCurrencyCostBasis(t *testing.T) {
	snapshot := loadSnapshot(t, "[[\"bitcoin\", 2, 10, \"EUR\"]]")

	assertAggregates(t, snapshot, true, 40, 60, 150)
	holding := snapshot.Holdings[0]
	if holding.Balance != 100 || holding.CostBasis != 40 || holding.ProfitLoss != 60 || holding.Allocation != 100 {
		t.Fatalf("holding: %#v", holding)
	}
}

func loadSnapshot(t *testing.T, holdings string) Snapshot {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	content := []byte("[portfolio]\nholdings = " + holdings + "\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewService(fakeMarket{}, path).Load(context.Background(), "USD")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, ok := loaded.Data.(Snapshot)
	if !ok {
		t.Fatalf("snapshot: %#v", loaded.Data)
	}
	return snapshot
}

func assertAggregates(t *testing.T, snapshot Snapshot, complete bool, costBasis, profitLoss, profitLossPercent float64) {
	t.Helper()
	if snapshot.CostBasisComplete != complete || snapshot.TotalCostBasis == nil || *snapshot.TotalCostBasis != costBasis || snapshot.ProfitLoss == nil || *snapshot.ProfitLoss != profitLoss || snapshot.ProfitLossPercent == nil || *snapshot.ProfitLossPercent != profitLossPercent {
		t.Fatalf("aggregates: %#v", snapshot)
	}
}

func assertIncompleteAggregates(t *testing.T, snapshot Snapshot) {
	t.Helper()
	if snapshot.CostBasisComplete || snapshot.TotalCostBasis != nil || snapshot.ProfitLoss != nil || snapshot.ProfitLossPercent != nil {
		t.Fatalf("aggregates: %#v", snapshot)
	}
}

func closeEnough(got, want float64) bool {
	return math.Abs(got-want) < 1e-12
}
