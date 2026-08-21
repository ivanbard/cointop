package marketdata

import (
	"errors"
	"time"

	apitypes "github.com/cointop-sh/cointop/pkg/api/types"
)

const CacheVersion = 1

var (
	ErrInvalidInput = errors.New("invalid input")
	ErrNotFound     = errors.New("not found")
	ErrUnavailable  = errors.New("market data unavailable")
)

type Meta struct {
	Provider        string    `json:"provider"`
	PrimaryProvider string    `json:"primaryProvider"`
	FallbackUsed    bool      `json:"fallbackUsed"`
	Currency        string    `json:"currency,omitempty"`
	FetchedAt       time.Time `json:"fetchedAt"`
	ExpiresAt       time.Time `json:"expiresAt"`
	CacheStatus     string    `json:"cacheStatus"`
	Stale           bool      `json:"stale"`
}

type Result struct {
	Data interface{} `json:"data"`
	Meta Meta        `json:"meta"`
}

type Price struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Symbol string  `json:"symbol"`
	Price  float64 `json:"price"`
}

type Health struct {
	Status string `json:"status"`
}

type CoinHistory struct {
	ID     string             `json:"id"`
	Name   string             `json:"name"`
	Symbol string             `json:"symbol"`
	Range  string             `json:"range"`
	Start  time.Time          `json:"start"`
	End    time.Time          `json:"end"`
	Series apitypes.CoinGraph `json:"series"`
}

type GlobalHistory struct {
	Range  string               `json:"range"`
	Start  time.Time            `json:"start"`
	End    time.Time            `json:"end"`
	Series apitypes.MarketGraph `json:"series"`
}

type ExchangeRate struct {
	From string  `json:"from"`
	To   string  `json:"to"`
	Rate float64 `json:"rate"`
}

type CoinLink struct {
	Identifier string `json:"identifier"`
	URL        string `json:"url"`
}
