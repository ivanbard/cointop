package cmd

import (
	"time"

	"github.com/cointop-sh/cointop/pkg/marketdata"
)

type marketServiceOptions struct {
	FreshTTL         time.Duration
	MaxStale         time.Duration
	FallbackProvider string
}

func newMarketService(settings daemonSettings, options marketServiceOptions) (*marketdata.Service, error) {
	provider, providerID, err := marketdata.NewProvider(settings.Provider)
	if err != nil {
		return nil, err
	}
	config := marketdata.Config{
		Provider:      providerID,
		CacheDir:      settings.CacheDir,
		FreshTTL:      options.FreshTTL,
		CurrenciesTTL: 24 * time.Hour,
		MaxStale:      options.MaxStale,
	}
	if options.FallbackProvider == "" {
		return marketdata.NewService(provider, config)
	}
	fallbackConfig := settings.Provider
	fallbackConfig.Name = options.FallbackProvider
	fallback, fallbackID, err := marketdata.NewProvider(fallbackConfig)
	if err != nil {
		return nil, err
	}
	config.FallbackProvider = fallbackID
	return marketdata.NewServiceWithFallback(provider, fallback, config)
}
