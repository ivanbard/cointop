package marketdata

import (
	"fmt"
	"strings"

	"github.com/cointop-sh/cointop/pkg/api"
)

type ProviderConfig struct {
	Name                string
	CoinMarketCapAPIKey string
	CoinGeckoAPIKey     string
	CoinGeckoProAPIKey  string
	PerPage             uint
	MaxPages            uint
}

func NewProvider(config ProviderConfig) (api.Interface, string, error) {
	name := strings.ToLower(strings.TrimSpace(config.Name))
	switch name {
	case "", "coingecko":
		return api.NewCG(&api.CoinGeckoConfig{
			PerPage:   config.PerPage,
			MaxPages:  config.MaxPages,
			ApiKey:    config.CoinGeckoAPIKey,
			ProApiKey: config.CoinGeckoProAPIKey,
		}), "coingecko", nil
	case "coinmarketcap":
		if config.CoinMarketCapAPIKey == "" {
			return nil, "", fmt.Errorf("coinmarketcap API key is required")
		}
		return api.NewCMC(config.CoinMarketCapAPIKey), "coinmarketcap", nil
	default:
		return nil, "", fmt.Errorf("unsupported provider %q", config.Name)
	}
}
