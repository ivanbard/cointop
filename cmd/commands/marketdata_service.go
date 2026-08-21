package cmd

import (
	"time"

	"github.com/cointop-sh/cointop/pkg/marketdata"
)

type marketServiceOptions struct {
	FreshTTL time.Duration
	MaxStale time.Duration
}

func newMarketService(settings daemonSettings, options marketServiceOptions) (*marketdata.Service, error) {
	provider, providerID, err := marketdata.NewProvider(settings.Provider)
	if err != nil {
		return nil, err
	}
	return marketdata.NewService(provider, marketdata.Config{
		Provider:      providerID,
		CacheDir:      settings.CacheDir,
		FreshTTL:      options.FreshTTL,
		CurrenciesTTL: 24 * time.Hour,
		MaxStale:      options.MaxStale,
	})
}
