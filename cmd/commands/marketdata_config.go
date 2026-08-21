package cmd

import (
	"os"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/cointop-sh/cointop/cointop"
	"github.com/cointop-sh/cointop/pkg/marketdata"
	"github.com/cointop-sh/cointop/pkg/pathutil"
)

type marketConfigFile struct {
	API       string `toml:"api"`
	CacheDir  string `toml:"cache_dir"`
	CoinGecko struct {
		APIKey    string `toml:"api_key"`
		ProAPIKey string `toml:"pro_api_key"`
	} `toml:"coingecko"`
	CoinMarketCap struct {
		ProAPIKey string `toml:"pro_api_key"`
	} `toml:"coinmarketcap"`
}

type daemonSettings struct {
	Provider marketdata.ProviderConfig
	CacheDir string
}

func loadDaemonSettings(configPath string) (daemonSettings, error) {
	settings := daemonSettings{
		Provider: marketdata.ProviderConfig{Name: "coingecko", PerPage: cointop.DefaultPerPage, MaxPages: cointop.DefaultMaxPages},
		CacheDir: pathutil.NormalizePath(cointop.DefaultCacheDir),
	}
	defaultPath := configPath == ""
	if defaultPath {
		configPath = cointop.DefaultConfigFilepath
	}
	configPath = pathutil.NormalizePath(configPath)
	if defaultPath {
		for _, possible := range cointop.PossibleConfigPaths {
			candidate := pathutil.NormalizePath(possible)
			if _, err := os.Stat(candidate); err == nil {
				configPath = candidate
				break
			}
		}
	}
	var fileConfig marketConfigFile
	if _, err := os.Stat(configPath); err == nil {
		if _, err := toml.DecodeFile(configPath, &fileConfig); err != nil {
			return settings, err
		}
		if fileConfig.API != "" {
			settings.Provider.Name = fileConfig.API
		}
		if fileConfig.CacheDir != "" {
			settings.CacheDir = pathutil.NormalizePath(fileConfig.CacheDir)
		}
		settings.Provider.CoinGeckoAPIKey = fileConfig.CoinGecko.APIKey
		settings.Provider.CoinGeckoProAPIKey = fileConfig.CoinGecko.ProAPIKey
		settings.Provider.CoinMarketCapAPIKey = fileConfig.CoinMarketCap.ProAPIKey
	} else if !os.IsNotExist(err) {
		return settings, err
	}
	if value := strings.TrimSpace(os.Getenv("COINTOP_API")); value != "" {
		settings.Provider.Name = value
	}
	if value := strings.TrimSpace(os.Getenv("COINTOP_CACHE_DIR")); value != "" {
		settings.CacheDir = pathutil.NormalizePath(value)
	}
	if value := strings.TrimSpace(os.Getenv("COINGECKO_API_KEY")); value != "" {
		settings.Provider.CoinGeckoAPIKey = value
	}
	if value := strings.TrimSpace(os.Getenv("COINGECKO_PRO_API_KEY")); value != "" {
		settings.Provider.CoinGeckoProAPIKey = value
	}
	if value := strings.TrimSpace(os.Getenv("CMC_PRO_API_KEY")); value != "" {
		settings.Provider.CoinMarketCapAPIKey = value
	}
	return settings, nil
}
