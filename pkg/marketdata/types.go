package marketdata

import (
	"errors"
	"time"
)

const CacheVersion = 1

var (
	ErrInvalidInput = errors.New("invalid input")
	ErrNotFound     = errors.New("not found")
	ErrUnavailable  = errors.New("market data unavailable")
)

type Meta struct {
	Provider    string    `json:"provider"`
	Currency    string    `json:"currency,omitempty"`
	FetchedAt   time.Time `json:"fetchedAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
	CacheStatus string    `json:"cacheStatus"`
	Stale       bool      `json:"stale"`
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
