// Package portfolio provides privacy-sensitive, read-only portfolio snapshots.
package portfolio

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	apitypes "github.com/cointop-sh/cointop/pkg/api/types"
	"github.com/cointop-sh/cointop/pkg/marketdata"
)

type MarketReader interface {
	Provider() string
	Coin(context.Context, string, string) (marketdata.Result, error)
	ExchangeRate(context.Context, string, string) (marketdata.Result, error)
}

type Holding struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Symbol            string  `json:"symbol"`
	Quantity          float64 `json:"quantity"`
	Price             float64 `json:"price"`
	Balance           float64 `json:"balance"`
	Allocation        float64 `json:"allocation"`
	BuyPrice          float64 `json:"buyPrice,omitempty"`
	BuyCurrency       string  `json:"buyCurrency,omitempty"`
	CostBasis         float64 `json:"costBasis,omitempty"`
	ProfitLoss        float64 `json:"profitLoss,omitempty"`
	ProfitLossPercent float64 `json:"profitLossPercent,omitempty"`
}

type Snapshot struct {
	Currency          string    `json:"currency"`
	Holdings          []Holding `json:"holdings"`
	TotalBalance      float64   `json:"totalBalance"`
	CostBasisComplete bool      `json:"costBasisComplete"`
	TotalCostBasis    *float64  `json:"totalCostBasis,omitempty"`
	ProfitLoss        *float64  `json:"profitLoss,omitempty"`
	ProfitLossPercent *float64  `json:"profitLossPercent,omitempty"`
}

type entry struct {
	identifier         string
	quantity, buyPrice float64
	buyCurrency        string
}
type Service struct {
	market     MarketReader
	configPath string
}

func NewService(market MarketReader, configPath string) *Service {
	return &Service{market: market, configPath: configPath}
}

func (s *Service) Load(ctx context.Context, currency string) (marketdata.Result, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		currency = "USD"
	}
	entries, err := loadEntries(s.configPath)
	if err != nil {
		return marketdata.Result{}, err
	}
	snapshot := Snapshot{Currency: currency, Holdings: make([]Holding, 0, len(entries)), CostBasisComplete: true}
	var totalCostBasis float64
	var meta marketdata.Meta
	for _, configured := range entries {
		coinResult, coinErr := s.market.Coin(ctx, configured.identifier, currency)
		if coinErr != nil {
			return marketdata.Result{}, coinErr
		}
		var coin apitypes.Coin
		if err := remarshal(coinResult.Data, &coin); err != nil {
			return marketdata.Result{}, err
		}
		holding := Holding{ID: coin.ID, Name: coin.Name, Symbol: coin.Symbol, Quantity: configured.quantity, Price: coin.Price, Balance: coin.Price * configured.quantity, BuyPrice: configured.buyPrice, BuyCurrency: strings.ToUpper(configured.buyCurrency)}
		if configured.buyPrice > 0 {
			costPrice := configured.buyPrice
			if holding.BuyCurrency != "" && holding.BuyCurrency != currency {
				rateResult, rateErr := s.market.ExchangeRate(ctx, holding.BuyCurrency, currency)
				if rateErr != nil {
					return marketdata.Result{}, rateErr
				}
				var rate marketdata.ExchangeRate
				if err := remarshal(rateResult.Data, &rate); err != nil {
					return marketdata.Result{}, err
				}
				costPrice *= rate.Rate
				meta = combineMeta(meta, rateResult.Meta)
			}
			holding.CostBasis = costPrice * configured.quantity
			holding.ProfitLoss = holding.Balance - holding.CostBasis
			if holding.CostBasis != 0 {
				holding.ProfitLossPercent = holding.ProfitLoss / holding.CostBasis * 100
			}
		} else {
			snapshot.CostBasisComplete = false
		}
		snapshot.TotalBalance += holding.Balance
		totalCostBasis += holding.CostBasis
		snapshot.Holdings = append(snapshot.Holdings, holding)
		meta = combineMeta(meta, coinResult.Meta)
	}
	for i := range snapshot.Holdings {
		if snapshot.TotalBalance != 0 {
			snapshot.Holdings[i].Allocation = snapshot.Holdings[i].Balance / snapshot.TotalBalance * 100
		}
	}
	if snapshot.CostBasisComplete {
		profitLoss := snapshot.TotalBalance - totalCostBasis
		profitLossPercent := float64(0)
		if totalCostBasis != 0 {
			profitLossPercent = profitLoss / totalCostBasis * 100
		}
		snapshot.TotalCostBasis = &totalCostBasis
		snapshot.ProfitLoss = &profitLoss
		snapshot.ProfitLossPercent = &profitLossPercent
	}
	sort.SliceStable(snapshot.Holdings, func(i, j int) bool { return snapshot.Holdings[i].Balance > snapshot.Holdings[j].Balance })
	if len(entries) == 0 {
		now := time.Now().UTC()
		meta = marketdata.Meta{Provider: s.market.Provider(), PrimaryProvider: s.market.Provider(), Currency: currency, FetchedAt: now, ExpiresAt: now, CacheStatus: "hit"}
	}
	meta.Currency = currency
	return marketdata.Result{Data: snapshot, Meta: meta}, nil
}

func loadEntries(path string) ([]entry, error) {
	var config struct {
		Portfolio map[string]interface{} `toml:"portfolio"`
	}
	if _, err := toml.DecodeFile(path, &config); err != nil {
		if os.IsNotExist(err) {
			return []entry{}, nil
		}
		return nil, fmt.Errorf("load portfolio: %w", err)
	}
	value, ok := config.Portfolio["holdings"]
	if !ok {
		return []entry{}, nil
	}
	rows, ok := value.([]interface{})
	if !ok {
		return nil, fmt.Errorf("load portfolio: invalid holdings")
	}
	result := make([]entry, 0, len(rows))
	for _, row := range rows {
		values, ok := row.([]interface{})
		if !ok || len(values) < 2 || len(values) > 4 {
			continue
		}
		identifier, ok := values[0].(string)
		if !ok || strings.TrimSpace(identifier) == "" {
			continue
		}
		quantity, err := number(values[1])
		if err != nil {
			return nil, fmt.Errorf("load portfolio: invalid quantity")
		}
		configured := entry{identifier: identifier, quantity: quantity}
		if len(values) >= 3 {
			configured.buyPrice, err = number(values[2])
			if err != nil {
				return nil, fmt.Errorf("load portfolio: invalid buy price")
			}
		}
		if len(values) == 4 {
			configured.buyCurrency, _ = values[3].(string)
		}
		result = append(result, configured)
	}
	return result, nil
}

func number(value any) (float64, error) {
	switch v := value.(type) {
	case float64:
		return v, nil
	case int64:
		return float64(v), nil
	case string:
		return strconv.ParseFloat(v, 64)
	default:
		return 0, fmt.Errorf("not a number")
	}
}
func remarshal(value, target any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}
func combineMeta(current, next marketdata.Meta) marketdata.Meta {
	if current.Provider == "" {
		return next
	}
	if next.FetchedAt.Before(current.FetchedAt) {
		current.FetchedAt = next.FetchedAt
	}
	if current.ExpiresAt.IsZero() || next.ExpiresAt.Before(current.ExpiresAt) {
		current.ExpiresAt = next.ExpiresAt
	}
	if next.Stale {
		current.Stale = true
		current.CacheStatus = "stale"
	} else if current.CacheStatus != "stale" && next.CacheStatus == "miss" {
		current.CacheStatus = "miss"
	}
	return current
}
